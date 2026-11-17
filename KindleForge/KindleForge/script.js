/*
  KindleForge (KPM Edition)
  Kindle GUI Appstore

  The page never talks to the network or to KPM directly. KFPM (the Go
  backend) keeps /mnt/us/.KFPM/catalog.json up to date; this script reads that
  file, shows it, and asks KFPM to change things via the Utild bridge. KFPM
  reports back through lipc properties (packageInstallStatus, ...).

  ES5 only: the Kindle browser is old.
*/

var KFPM_BIN = "/var/local/mesquite/KindleForge/binaries/KFPM";
var UTILD = "com.kindlemodding.utild";
var DATA_BASE = (document.location.protocol === "file:") ? "file:///mnt/us/.KFPM/" : "./dev/"; // ./dev/ = desktop testing only
var PAGE_SIZE = 3; // what fits on one e-ink screen without scrolling
var OP_TIMEOUT_MS = 6 * 60 * 1000;
var ID_RE = /^[A-Za-z0-9._-]{1,64}$/; // ids end up in a shell command line

var TABS = [
  { id: "all", label: "All" },
  { id: "installed", label: "Installed" },
  { id: "readers", label: "Readers" },
  { id: "games", label: "Games" },
  { id: "media", label: "Media" },
  { id: "utilities", label: "Utilities" }
];

var catalog = null;     // parsed catalog.json
var tab = "all";
var query = "";
var page = 0;
var busy = null;        // { kind: "install"|"update"|"uninstall"|"upgrade"|"sync", id, name }
var busyTimer = null;
var lastFailure = null; // { text } shown with a "View log" button
var notice = "";        // one-line result of the last operation
var updateInfo = null;  // parsed update.json: { current, latest, available, notes, ... }
var cleanup = null;     // parsed cleanup.json (last scan report)
var cleanSel = {};      // category id -> selected?
var cleanOpen = false;  // cleanup screen showing
var cleanError = "";
var confirmEmpty = false;

function $(id) { return document.getElementById(id); }

function el(tag, className, text) {
  var e = document.createElement(tag);
  if (className) e.className = className;
  if (text !== undefined && text !== null) e.textContent = text;
  return e;
}

function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

function kindleObj() { return window.kindle || top.kindle; }

/* ------------------------------------------------------------------ */
/* Chromebar / system menu                                             */
/* ------------------------------------------------------------------ */

function update() {
  var systemMenu = { // See https://m.media-amazon.com/images/I/A1ebeenJUXL.js (search 'systemChrome.js')
    "clientParams": {
      "profile": {
        "name": "default",
        "items": [
          { "id": "KFORGE_REFRESH", "state": "enabled", "handling": "notifyApp", "label": "Refresh Packages", "position": 0 },
          { "id": "KFORGE_UPGRADE", "state": "enabled", "handling": "notifyApp", "label": "Update All Packages", "position": 1 },
          { "id": "KFORGE_CHECKUPDATE", "state": "enabled", "handling": "notifyApp", "label": "Check for KindleForge Update", "position": 2 },
          { "id": "KFORGE_CLEANUP", "state": "enabled", "handling": "notifyApp", "label": "Clean Up Kindle", "position": 3 }
        ],
        "selectionMode": "none",
        "closeOnUse": true
      }
    }
  };

  if (window.kindle.chrome.isDecanterChromeEnabled) { // KPP modern payload
    var chromebar = {
      "appId": "xyz.penguins184.kindleforge",
      "topNavBar": {
        "template": "title",
        "title": "KindleForge",
        "buttons": [
          { "id": "KPP_MORE", "state": "enabled", "handling": "system" },
          { "id": "KPP_CLOSE", "state": "enabled", "handling": "system" }
        ]
      }
    };
    chromebar.systemMenu = systemMenu;
    window.kindle.messaging.sendMessage("com.lab126.chromebar", "configureChrome", chromebar);
  } else {
    var pillowbar = {
      "appId": "xyz.penguins184.kindleforge",
      "searchBar": {
        "clientParams": {
          "profile": {
            "name": "default",
            "buttons": [{ "id": "menu", "state": "enabled", "handling": "system" }]
          }
        }
      }
    };
    pillowbar.systemMenu = systemMenu;
    window.kindle.messaging.sendMessage("com.lab126.pillow", "configureChrome", pillowbar);
  }
}

