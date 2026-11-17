#!/usr/bin/env python3
"""Keep catalog/config.json's package info in step with the upstream KPM repo.

For every package in gingrspacecadet/kpm's packages.list it works out a name,
author, description and category, using (best first):

  1. what is already in config.json and was written by a human (never touched)
  2. the upstream project's own GitHub description, found from the URL in the
     package's install.kpm (e.g. github.com/Serialbocks/kindle-blackjack), or
     a same-named repo owned by the KPM author (gingrspacecadet/cmsg)
  3. the package's page in the KPM wiki ("## Overview")

Fields this script fills are listed in the entry's "auto" list, so on the next
run it may refresh those, but it never overwrites a field a person wrote.
Infrastructure packages (lib*, compilers, ...) are added to "hidden".

usage: sync_packages.py [catalog/config.json]
Environment: GH_TOKEN or GITHUB_TOKEN (optional, raises the API rate limit).
Prints a short report; exit 0 whether or not anything changed.
"""
import json
import os
import re
import sys
import urllib.error
import urllib.request

UPSTREAM = "gingrspacecadet/kpm"
BRANCH = "main"
RAW = "https://raw.githubusercontent.com/%s/%s" % (UPSTREAM, BRANCH)
API = "https://api.github.com"
KPM_AUTHOR = "gingrspacecadet"

PKG_RE = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
INFRA = {"kpm", "musl", "apk", "make", "git", "curl", "gcc", "cc", "kindleforge", "template.kpm"}
INFRA_RE = re.compile(r"^lib")
MAX_DESC = 200


def http_get(url, token=None):
    req = urllib.request.Request(url, headers={"User-Agent": "kindleforge-sync"})
    if token and url.startswith(API):
        req.add_header("Authorization", "Bearer " + token)
        req.add_header("Accept", "application/vnd.github+json")
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.read().decode("utf-8", "replace")
    except (urllib.error.URLError, TimeoutError, OSError):
        return None


def tidy(text):
    """One plain sentence or two: no markdown, no links, bounded length."""
    if not text:
        return ""
    text = re.sub(r"`([^`]*)`", r"\1", text)
    text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"[*_#>]+", "", text)
    text = re.sub(r"https?://\S+", "", text)
    text = re.sub(r"\s+", " ", text).strip(" -:\t")
    if len(text) > MAX_DESC:
        cut = text[:MAX_DESC]
        stop = max(cut.rfind(". "), cut.rfind("! "))
        text = cut[: stop + 1] if stop > 60 else cut.rstrip() + "…"
    return text


def guess_category(text):
    t = (text or "").lower()
    rules = (
        ("games", ("game", "wordle", "blackjack", "emulator", "puzzle", "chess")),
        ("media", ("music", "audio", "video", "podcast", "radio", "player")),
        ("readers", ("reader", "flashcard", "anki", "ebook", "book", "dictionary", "novel")),
    )
    for cat, words in rules:
        if any(w in t for w in words):
            return cat
    return "utilities"


def pretty_name(pkg):
    return pkg if not pkg.islower() or len(pkg) <= 4 else pkg[:1].upper() + pkg[1:]


def upstream_repo(pkg, get, token):
    """(owner, repo) of the package's own project, if we can tell."""
    script = get("%s/packages/%s/install.kpm" % (RAW, pkg), token) or ""
    for owner, repo in re.findall(r"github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)", script):
        repo = re.sub(r"\.git$", "", repo)
        if "%s/%s" % (owner, repo) != UPSTREAM:
            return owner, repo
    # a repo of the same name owned by the KPM author (e.g. gingrspacecadet/cmsg)
    if get("%s/repos/%s/%s" % (API, KPM_AUTHOR, pkg), token):
        return KPM_AUTHOR, pkg
    return None


def derive(pkg, get, token):
    """Best-effort {name?, author?, description?, category?} for a package."""
    out = {}
    desc = ""
    repo = upstream_repo(pkg, get, token)
    if repo:
        raw = get("%s/repos/%s/%s" % (API, repo[0], repo[1]), token)
        try:
            info = json.loads(raw) if raw else {}
        except ValueError:
            info = {}
        desc = tidy(info.get("description") or "")
        out["author"] = repo[0]
    if not desc:
        page = get("%s/wiki/pages/packages/%s.md" % (RAW, pkg), token) or ""
        m = re.search(r"##\s*(?:Overview|Description)\s*\n+(.+?)(?:\n\s*\n|\Z)", page, re.S)
        if m:
            desc = tidy(m.group(1))
    if desc:
        out["description"] = desc
        out["category"] = guess_category(desc + " " + pkg)
    out["name"] = pretty_name(pkg)
    return out


def package_names(get, token):
    text = get("%s/packages.list" % RAW, token)
    if not text:
        return None
    seen, names = set(), []
    for line in text.splitlines():
        p = line.strip()
        if PKG_RE.match(p) and p not in seen:
            seen.add(p)
            names.append(p)
    return names


def sync(cfg, get=http_get, token=None):
    """Update cfg in place. Returns (added, filled, hidden_added) lists."""
    names = package_names(get, token)
    if names is None:
        raise RuntimeError("could not read the upstream package list")
    pkgs = cfg.setdefault("packages", {})
    hidden = cfg.setdefault("hidden", [])
    added, filled, hid = [], [], []

    for pkg in names:
        if (pkg in INFRA or INFRA_RE.match(pkg)) and pkg not in hidden:
            hidden.append(pkg)
            hid.append(pkg)
        if pkg in hidden:
            continue  # no need to describe what nobody sees

        entry = pkgs.get(pkg)
        if entry is None:
            entry, added_new = {}, True
        else:
            added_new = False
        auto = list(entry.get("auto", []))

        # which fields may the script write? empty ones, and ones it wrote before
        writable = [f for f in ("name", "author", "description", "category")
                    if not entry.get(f) or f in auto]
        # a bare name set by hand is not a "description"; still fill the rest
        if not writable:
            continue
        d = derive(pkg, get, token)
        changed = False
        for f in writable:
            v = d.get(f)
            if v and entry.get(f) != v:
                entry[f] = v
                if f not in auto:
                    auto.append(f)
                changed = True
        if auto:
            entry["auto"] = sorted(auto)
        if added_new and entry:
            pkgs[pkg] = entry
            added.append(pkg)
        elif changed:
            filled.append(pkg)

    cfg["hidden"] = hidden
    return added, filled, hid


def main(argv):
    path = argv[1] if len(argv) > 1 else "catalog/config.json"
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    with open(path) as f:
        cfg = json.load(f)
    before = json.dumps(cfg, sort_keys=True)
    added, filled, hid = sync(cfg, http_get, token)
    if json.dumps(cfg, sort_keys=True) == before:
        print("unchanged")
        return 0
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2)
        f.write("\n")
    print("changed: %d new, %d updated, %d newly hidden" % (len(added), len(filled), len(hid)))
    for label, items in (("new", added), ("updated", filled), ("hidden", hid)):
        if items:
            print("  %s: %s" % (label, ", ".join(items)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
