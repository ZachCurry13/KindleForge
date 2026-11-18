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
var STALE_MS = 24 * 3600 * 1000;      // refresh by itself when the list is older than this
var RETRY_MS = 6 * 3600 * 1000;       // ...but do not retry a failed attempt more often than this
var OLD_MS = 7 * 24 * 3600 * 1000;    // warn in the footer past this
var pageSize = 3; // cards per page: measured to fit the screen (recomputePageSize)
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
var lastInstalled = ""; // id of the package just installed, to offer "Rate it"
var detailNote = "";    // one-line message shown inside the details view
var confirmTake = "";   // id of a package whose "Take over" awaits a second tap
var sourcesPage = 0;    // Sources screen: current page
var confirmSource = ""; // id of a source whose "Turn on" awaits a second tap
var srcUrl = "";        // address typed on the Add a repository screen
var srcPreview = null;  // result of checking that address (source-preview.json)
var sourcesMode = "";   // which sources command is running
var confirmAdd = false; // "Add it" awaits a second tap
var SRC_URL_RE = /^https:\/\/[A-Za-z0-9:\/._~%+-]{4,280}$/; // also what the shell command may contain

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
          { "id": "KFORGE_CLEANUP", "state": "enabled", "handling": "notifyApp", "label": "Clean Up Kindle", "position": 3 },
          { "id": "KFORGE_SUGGEST", "state": "enabled", "handling": "notifyApp", "label": "Suggest an App", "position": 4 },
          { "id": "KFORGE_SOURCES", "state": "enabled", "handling": "notifyApp", "label": "Sources and Repositories", "position": 5 }
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
    else if (id === "KFORGE_SUGGEST") showSuggest();
    else if (id === "KFORGE_SOURCES") { sourcesPage = 0; confirmSource = ""; showSources(); }
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

// Ratings: the page only sends a package id from the catalog and 1-5.
function startRate(pkg, stars) {
  if (busy || !(stars >= 1 && stars <= 5) || !ID_RE.test(pkg.id)) return;
  lastFailure = null;
  detailNote = "";
  setBusy({ kind: "rate", id: pkg.id, name: pkg.name, label: "Sending your rating for " + pkg.name });
  showDetails(pkg);
  sendKFPM("-R " + pkg.id + " " + stars);
}