window.kindle.appmgr.ongo = function() {
  update();
  window.kindle.messaging.receiveMessage("systemMenuItemSelected", function(eventType, id) {
    if (id === "KFORGE_REFRESH") startSync();
    else if (id === "KFORGE_UPGRADE") startUpgradeAll();
    else if (id === "KFORGE_CHECKUPDATE") startCheck();
    else if (id === "KFORGE_CLEANUP") startCleanup("scan", "Scanning your Kindle");
  });
};

// Wi-Fi prompt. Declining is fine now: the cached catalog still browses offline.
kindle.net.ensureConnection("all", false, function() {});

/* ------------------------------------------------------------------ */
/* Reading files written by KFPM                                       */
/* ------------------------------------------------------------------ */

// file:// XHR is unreliable in Mesquite, so load the file into a hidden
// iframe (the browser renders text files as a <pre>) and read it back.
function readFile(name, cb) {
  var done = false;
  var iframe = document.createElement("iframe");
  iframe.style.display = "none";

  function finish(text) {
    if (done) return;
    done = true;
    if (iframe.parentNode) iframe.parentNode.removeChild(iframe);
    cb(text);
  }

  iframe.addEventListener("load", function() {
    var text = null;
    try {
      var doc = iframe.contentDocument;
      if (doc && doc.body) text = doc.body.innerText || doc.body.textContent;
    } catch (e) { text = null; }
    finish(text && text.length ? text : null);
  });
  setTimeout(function() { finish(null); }, 3000);

  iframe.src = DATA_BASE + name + "?t=" + new Date().getTime();
  document.body.appendChild(iframe);
}

function loadCatalog(cb) {
  readFile("catalog.json", function(text) {
    var parsed = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
        if (!parsed || !parsed.packages) parsed = null;
      } catch (e) { parsed = null; }
    }
    if (parsed) catalog = parsed;
    if (cb) cb(parsed);
  });
}

function loadUpdateInfo(cb) {
  readFile("update.json", function(text) {
    var parsed = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
        if (!parsed || typeof parsed.current !== "string") parsed = null;
      } catch (e) { parsed = null; }
    }
    if (parsed) updateInfo = parsed;
    if (cb) cb(parsed);
  });
}

/* ------------------------------------------------------------------ */
/* Sending commands to KFPM                                            */
/* ------------------------------------------------------------------ */

function sendKFPM(args) {
  setTimeout(function() {
    kindleObj().messaging.sendStringMessage(UTILD, "runCMD", KFPM_BIN + " " + args);
  }, 10); // give the UI time to repaint first
}

function setBusy(op) {
  busy = op;
  if (busyTimer) clearTimeout(busyTimer);
  busyTimer = null;
  if (op) {
    busyTimer = setTimeout(function() { // never lock the UI forever
      busy = null;
      notice = "Timed out. Check your Wi-Fi and Refresh.";
      renderAll();
    }, OP_TIMEOUT_MS);
  }
}

var PAST = { install: "Installed", update: "Updated", uninstall: "Uninstalled", upgrade: "Updated all packages" };
var ING = { install: "Installing", update: "Updating", uninstall: "Uninstalling", upgrade: "Updating all packages", sync: "Refreshing package list",
            appupdate: "Updating KindleForge", check: "Checking for KindleForge updates" };
var FLAG = { install: "-i", update: "-u", uninstall: "-r" };

function startPackageOp(kind, pkg) {
  if (busy) { notice = "Another operation is in progress..."; renderBanner(); return; }
  if (!ID_RE.test(pkg.id)) { notice = "Invalid package id."; renderBanner(); return; }
  lastFailure = null;
  notice = "";
  setBusy({ kind: kind, id: pkg.id, name: pkg.name });
  renderAll();
  sendKFPM(FLAG[kind] + " " + pkg.id);
}

function startUpgradeAll() {
  if (busy) return;
  lastFailure = null;
  notice = "";
  setBusy({ kind: "upgrade", id: "", name: "" });
  renderAll();
  sendKFPM("-U");
}

