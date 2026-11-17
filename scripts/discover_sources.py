#!/usr/bin/env python3
"""Find package repositories and apps worth reviewing. Plain code, no AI.

usage: discover_sources.py [--out catalog/discovered.json] [--md DISCOVERED.md] [--config catalog/config.json]
Environment: GH_TOKEN or GITHUB_TOKEN (code search needs one).

What it does
  1. Searches GitHub for repositories that publish a package manifest in a
     format KindleForge can read (KindleModding/KPM "manifest_version", or
     ZenPM "schema_version"), fetches and validates each manifest.
  2. Reads Awesome-Kindle (CC0) and lists the apps in it, noting whether each
     publishes installable release files.
  3. Marks everything already in catalog/config.json as "known".
  4. Writes catalog/discovered.json and DISCOVERED.md for a human to read.

What it never does: enable, install or trust anything. A maintainer reads the
list and promotes entries with scripts/promote_source.py. Everything taken
from other people's pages is treated as untrusted text: it is cleaned and
shortened before being written anywhere.

Other places worth reading by hand (NOT scraped): KindleModShelf, the KindleModding
wiki, and the MIP Wiki, whose authors state that no AI is used on it; those
are linked from docs/RESOURCES.md for people.
"""
import argparse
import base64
import json
import os
import re
import sys
import urllib.parse
import urllib.request

AWESOME = "KindleTweaks/Awesome-Kindle"
SEARCHES = [
    ('"manifest_version" "supported_platforms" "artifacts" filename:manifest.json', "kpm"),
    ('"schema_version" "source_asset" "versions_url" filename:manifest.json', "zen"),
]
MAX_TEXT = 140
MAX_MANIFEST = 8 << 20
SLUG_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")


def http_get(url, token=None, raw=False):
    req = urllib.request.Request(url, headers={"User-Agent": "kindleforge-discover", "Accept": "application/vnd.github+json"})
    if token and "api.github.com" in url:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            data = r.read(MAX_MANIFEST + 1)
    except Exception:
        return None
    if len(data) > MAX_MANIFEST:
        return None
    text = data.decode("utf-8", "replace")
    if raw:
        return text
    try:
        return json.loads(text)
    except ValueError:
        return None


def clean(text, limit=MAX_TEXT):
    t = re.sub(r"<[^>]*>", "", str(text or ""))
    t = re.sub(r"[\x00-\x1f\x7f]+", " ", t)
    t = re.sub(r"[`*_~|\[\]()<>\\!#]", "", t)
    t = re.sub(r"https?://\S+", "", t)
    t = re.sub(r"\s+", " ", t).strip()
    return t[: limit - 1].rstrip() + "…" if len(t) > limit else t


# ---- repositories with a readable manifest --------------------------------------

def classify(manifest):
    """('kpm'|'zen'|None, package_count, sample_names) for a parsed manifest."""
    if not isinstance(manifest, dict):
        return None, 0, []
    pk = manifest.get("packages")
    if isinstance(pk, dict) and isinstance(manifest.get("manifest_version"), int) and manifest["manifest_version"] > 0:
        names = [clean(p.get("name") or k, 40) for k, p in list(pk.items())[:5] if isinstance(p, dict)]
        return "kpm", len(pk), names
    if isinstance(pk, list) and "schema_version" in manifest and isinstance(manifest.get("repo"), dict):
        names = [clean(p.get("name") or p.get("id"), 40) for p in pk[:5] if isinstance(p, dict)]
        return "zen", len(pk), names
    return None, 0, []


def examine_repo(full, path, get, token):
    if not SLUG_RE.match(full):
        return None
    info = get("https://api.github.com/repos/" + full, token)
    if not isinstance(info, dict) or info.get("archived"):
        return None
    branch = info.get("default_branch") or "main"
    raw_url = "https://raw.githubusercontent.com/%s/%s/%s" % (full, branch, urllib.parse.quote(path))
    text = get(raw_url, token, raw=True)
    try:
        manifest = json.loads(text) if text else None
    except ValueError:
        manifest = None
    fmt, count, sample = classify(manifest)
    if not fmt or count == 0:
        return None
    lic = (info.get("license") or {}).get("spdx_id") or "none"
    return {
        "id": re.sub(r"[^a-z0-9]+", "-", full.lower()).strip("-")[:48],
        "repo": full, "url": raw_url, "format": fmt,
        "name": clean(manifest.get("name") or (manifest.get("repo") or {}).get("name") or full, 60),
        "packages": count, "sample": sample, "license": clean(lic, 20),
        "stars": info.get("stargazers_count") if isinstance(info.get("stargazers_count"), int) else 0,
        "pushed": clean(info.get("pushed_at"), 10),
    }


def find_repositories(get, token):
    seen, out = set(), []
    for query, _fmt in SEARCHES:
        for page in (1, 2):
            res = get("https://api.github.com/search/code?q=%s&per_page=50&page=%d" % (urllib.parse.quote(query), page), token)
            items = (res or {}).get("items") if isinstance(res, dict) else None
            if not items:
                break
            for it in items:
                full = ((it.get("repository") or {}).get("full_name") or "")
                path = it.get("path") or ""
                key = (full, path)
                if key in seen or not full or not path or ".." in path:
                    continue
                seen.add(key)
                r = examine_repo(full, path, get, token)
                if r:
                    out.append(r)
    out.sort(key=lambda r: (-r["packages"], r["repo"]))
    # one entry per repository: the manifest with the most packages wins
    uniq, repos_seen = [], set()
    for r in out:  # already sorted by package count, descending
        if r["repo"] not in repos_seen:
            repos_seen.add(r["repo"])
            uniq.append(r)
    return uniq