function onRateResult(eventType, data) {
  if (!busy || busy.kind !== "rate") return;
  var id = busy.id;
  var s = typeof data === "string" ? data : "";
  setBusy(null);
  if (s.indexOf("sent") !== -1) detailNote = "Thanks! Your rating was sent.";
  else if (s.indexOf("queued") !== -1) detailNote = "Saved. It will be sent next time you are online.";
  else detailNote = "Could not save your rating.";
  lastInstalled = "";
  loadCatalog(function() {
    renderAll();
    var p = findPackage(id);
    if (p && $("overlay").className !== "hidden") showDetails(p);
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
    if (op.kind === "sync" || op.kind === "appupdate" || op.kind === "check" || op.kind === "cleanup" || op.kind === "rate" || op.kind === "sources") return; // they report through their own properties
    var isUn = (op.kind === "uninstall");
    if (isUn !== isUninstall) return; // event for the other kind of operation
    var success = typeof data === "string" && data.indexOf("success") !== -1;
    setBusy(null);
    var what = op.name ? " " + op.name : "";
    if (success) {
      notice = PAST[op.kind] + what + ".";
      lastFailure = null;
      lastInstalled = (op.kind === "install") ? op.id : "";
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
  if (tab === "installed") return p.installed || p.detected;
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
  return Math.max(1, Math.ceil(total / pageSize));
}

// ---- fitting the screen -------------------------------------------------------

// Everything in style.css is in rem, so one number scales the whole interface.
// The design is 758 x 1024 css px; a bigger screen (the Kindle's browser
// reports a larger viewport than that) gets proportionally bigger text and buttons.
function applyScale() {
  var w = window.innerWidth || document.documentElement.clientWidth || 758;
  var h = window.innerHeight || document.documentElement.clientHeight || 1024;
  var scale = Math.min(w / 758, h / 1024); // the limiting dimension decides, so landscape stays sensible
  if (scale < 1) scale = 1;
  if (scale > 2.2) scale = 2.2;
  document.documentElement.style.fontSize = (Math.round(16 * scale * 100) / 100) + "px";
}

// The tallest card a package can produce (long text, both badges, rating).
var WORST_CARD = {
  id: "probe", name: "Wwwwwwwwwwwwwwwwwwww", author: "Wwwwwwwwwwwwww", repo: "x", category: "", supported: true,
  description: "Wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww wwwww",
  installed: true, installedVersion: "1.2.3", version: "1.3.0", updateAvailable: true, rating: 4.5, ratingCount: 12,
  dependencies: []
};

function boxExtras(node) {
  var cs = window.getComputedStyle(node);
  return (parseFloat(cs.marginTop) || 0) + (parseFloat(cs.marginBottom) || 0) + (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0);
}

// How many cards fit below the search bar and above the pager without
// scrolling? Measured with the tallest possible card, so every page fits.
function recomputePageSize() {
  var box = $("packages");
  clear(box);
  var probe = renderCard(WORST_CARD);
  box.appendChild(probe);
  var cardH = probe.getBoundingClientRect().height + (parseFloat(window.getComputedStyle(probe).marginBottom) || 0);
  clear(box);

  var container = document.querySelector(".container");
  var chrome = boxExtras(container) - (parseFloat(window.getComputedStyle(container).marginTop) || 0) - (parseFloat(window.getComputedStyle(container).marginBottom) || 0);
  for (var i = 0; i < container.children.length; i++) {
    var kid = container.children[i];
    if (window.getComputedStyle(kid).display === "none") continue;
    if (kid === box) { chrome += boxExtras(box); continue; }
    chrome += kid.getBoundingClientRect().height + (parseFloat(window.getComputedStyle(kid).marginTop) || 0) + (parseFloat(window.getComputedStyle(kid).marginBottom) || 0);
  }
  var avail = (window.innerHeight || 1024) - chrome - 6;
  var n = cardH > 0 ? Math.floor(avail / cardH) : 3;
  pageSize = Math.max(1, Math.min(12, n));
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
  if (p.readOnly) return null; // shown for information only (e.g. KOReader)
  if (p.detected && !p.installed) {
    var sure = confirmTake === p.id;
    return { kind: "install", takeover: true,
             label: sure ? "Tap again: replace your copy with v" + p.version : "Take over (install v" + p.version + ")" };
  }
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
  if (!busy && !lastFailure && lastInstalled && catalog && catalog.ratingsEnabled && findPackage(lastInstalled)) {
    var rb = el("button", "", "Rate it");
    rb.setAttribute("data-action", "details");
    rb.setAttribute("data-id", lastInstalled);
    box.appendChild(rb);
  }
}

function renderCard(p) {
  var card = el("article", "card");

  var top = el("div", "card-top");
  top.appendChild(el("h2", "title", p.name));
  var shown = p.installed ? p.installedVersion : p.version;
  var ver = (shown && shown !== "0.0.0") ? "v" + shown : (p.detected && p.installed ? "version unknown" : "");
  top.appendChild(el("span", "version", ver));
  card.appendChild(top);

  var byline = p.author ? "by " + p.author : (p.shortNote || "");
  if (p.rating) byline += (byline ? "  |  " : "") + "Rating " + p.rating + " / 5 (" + p.ratingCount + ")";
  card.appendChild(el("p", "author", byline));

  if (p.detected) card.appendChild(el("span", "badge inverse", "FOUND ON YOUR KINDLE"));
  else if (p.installed) card.appendChild(el("span", "badge inverse", "INSTALLED"));
  if (p.updateAvailable) card.appendChild(el("span", "badge", "UPDATE " + p.version));
  if (!p.supported) card.appendChild(el("span", "badge", "NOT AVAILABLE FOR THIS DEVICE"));

  card.appendChild(el("p", "description", shorten(p.description, 110)));

  var actions = el("div", "actions");
  var act = primaryAction(p);
  if (act) {
    var main = el("button", act.kind === "uninstall" ? "" : "primary", act.label);
    main.setAttribute("data-action", act.kind);
    main.setAttribute("data-id", p.id);
    if (act.takeover) main.setAttribute("data-takeover", "1");
    if (!p.supported && !p.installed) main.disabled = true;
    actions.appendChild(main);
  }

  var more = el("button", "", "Details");
  more.setAttribute("data-action", "details");
  more.setAttribute("data-id", p.id);
  actions.appendChild(more);

  card.appendChild(actions);
  return card;
}

function renderList() {
  recomputePageSize();
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
    var start = page * pageSize;
    for (var i = start; i < list.length && i < start + pageSize; i++) {
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

// Dates in catalog.json are RFC 3339 UTC ("2026-11-17T20:08:50Z"). Parsed by
// hand because old WebKit builds are unreliable with Date.parse.
function parseISO(iso) {
  var m = /^(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)Z$/.exec(iso || "");
  if (!m) return null;
  return Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]);
}

function ageMs(iso) {
  var t = parseISO(iso);
  if (t === null) return null;
  var d = new Date().getTime() - t;
  return d < 0 ? 0 : d; // a Kindle clock that is behind must not look "stale"
}

function agoText(ms) {
  var min = Math.floor(ms / 60000);
  if (min < 2) return "just now";
  if (min < 120) return min + " min ago";
  var h = Math.floor(min / 60);
  if (h < 48) return h + " h ago";
  return Math.floor(h / 24) + " days ago";
}

// Should the app refresh the list on its own? Yes when the last real update is
// old, unless an attempt was made recently (so being offline does not cause a
// slow retry on every launch).
function shouldAutoSync() {
  if (!catalog) return true;
  var synced = ageMs(catalog.syncedAt);
  if (synced !== null && synced < STALE_MS) return false;
  var tried = ageMs(catalog.attemptedAt);
  if (tried !== null && tried < RETRY_MS) return false;
  return true;
}

function renderFooter() {
  $("app-version").textContent = "KindleForge " + (updateInfo && updateInfo.current ? "v" + updateInfo.current : "");
  var s = "No Catalog";
  if (catalog) {
    var synced = ageMs(catalog.syncedAt);
    if (synced === null) s = "List: not updated yet";
    else if (synced > OLD_MS) s = "List is " + agoText(synced).replace(" ago", " old") + ": tap Refresh";
    else s = "List updated " + agoText(synced);
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
  detailNote = "";
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
  var hasVer = p.installedVersion && p.installedVersion !== "0.0.0";
  meta.appendChild(metaRow("Installed", p.detected ? (p.installed && hasVer ? p.installedVersion + " (found on your Kindle)" : "found on your Kindle, version unknown") : (p.installed ? (hasVer ? p.installedVersion : "yes") : "no")));
  meta.appendChild(metaRow("Repository", p.repo || "local"));
  if (p.category) meta.appendChild(metaRow("Category", p.category));
  if (p.dependencies && p.dependencies.length) meta.appendChild(metaRow("Needs", p.dependencies.join(", ")));
  o.appendChild(meta);

  o.appendChild(el("p", "body", shorten(p.description, 600) || "No description."));

  if (p.note) o.appendChild(el("p", "body", shorten(p.note, 300)));
  if (detailNote) o.appendChild(el("div", "cleanmsg", detailNote));
  if (busy && busy.kind === "rate") {
    o.appendChild(el("div", "cleanmsg", (busy.label || "Working") + "..."));
  } else if (catalog && catalog.ratingsEnabled && p.installed && !p.readOnly && !p.detected) {
    o.appendChild(el("p", "small", p.rating ? "Rating " + p.rating + " / 5 from " + p.ratingCount + " people. Anonymous." : "Not enough ratings yet. Anonymous."));
    var rr = el("div", "actions");
    for (var st = 1; st <= 5; st++) {
      var rbtn = el("button", p.myRating === st ? "primary" : "", String(st));
      rbtn.setAttribute("data-action", "rate");
      rbtn.setAttribute("data-id", p.id);
      rbtn.setAttribute("data-stars", String(st));
      rr.appendChild(rbtn);
    }
    o.appendChild(el("p", "small", p.myRating ? "Your rating: " + p.myRating + " (tap a number to change it)" : "Rate it, 1 (poor) to 5 (great):"));
    o.appendChild(rr);
  }

  var actions = el("div", "actions");
  var act = primaryAction(p);
  var main = null;
  if (act) {
    main = el("button", act.kind === "uninstall" ? "" : "primary", act.label);
    main.setAttribute("data-action", act.kind);
    main.setAttribute("data-id", p.id);
    if (act.takeover) main.setAttribute("data-takeover", "1"); else main.setAttribute("data-close", "1");
    if (!p.supported && !p.installed) main.disabled = true;
    actions.appendChild(main);
  }

  if (p.installed && p.updateAvailable && !p.readOnly) { // update is primary; still allow removing
    var rm = el("button", "", "Uninstall");
    rm.setAttribute("data-action", "uninstall");
    rm.setAttribute("data-id", p.id);
    rm.setAttribute("data-close", "1");
    actions.appendChild(rm);
  }
  if (main) o.appendChild(actions);

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

// ---- Sources: which repositories packages come from ---------------------------

var TRUST_LABEL = { reviewed: "REVIEWED", community: "COMMUNITY: NOT REVIEWED", yours: "ADDED BY YOU" };

function startSources(cmd, arg, label) {
  if (busy) return;
  if (cmd === "check" || cmd === "add") { if (!SRC_URL_RE.test(arg)) return; }
  else if (!ID_RE.test(arg)) return;
  sourcesMode = cmd;
  confirmSource = ""; confirmAdd = false;
  setBusy({ kind: "sources", id: "", name: "", label: label });
  showSourcesBusy();
  sendKFPM("-g " + cmd + " " + arg);
}

function showSourcesBusy() {
  var o = $("overlay");
  clear(o);
  o.className = "";
  o.appendChild(el("h2", "", "Sources"));
  o.appendChild(el("div", "empty", (busy && busy.label ? busy.label : "Working") + "... Please wait."));
}

function loadPreview(cb) {
  readFile("source-preview.json", function(text) {
    var p = null;
    if (text) { try { p = JSON.parse(text); } catch (e) { p = null; } }
    srcPreview = p;
    if (cb) cb(p);
  });
}

function onSourcesResult(eventType, data) {
  if (!busy || busy.kind !== "sources") return;
  var success = typeof data === "string" && data.indexOf("success") !== -1;
  var mode = sourcesMode;
  setBusy(null);
  if (mode === "check" || (mode === "add" && !success)) {
    loadPreview(function() { showAddSource(); });
    return;
  }
  if (mode === "add" && success) { srcUrl = ""; srcPreview = null; }
  loadCatalog(function() { renderAll(); showSources(); });
}

function showSources() {
  var o = $("overlay");
  clear(o);
  o.className = "";
  if (busy && busy.kind === "sources") { showSourcesBusy(); return; }
  o.appendChild(el("h2", "", "Sources"));
  o.appendChild(el("p", "small", "Reviewed sources are on. Community sources and ones you add run other people's code on your Kindle: switch them on only if you trust them."));

  var list = (catalog && catalog.sources) || [];
  var PER = 3, pages = Math.max(1, Math.ceil(list.length / PER));
  if (sourcesPage >= pages) sourcesPage = pages - 1;
  for (var i = sourcesPage * PER; i < list.length && i < sourcesPage * PER + PER; i++) {
    var s = list[i];
    var card = el("article", "card");
    var top = el("div", "card-top");
    top.appendChild(el("h2", "title", s.name));
    top.appendChild(el("span", "version", s.enabled ? "ON" : "OFF"));
    card.appendChild(top);
    var line = (TRUST_LABEL[s.trust] || s.trust) + " | " + (s.format === "zen" ? "KOReader plugins" : "KPM packages");
    if (s.enabled && s.packages) line += " | " + s.packages + (s.fetched === "cache" ? " (saved copy)" : "");
    card.appendChild(el("p", "author", line));
    if (s.note) card.appendChild(el("p", "small", shorten(s.note, 110)));
    var row = el("div", "actions");
    var sure = confirmSource === s.id;
    var tog = el("button", s.enabled ? "" : "primary", s.enabled ? "Turn off" : (sure ? "Tap again: turn on (not reviewed)" : "Turn on"));
    tog.setAttribute("data-action", "source-toggle");
    tog.setAttribute("data-sid", s.id);
    row.appendChild(tog);
    if (s.user) {
      var rm = el("button", "", "Remove");
      rm.setAttribute("data-action", "source-remove");
      rm.setAttribute("data-sid", s.id);
      row.appendChild(rm);
    }
    card.appendChild(row);
    o.appendChild(card);
  }
  var pager = el("div", "actions");
  var prev = el("button", "", "\u00AB Page Up"); prev.setAttribute("data-action", "sources-prev"); if (sourcesPage <= 0) prev.disabled = true;
  var next = el("button", "", "Page Down \u00BB"); next.setAttribute("data-action", "sources-next"); if (sourcesPage >= pages - 1) next.disabled = true;
  pager.appendChild(prev);
  pager.appendChild(el("span", "small", "Page " + (sourcesPage + 1) + " of " + pages));
  pager.appendChild(next);
  o.appendChild(pager);
  var bottom = el("div", "actions");
  var add = el("button", "primary", "Add a repository");
  add.setAttribute("data-action", "source-addform");
  bottom.appendChild(add);
  var back = el("button", "", "Back");
  back.setAttribute("data-action", "close");
  bottom.appendChild(back);
  o.appendChild(bottom);
}

function showAddSource() {
  var o = $("overlay");
  clear(o);
  o.className = "";
  if (busy && busy.kind === "sources") { showSourcesBusy(); return; }
  o.appendChild(el("h2", "", "Add a repository"));
  o.appendChild(el("p", "body", "Type the web address of a package repository (the manifest.json file). It must start with https://"));
  o.appendChild(el("p", "small", "Only add repositories you trust. Their packages are not checked by anyone and can run code on your Kindle."));
  var input = document.createElement("input");
  input.type = "text";
  input.id = "srcurl";
  input.value = srcUrl;
  input.setAttribute("placeholder", "https://example.github.io/repo/manifest.json");
  input.setAttribute("autocomplete", "off");
  input.setAttribute("autocorrect", "off");
  input.setAttribute("autocapitalize", "off");
  input.addEventListener("input", function() { srcUrl = input.value.replace(/^\s+|\s+$/g, ""); confirmAdd = false; });
  o.appendChild(input);

  var p = srcPreview;
  if (p && p.url === srcUrl) {
    if (!p.ok) {
      o.appendChild(el("div", "cleanmsg", "Not usable: " + (p.error || "unknown problem")));
    } else {
      var info = p.name + " (" + (p.format === "zen" ? "KOReader plugins" : "KPM packages") + "), " + p.packages + " packages";
      if (p.sample && p.sample.length) info += ". For example: " + p.sample.join(", ");
      o.appendChild(el("div", "cleanmsg", "Found: " + info + (p.exists ? ". It is already in your list." : ".")));
    }
  } else if (srcUrl && !SRC_URL_RE.test(srcUrl)) {
    o.appendChild(el("p", "small", "That does not look like an allowed address (letters, digits and : / . _ ~ % + - only)."));
  }
  var row = el("div", "actions");
  var chk = el("button", p && p.ok && p.url === srcUrl ? "" : "primary", "Check");
  chk.setAttribute("data-action", "source-check");
  row.appendChild(chk);
  if (p && p.ok && !p.exists && p.url === srcUrl) {
    var go = el("button", "primary", confirmAdd ? "Tap again: add it" : "Add it");
    go.setAttribute("data-action", "source-add");
    row.appendChild(go);
  }
  o.appendChild(row);
  var back = el("button", "", "Back");
  back.setAttribute("data-action", "source-back");
  o.appendChild(back);
}

function showSuggest() {
  var o = $("overlay");
  clear(o);
  o.className = "";
  o.appendChild(el("h2", "", "Suggest an app"));
  var url = catalog && catalog.suggestURL;
  if (!url) {
    o.appendChild(el("p", "body", "Not available yet. Tap Refresh, then try again."));
  } else {
    o.appendChild(el("p", "body", "Want an app in the store? On your phone or computer, open this address and fill in the short form:"));
    o.appendChild(el("pre", "", url));
    o.appendChild(el("p", "small", "Add a thumbs up to suggestions you also want: the most wanted are reviewed first. Every app is read and checked by hand before it is added, because install scripts run as root on your Kindle."));
  }
  var back = cleanBtn("Back", "close", "primary");
  o.appendChild(back);
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
  o.appendChild(el("p", "small", "Full history: kindleforge-log.txt in the top folder of the Kindle's storage (over USB). Clean Up can remove it."));
  var back = el("button", "primary", "Back");
  back.setAttribute("data-action", "close");
  o.appendChild(back);

  readFile("last-log.txt", function(text) {
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
    tab = t.value; page = 0; confirmTake = "";
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
      if (attr(a.node, "data-takeover") && confirmTake !== p.id) { // replacing a hand-made copy: ask twice
        confirmTake = p.id;
        if ($("overlay").className !== "hidden") showDetails(p); else renderList();
        return;
      }
      confirmTake = "";
      if ($("overlay").className !== "hidden") closeOverlay();
      startPackageOp(a.value, p);
      break;
    case "details":
      if (p) { detailNote = ""; confirmTake = ""; showDetails(p); }
      break;
    case "rate":
      if (p) startRate(p, parseInt(a.node.getAttribute("data-stars"), 10));
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
    case "source-toggle":
      var sid = a.node.getAttribute("data-sid");
      var src = null, list = (catalog && catalog.sources) || [];
      for (var q = 0; q < list.length; q++) if (list[q].id === sid) src = list[q];
      if (!src) return;
      if (!src.enabled && src.trust !== "reviewed" && confirmSource !== sid) { confirmSource = sid; showSources(); return; }
      startSources(src.enabled ? "disable" : "enable", sid, src.enabled ? "Turning it off" : "Turning it on and fetching its packages");
      break;
    case "source-remove":
      startSources("remove", a.node.getAttribute("data-sid"), "Removing it");
      break;
    case "sources-prev":
      if (sourcesPage > 0) { sourcesPage--; showSources(); }
      break;
    case "sources-next":
      sourcesPage++; showSources();
      break;
    case "source-addform":
      srcPreview = null; confirmAdd = false;
      showAddSource();
      break;
    case "source-check":
      var inp = $("srcurl");
      if (inp) srcUrl = inp.value.replace(/^\s+|\s+$/g, "");
      if (!SRC_URL_RE.test(srcUrl)) { showAddSource(); return; }
      startSources("check", srcUrl, "Checking the address");
      break;
    case "source-add":
      if (!confirmAdd) { confirmAdd = true; showAddSource(); return; }
      startSources("add", srcUrl, "Adding the repository");
      break;
    case "source-back":
      showSources();
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

var resizeTimer = null;
window.addEventListener("resize", function() { // rotation, or the browser bar appearing
  if (resizeTimer) clearTimeout(resizeTimer);
  resizeTimer = setTimeout(function() { applyScale(); renderList(); }, 250);
});

document.addEventListener("DOMContentLoaded", function() {
  applyScale();
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
  k.messaging.receiveMessage("rateStatus", onRateResult);
  k.messaging.receiveMessage("sourcesStatus", onSourcesResult);

  sendKFPM("-abi");

  renderAll();
  // Show the saved catalog immediately (works offline); fetch one if none exists yet.
  loadUpdateInfo(function() { renderAll(); }); // installed version + any pending update
  loadCatalog(function(parsed) {
    renderAll();
    if (shouldAutoSync()) startSync(); // first run, or the list has gone stale
  });
});