function startAppUpdate() {
  if (busy) { notice = "Another operation is in progress..."; renderBanner(); return; }
  lastFailure = null;
  notice = "";
  setBusy({ kind: "appupdate", id: "", name: "" });
  renderAll();
  sendKFPM("-A");
}

function startCheck() {
  if (busy) return;
  lastFailure = null;
  notice = "";
  setBusy({ kind: "check", id: "", name: "" });
  renderAll();
  sendKFPM("-c");
}

// Cleanup: the page only ever sends one of four fixed commands plus category
// ids from a fixed list; KFPM derives the actual files itself.
var CLEAN_ID_RE = /^[a-z]{1,20}$/;
function startCleanup(cmd, label) {
  if (busy) return;
  var args = "-k " + cmd;
  if (cmd === "apply") {
    var ids = [];
    for (var k in cleanSel) {
      if (cleanSel.hasOwnProperty(k) && cleanSel[k] && CLEAN_ID_RE.test(k)) ids.push(k);
    }
    if (!ids.length) return;
    args += " " + ids.join(",");
  }
  confirmEmpty = false;
  cleanError = "";
  setBusy({ kind: "cleanup", id: "", name: "", label: label });
  if (cleanOpen) showCleanup(); else renderAll();
  sendKFPM(args);
}

function loadCleanup(cb) {
  readFile("cleanup.json", function(text) {
    var parsed = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
        if (!parsed || !parsed.categories) parsed = null;
      } catch (e) { parsed = null; }
    }
    if (parsed) {
      cleanup = parsed;
      for (var i = 0; i < parsed.categories.length; i++) {
        var c = parsed.categories[i];
        if (cleanSel[c.id] === undefined) cleanSel[c.id] = !!c["default"];
      }
    }
    if (cb) cb(parsed);
  });
}

function onCleanupResult(eventType, data) {
  if (!busy || busy.kind !== "cleanup") return;
  var success = typeof data === "string" && data.indexOf("success") !== -1;
  setBusy(null);
  if (!success) cleanError = "That did not work. Nothing else was changed.";
  loadCleanup(function(parsed) {
    if (!parsed && !cleanup) cleanError = "Could not read the scan results.";
    showCleanup();
  });
}

function startSync() {
  if (busy) return;
  lastFailure = null;
  notice = "";
  setBusy({ kind: "sync", id: "", name: "" });
  renderAll();
  sendKFPM("-s");
}

// Registered once (the old code registered a new handler on every tap).
function onOpResult(isUninstall) {
  return function(eventType, data) {
    if (!busy) return;
    var op = busy;
    if (op.kind === "sync" || op.kind === "appupdate" || op.kind === "check" || op.kind === "cleanup") return; // they report through their own properties
    var isUn = (op.kind === "uninstall");
    if (isUn !== isUninstall) return; // event for the other kind of operation
    var success = typeof data === "string" && data.indexOf("success") !== -1;
    setBusy(null);
    var what = op.name ? " " + op.name : "";
    if (success) {
      notice = PAST[op.kind] + what + ".";
      lastFailure = null;
    } else {
      notice = "Failed:" + (op.kind === "upgrade" ? " update all" : " " + op.kind + what);
      lastFailure = { text: notice };
    }
    loadCatalog(function() { renderAll(); });
  };
}

function onSyncResult(eventType, data) {
  if (!busy || busy.kind !== "sync") return;
  var success = typeof data === "string" && data.indexOf("success") !== -1;
  setBusy(null);
  if (success) { notice = "Package list updated."; lastFailure = null; }
  else { notice = "Could not reach the repositories. Showing the saved list."; lastFailure = { text: notice }; }
  loadCatalog(function() { loadUpdateInfo(function() { renderAll(); }); });
}

function onAppUpdateResult(eventType, data) {
  if (!busy || busy.kind !== "appupdate") return;
  var success = typeof data === "string" && data.indexOf("success") !== -1;
  setBusy(null);
  if (success) {
    notice = "KindleForge updated. Restarting...";
    lastFailure = null;
    renderAll();
    // The new files are already on disk; reloading picks them up.
    setTimeout(function() { window.location.reload(); }, 1500);
    return;
  }
  notice = "KindleForge update failed. Nothing was changed.";
  lastFailure = { text: notice };
  renderAll();
}