# ---- apps from Awesome-Kindle ----------------------------------------------------

LINK_RE = re.compile(r"^\s*[-*]\s+\[([^\]]+)\]\((https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+))/?\)\s*(?:[-–—:]\s*(.*))?$")


def parse_awesome(readme):
    apps, section = [], ""
    for line in readme.splitlines():
        if line.startswith("### "):
            section = clean(line[4:], 40)
            continue
        m = LINK_RE.match(line)
        if m and section:
            apps.append({"repo": m.group(3), "name": clean(m.group(1), 60), "description": clean(m.group(4) or "", MAX_TEXT), "section": section})
    return apps


def releases_signal(repo, get, token):
    rel = get("https://api.github.com/repos/%s/releases/latest" % repo, token)
    if not isinstance(rel, dict):
        return {"release": None, "kpkg": False, "zip": False}
    names = [str(a.get("name", "")).lower() for a in rel.get("assets", []) if isinstance(a, dict)]
    return {"release": clean(rel.get("tag_name"), 30), "kpkg": any(n.endswith(".kpkg") for n in names), "zip": any(n.endswith(".zip") for n in names)}


def find_apps(get, token):
    info = get("https://api.github.com/repos/%s/readme" % AWESOME, token)
    try:
        readme = base64.b64decode((info or {}).get("content", "")).decode("utf-8", "replace")
    except Exception:
        return []
    apps = parse_awesome(readme)
    for a in apps:
        a.update(releases_signal(a["repo"], get, token))
    return apps


# ---- output ------------------------------------------------------------------------

def known_urls(config):
    urls = set()
    for r in (config.get("repos") or []) + (config.get("sources") or []):
        if isinstance(r, dict) and r.get("url"):
            urls.add(r["url"])
    return urls


def slug_of(url):
    """owner/repo behind a raw.githubusercontent.com or <owner>.github.io/<repo> address."""
    m = re.match(r"^https://raw\.githubusercontent\.com/([^/]+)/([^/]+)/", url or "")
    if m:
        return (m.group(1) + "/" + m.group(2)).lower()
    m = re.match(r"^https://([^./]+)\.github\.io/([^/]+)/", url or "")
    if m:
        return (m.group(1) + "/" + m.group(2)).lower()
    return None


def known_slugs(config):
    return {s for s in (slug_of(u) for u in known_urls(config)) if s}


def build(repos, apps, config):
    known, slugs = known_urls(config), known_slugs(config)
    for r in repos:
        r["known"] = r["url"] in known or r["repo"].lower() in slugs
    return {"repositories": repos, "apps": apps}


def render_md(data):
    out = ["# Discovered sources", "",
           "_Generated by `scripts/discover_sources.py` (plain code, no AI). Nothing here is enabled or trusted: a person reads it and promotes entries with `scripts/promote_source.py`._", "",
           "## Repositories KindleForge can read", ""]
    repos = data["repositories"]
    if repos:
        out += ["| Repository | Format | Packages | License | Stars | Status |", "|---|---|---:|---|---:|---|"]
        for r in repos:
            out.append("| [%s](https://github.com/%s) | %s | %d | %s | %d | %s |" % (
                r["name"], r["repo"], r["format"], r["packages"], r["license"], r["stars"], "in the directory" if r["known"] else "not reviewed yet"))
    else:
        out.append("None found.")
    out += ["", "## Apps listed in Awesome-Kindle (CC0)", ""]
    apps = data["apps"]
    if apps:
        out += ["| App | Section | Installable release files |", "|---|---|---|"]
        for a in apps:
            sig = ", ".join(x for x, ok in (("kpkg", a.get("kpkg")), ("zip", a.get("zip"))) if ok) or ("release only" if a.get("release") else "none")
            out.append("| [%s](https://github.com/%s) | %s | %s |" % (a["name"], a["repo"], a["section"], sig))
    else:
        out.append("None found.")
    return "\n".join(out) + "\n"


def write_if_changed(path, text):
    try:
        old = open(path).read()
    except OSError:
        old = None
    if old == text:
        return False
    with open(path, "w") as f:
        f.write(text)
    return True


def main(argv, get=http_get):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--out", default="catalog/discovered.json")
    ap.add_argument("--md", default="DISCOVERED.md")
    ap.add_argument("--config", default="catalog/config.json")
    args = ap.parse_args(argv[1:])
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    try:
        config = json.load(open(args.config))
    except (OSError, ValueError):
        config = {}
    repos = find_repositories(get, token)
    apps = find_apps(get, token)
    if not repos and not apps:
        print("nothing found (rate limit or no token?): leaving the files as they are")
        return 1
    data = build(repos, apps, config)
    changed = write_if_changed(args.out, json.dumps(data, indent=2, sort_keys=True) + "\n")
    changed = write_if_changed(args.md, render_md(data)) or changed
    print("changed" if changed else "unchanged")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