function onCheckResult(eventType, data) {
  if (!busy || busy.kind !== "check") return;
  var s = typeof data === "string" ? data : "";
  setBusy(null);
  if (s.indexOf("failure") !== -1) {
    notice = "Could not check for updates. Check your Wi-Fi.";
    lastFailure = { text: notice };
    renderAll();
    return;
  }
  notice = s.indexOf("available") !== -1 ? "A KindleForge update is available." : "KindleForge is up to date.";
  lastFailure = null;
  loadUpdateInfo(function() { renderAll(); });
}

/* ------------------------------------------------------------------ */
/* Filtering + pagination                                              */
/* ------------------------------------------------------------------ */

function matchesQuery(p) {
  if (!query) return true;
  var hay = (p.name + " " + p.author + " " + p.description + " " + p.id).toLowerCase();
  var words = query.toLowerCase().split(/\s+/);
  for (var i = 0; i < words.length; i++) {
    if (words[i] && hay.indexOf(words[i]) === -1) return false;
  }
  return true;
}

function matchesTab(p) {
  if (tab === "all") return true;
  if (tab === "installed") return p.installed;
  return p.category === tab;
}

function visiblePackages() {
  var out = [];
  if (!catalog) return out;
  for (var i = 0; i < catalog.packages.length; i++) {
    var p = catalog.packages[i];
    if (!p.supported && !p.installed) continue; // nothing for this device
    if (matchesTab(p) && matchesQuery(p)) out.push(p);
  }
  return out;
}

function pageCount(total) {
  return Math.max(1, Math.ceil(total / PAGE_SIZE));
}

function findPackage(id) {
  if (!catalog) return null;
  for (var i = 0; i < catalog.packages.length; i++) {
    if (catalog.packages[i].id === id) return catalog.packages[i];
  }
  return null;
}

/* ------------------------------------------------------------------ */
/* Rendering                                                           */
/* ------------------------------------------------------------------ */

function shorten(s, n) {
  s = (s || "").replace(/\s+/g, " ");
  return s.length > n ? s.substring(0, n - 1) + "…" : s;
}

// The one "smart" action a package offers right now.
function primaryAction(p) {
  if (!p.installed) return { kind: "install", label: "Install" };
  if (p.updateAvailable) return { kind: "update", label: "Update to " + p.version };
  return { kind: "uninstall", label: "Uninstall" };
}

function renderTabs() {
  var box = $("tabs");
  clear(box);
  for (var i = 0; i < TABS.length; i++) {
    var b = el("button", TABS[i].id === tab ? "active" : "", TABS[i].label);
    b.setAttribute("data-tab", TABS[i].id);
    box.appendChild(b);
  }
}

function renderBanner() {
  var box = $("banner");
  clear(box);
  var text = "";
  if (busy) {
    text = (busy.label || (ING[busy.kind] + (busy.name ? " " + busy.name : ""))) + "... Please wait.";
  } else if (notice) {
    text = notice;
  }
  if (!text) { box.className = "hidden"; return; }
  box.className = "";
  box.appendChild(el("div", "", text));
  if (!busy && lastFailure) {
    var b = el("button", "", "View log");
    b.setAttribute("data-action", "log");
    box.appendChild(b);
  }
}

function renderCard(p) {
  var card = el("article", "card");

  var top = el("div", "card-top");
  top.appendChild(el("h2", "title", p.name));
  var shown = p.installed ? p.installedVersion : p.version;
  var ver = (shown && shown !== "0.0.0") ? "v" + shown : "";
  top.appendChild(el("span", "version", ver));
  card.appendChild(top);

  card.appendChild(el("p", "author", p.author ? "by " + p.author : ""));

  if (p.installed) card.appendChild(el("span", "badge inverse", "INSTALLED"));
  if (p.updateAvailable) card.appendChild(el("span", "badge", "UPDATE " + p.version));
  if (!p.supported) card.appendChild(el("span", "badge", "NOT AVAILABLE FOR THIS DEVICE"));

  card.appendChild(el("p", "description", shorten(p.description, 110)));

  var actions = el("div", "actions");
  var act = primaryAction(p);
  var main = el("button", act.kind === "uninstall" ? "" : "primary", act.label);
  main.setAttribute("data-action", act.kind);
  main.setAttribute("data-id", p.id);
  if (!p.supported && !p.installed) main.disabled = true;
  actions.appendChild(main);

  var more = el("button", "", "Details");
  more.setAttribute("data-action", "details");
  more.setAttribute("data-id", p.id);
  actions.appendChild(more);

  card.appendChild(actions);
  return card;
}

function renderList() {
  var box = $("packages");
  clear(box);
  var list = visiblePackages();
  var pages = pageCount(list.length);
  if (page >= pages) page = pages - 1;
  if (page < 0) page = 0;

  if (!catalog) {
    box.appendChild(el("div", "empty", busy && busy.kind === "sync" ? "Fetching the package list..." : "No package list yet. Tap Refresh."));
  } else if (catalog.packages.length === 0) {
    var why = "The package list is empty: the last refresh could not reach the repositories. Check Wi-Fi, then tap Refresh.";
    if (catalog.repos && catalog.repos.length) {
      var parts = [];
      for (var ri = 0; ri < catalog.repos.length; ri++) parts.push(catalog.repos[ri].id + ": " + catalog.repos[ri].source);
      why += " (" + parts.join(", ") + ")";
    }
    box.appendChild(el("div", "empty", why));
  } else if (list.length === 0) {
    box.appendChild(el("div", "empty", query ? "Nothing matches “" + query + "”." : "Nothing here."));
  } else {
    var start = page * PAGE_SIZE;
    for (var i = start; i < list.length && i < start + PAGE_SIZE; i++) {
      box.appendChild(renderCard(list[i]));
    }
  }

  $("pageinfo").textContent = "Page " + (page + 1) + " of " + pages;
  $("prev").disabled = page <= 0;
  $("next").disabled = page >= pages - 1;
}

function renderUpdate() {
  var box = $("update");
  clear(box);
  if (!updateInfo || !updateInfo.available || busy) { box.className = "hidden"; return; }
  box.className = "";
  box.appendChild(el("div", "", "KindleForge " + updateInfo.latest + " is available (you have " + updateInfo.current + ")."));
  var row = el("div", "actions");
  var go = el("button", "", "Update KindleForge");
  go.setAttribute("data-action", "appupdate");
  row.appendChild(go);
  var more = el("button", "", "What's new");
  more.setAttribute("data-action", "whatsnew");
  row.appendChild(more);
  box.appendChild(row);
}

function renderFooter() {
  $("app-version").textContent = "KindleForge " + (updateInfo && updateInfo.current ? "v" + updateInfo.current : "");
  var s = "No Catalog";
  if (catalog) {
    var anyNet = false, anyCache = false;
    for (var i = 0; i < catalog.repos.length; i++) {
      if (catalog.repos[i].source === "network") anyNet = true;
      if (catalog.repos[i].source === "cache") anyCache = true;
    }
    s = (anyCache && !anyNet ? "Offline list" : "List") + ": " + (catalog.generatedAt || "").substring(0, 10);
    if (catalog.configSource) s += " (" + catalog.configSource + ")";
    if (catalog.backend) s += " | KPM: " + catalog.backend;
  }
  $("catalog-status").textContent = s;
}

function renderAll() {
  renderTabs();
  renderUpdate();
  renderBanner();
  renderList();
  renderFooter();
}

/* ------------------------------------------------------------------ */
/* Details / log overlay                                               */
/* ------------------------------------------------------------------ */

function closeOverlay() {
  cleanOpen = false;
  var o = $("overlay");
  o.className = "hidden";
  clear(o);
}

function metaRow(label, value) {
  var d = el("div", "");
  d.appendChild(el("strong", "", label + ": "));
  d.appendChild(document.createTextNode(value));
  return d;
}

function showDetails(p) {
  var o = $("overlay");
  clear(o);
  o.className = "";

  o.appendChild(el("h2", "", p.name));
  o.appendChild(el("p", "author", p.author ? "by " + p.author : ""));

  var meta = el("div", "meta");
  meta.appendChild(metaRow("Latest version", p.version || "n/a"));
  meta.appendChild(metaRow("Installed", p.installed ? ((p.installedVersion && p.installedVersion !== "0.0.0") ? p.installedVersion : "yes") : "no"));
  meta.appendChild(metaRow("Repository", p.repo || "local"));
  if (p.category) meta.appendChild(metaRow("Category", p.category));
  if (p.dependencies && p.dependencies.length) meta.appendChild(metaRow("Needs", p.dependencies.join(", ")));
  o.appendChild(meta);

  o.appendChild(el("p", "body", shorten(p.description, 600) || "No description."));

  var actions = el("div", "actions");
  var act = primaryAction(p);
  var main = el("button", act.kind === "uninstall" ? "" : "primary", act.label);
  main.setAttribute("data-action", act.kind);
  main.setAttribute("data-id", p.id);
  main.setAttribute("data-close", "1");
  if (!p.supported && !p.installed) main.disabled = true;
  actions.appendChild(main);

  if (p.installed && p.updateAvailable) { // update is primary; still allow removing
    var rm = el("button", "", "Uninstall");
    rm.setAttribute("data-action", "uninstall");
    rm.setAttribute("data-id", p.id);
    rm.setAttribute("data-close", "1");
    actions.appendChild(rm);
  }
  o.appendChild(actions);

  var row = el("div", "actions");
  var log = el("button", "", "View log");
  log.setAttribute("data-action", "log");
  row.appendChild(log);
  var back = el("button", "", "Back");
  back.setAttribute("data-action", "close");
  row.appendChild(back);
  o.appendChild(row);
}

function showWhatsNew() {
  var o = $("overlay");
  clear(o);
  o.className = "";
  var u = updateInfo || {};
  o.appendChild(el("h2", "", "KindleForge " + (u.latest || "")));
  var meta = el("div", "meta");
  meta.appendChild(metaRow("Installed", u.current || "unknown"));
  meta.appendChild(metaRow("Available", u.latest || "unknown"));
  if (u.size) meta.appendChild(metaRow("Download", Math.round(u.size / 1024 / 102.4) / 10 + " MB"));
  o.appendChild(meta);
  o.appendChild(el("p", "body", shorten(u.notes, 700) || "No release notes."));
  var row = el("div", "actions");
  var go = el("button", "primary", "Update KindleForge");
  go.setAttribute("data-action", "appupdate");
  go.setAttribute("data-close", "1");
  row.appendChild(go);
  var back = el("button", "", "Back");
  back.setAttribute("data-action", "close");
  row.appendChild(back);
  o.appendChild(row);
}

function fmtBytes(n) {
  if (!n || n < 1) return "0 B";
  if (n < 1024) return n + " B";
  if (n < 1048576) return Math.round(n / 102.4) / 10 + " KB";
  if (n < 1073741824) return Math.round(n / 104857.6) / 10 + " MB";
  return Math.round(n / 107374182.4) / 10 + " GB";
}

function plural(n, word) { return n + " " + word + (n === 1 ? "" : "s"); }

function cleanResultText(last) {
  if (!last) return "";
  var t = "";
  if (last.action === "trash") t = "Moved " + plural(last.count, "item") + " (" + fmtBytes(last.bytes) + ") to the trash.";
  else if (last.action === "restore") t = "Restored " + plural(last.count, "item") + ".";
  else if (last.action === "empty") t = "Permanently deleted " + plural(last.count, "item") + " (" + fmtBytes(last.bytes) + ").";
  if (last.skipped) t += " " + last.skipped + " skipped" + (last.action === "restore" ? " (the original place is in use)." : ".");
  if (last.errors && last.errors.length) t += " " + shorten(last.errors[0], 80);
  return t;
}

function cleanBtn(label, action, cls, disabled) {
  var b = el("button", cls || "", label);
  b.setAttribute("data-action", action);
  if (disabled) b.disabled = true;
  return b;
}

function showCleanup() {
  cleanOpen = true;
  var o = $("overlay");
  clear(o);
  o.className = "";
  o.appendChild(el("h2", "", "Clean up Kindle"));

  if (busy && busy.kind === "cleanup") {
    o.appendChild(el("div", "empty", (busy.label || "Working") + "... Please wait."));
    return;
  }
  if (!cleanup) {
    o.appendChild(el("div", "empty", cleanError || "No scan results yet."));
    var row0 = el("div", "actions");
    row0.appendChild(cleanBtn("Scan", "cleanup-rescan", "primary"));
    row0.appendChild(cleanBtn("Back", "cleanup-close"));
    o.appendChild(row0);
    return;
  }

  var meta = el("div", "meta");
  meta.appendChild(metaRow("Free space", fmtBytes(cleanup.freeBytes)));
  meta.appendChild(metaRow("In the trash", cleanup.trashItems ? plural(cleanup.trashItems, "item") + " (" + fmtBytes(cleanup.trashBytes) + ")" : "empty"));
  o.appendChild(meta);

  var msg = cleanError || cleanResultText(cleanup.last);
  if (msg) o.appendChild(el("div", "cleanmsg", msg));

  var any = false, picked = 0;
  for (var i = 0; i < cleanup.categories.length; i++) {
    var c = cleanup.categories[i];
    if (!c.count) continue;
    any = true;
    var on = !!cleanSel[c.id];
    if (on) picked++;
    var b = el("button", on ? "primary catbtn" : "catbtn", (on ? "ON   " : "OFF  ") + c.title + " \u2014 " + plural(c.count, "item") + ", " + fmtBytes(c.bytes));
    b.setAttribute("data-action", "cleanup-toggle");
    b.setAttribute("data-cat", c.id);
    o.appendChild(b);
    o.appendChild(el("p", "small", shorten(c.description, 100)));
  }
  if (!any) o.appendChild(el("div", "empty", "Nothing to clean up. Your Kindle is tidy."));

  var r1 = el("div", "actions");
  r1.appendChild(cleanBtn("Move selected to trash", "cleanup-apply", "primary", picked === 0));
  r1.appendChild(cleanBtn("Biggest items", "cleanup-biggest"));
  o.appendChild(r1);

  if (cleanup.trashItems > 0) {
    var r2 = el("div", "actions");
    r2.appendChild(cleanBtn("Restore from trash", "cleanup-restore"));
    r2.appendChild(cleanBtn(confirmEmpty ? "Tap again: delete " + plural(cleanup.trashItems, "item") + " forever" : "Empty trash",
                            "cleanup-empty", confirmEmpty ? "primary" : ""));
    o.appendChild(r2);
  }
  var r3 = el("div", "actions");
  r3.appendChild(cleanBtn("Scan again", "cleanup-rescan"));
  r3.appendChild(cleanBtn("Back", "cleanup-close"));
  o.appendChild(r3);
}

function showBiggest() {
  var o = $("overlay");
  clear(o);
  o.className = "";
  o.appendChild(el("h2", "", "Biggest items"));
  o.appendChild(el("p", "small", "Information only. KindleForge never deletes these."));
  var list = (cleanup && cleanup.biggest) || [];
  if (!list.length) o.appendChild(el("div", "empty", "Run a scan first."));
  for (var i = 0; i < list.length; i++) {
    var d = el("div", "bigrow");
    d.appendChild(el("strong", "", list[i].path));
    d.appendChild(document.createTextNode("  " + (list[i].approx ? "(too slow to measure)" : fmtBytes(list[i].bytes))));
    o.appendChild(d);
  }
  o.appendChild(cleanBtn("Back", "cleanup-back", "primary"));
}

function showLog() {
  var o = $("overlay");
  clear(o);
  o.className = "";
  o.appendChild(el("h2", "", "Output log"));
  var pre = el("pre", "", "Loading...");
  o.appendChild(pre);
  var back = el("button", "primary", "Back");
  back.setAttribute("data-action", "close");
  o.appendChild(back);

  readFile("last.log", function(text) {
    if (!text) { pre.textContent = "No log available."; return; }
    var lines = text.replace(/\s+$/, "").split("\n");
    if (lines.length > 22) lines = ["..."].concat(lines.slice(lines.length - 22));
    pre.textContent = lines.join("\n");
  });
}

/* ------------------------------------------------------------------ */
/* Events                                                              */
/* ------------------------------------------------------------------ */

function attr(node, name) {
  while (node && node !== document) {
    if (node.getAttribute && node.getAttribute(name) !== null) return { node: node, value: node.getAttribute(name) };
    node = node.parentNode;
  }
  return null;
}

function onTap(e) {
  var t = attr(e.target, "data-tab");
  if (t) {
    tab = t.value; page = 0;
    renderAll();
    return;
  }
  var a = attr(e.target, "data-action");
  if (!a || a.node.disabled) return;
  var idAttr = attr(a.node, "data-id");
  var p = idAttr ? findPackage(idAttr.value) : null;

  switch (a.value) {
    case "install":
    case "update":
    case "uninstall":
      if (!p) return;
      if (attr(a.node, "data-close")) closeOverlay();
      startPackageOp(a.value, p);
      break;
    case "details":
      if (p) showDetails(p);
      break;
    case "appupdate":
      if (attr(a.node, "data-close")) closeOverlay();
      startAppUpdate();
      break;
    case "whatsnew":
      showWhatsNew();
      break;
    case "log":
      showLog();
      break;
    case "close":
      closeOverlay();
      break;
    case "cleanup-toggle":
      var cid = a.node.getAttribute("data-cat");
      if (CLEAN_ID_RE.test(cid)) { cleanSel[cid] = !cleanSel[cid]; confirmEmpty = false; showCleanup(); }
      break;
    case "cleanup-apply":
      startCleanup("apply", "Moving files to the trash");
      break;
    case "cleanup-rescan":
      startCleanup("scan", "Scanning your Kindle");
      break;
    case "cleanup-restore":
      startCleanup("restore", "Restoring files");
      break;
    case "cleanup-empty":
      if (!confirmEmpty) { confirmEmpty = true; showCleanup(); }
      else startCleanup("empty", "Deleting the trash");
      break;
    case "cleanup-biggest":
      showBiggest();
      break;
    case "cleanup-back":
      showCleanup();
      break;
    case "cleanup-close":
      closeOverlay();
      break;
  }
}

var searchTimer = null;
function onSearchInput() {
  var v = $("search").value;
  if (v === query) return;
  query = v; page = 0;
  if (searchTimer) clearTimeout(searchTimer);
  // Debounced so each keystroke does not trigger a full e-ink refresh.
  searchTimer = setTimeout(function() { renderList(); }, 250);
}

document.addEventListener("DOMContentLoaded", function() {
  $("js-status").textContent = "JS Working!";

  document.addEventListener("click", onTap);
  $("search").addEventListener("input", onSearchInput);
  $("search").addEventListener("keyup", onSearchInput);
  $("refresh").addEventListener("click", startSync);
  $("prev").addEventListener("click", function() { if (page > 0) { page--; renderList(); } });
  $("next").addEventListener("click", function() {
    if (page < pageCount(visiblePackages().length) - 1) { page++; renderList(); }
  });

  var k = kindleObj();
  k.messaging.receiveMessage("deviceABI", function(eventType, ABI) {
    $("abi-status").textContent = "ABI: " + ABI;
  });
  k.messaging.receiveMessage("packageInstallStatus", onOpResult(false));
  k.messaging.receiveMessage("packageUninstallStatus", onOpResult(true));
  k.messaging.receiveMessage("catalogStatus", onSyncResult);
  k.messaging.receiveMessage("appUpdateStatus", onAppUpdateResult);
  k.messaging.receiveMessage("updateCheckStatus", onCheckResult);
  k.messaging.receiveMessage("cleanupStatus", onCleanupResult);

  sendKFPM("-abi");

  renderAll();
  // Show the saved catalog immediately (works offline); fetch one if none exists yet.
  loadUpdateInfo(function() { renderAll(); }); // installed version + any pending update
  loadCatalog(function(parsed) {
    renderAll();
    if (!parsed) startSync();
  });
});
