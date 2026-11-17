#!/usr/bin/env python3
"""Add (or update) an app in catalog/community/manifest.json, after inspecting it.

A maintainer runs this once an app suggestion has been accepted. It

  1. downloads the package (https only, size-capped),
  2. checks it WITHOUT running anything: a gzipped tar with a manifest.json
     whose id, version and platforms match what you typed, no unsafe paths,
     no links or devices,
  3. prints the install.sh / uninstall.sh for you to READ (they run as root on
     people's Kindles: read them),
  4. records the package with its sha256, so the download is pinned: if the
     author later swaps the file, Kindles refuse it.

usage:
  scripts/add_app.py --id foo --name "Foo" --author "Someone" \\
      --description "One sentence on what it does." --version 1.2.3 \\
      --platforms kindlehf,kindlepw2 \\
      --url https://github.com/someone/foo/releases/download/v1.2.3/foo_1.2.3.kpkg \\
      [--category games] [--manifest catalog/community/manifest.json] \\
      [--config catalog/config.json] [--dry-run]
"""
import argparse
import gzip
import hashlib
import io
import json
import re
import sys
import tarfile
import urllib.request

ID_RE = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
PLATFORMS = {"kindle", "kindle5", "kindlepw2", "kindlehf"}
CATEGORIES = {"readers", "games", "media", "utilities"}
MAX_DOWNLOAD = 64 << 20
MAX_EXPANDED = 160 << 20
MAX_FILES = 2000
MAX_DESC = 200
SHOW_LINES = 80


class Problem(Exception):
    pass


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "kindleforge-add-app"})
    with urllib.request.urlopen(req, timeout=120) as r:
        data = r.read(MAX_DOWNLOAD + 1)
    if len(data) > MAX_DOWNLOAD:
        raise Problem("download is larger than %d MB" % (MAX_DOWNLOAD >> 20))
    return data


def parse_version(s):
    m = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", s or "")
    if not m:
        raise Problem("version must look like 1.2.3")
    return [int(x) for x in m.groups()]


def inspect_package(data, pkg_id, version, platforms):
    """Validate the archive structure. Returns {hooks: {name: text}, files: n}."""
    try:
        tar = tarfile.open(fileobj=io.BytesIO(gzip.decompress(data)))
    except (OSError, EOFError, tarfile.TarError) as e:
        raise Problem("not a gzipped tar package: %s" % e)
    total, files, hooks, manifest = 0, 0, {}, None
    for m in tar.getmembers():
        name = m.name[2:] if m.name.startswith("./") else m.name
        if not name or name == ".":
            continue
        if name.startswith("/") or "\\" in name or ".." in name.split("/"):
            raise Problem("unsafe path in package: %r" % m.name)
        if not (m.isfile() or m.isdir()):
            raise Problem("links and devices are not allowed: %r" % m.name)
        files += 1
        total += m.size
        if files > MAX_FILES:
            raise Problem("package has too many files")
        if total > MAX_EXPANDED:
            raise Problem("package expands to more than %d MB" % (MAX_EXPANDED >> 20))
        if m.isfile() and name in ("manifest.json", "install.sh", "uninstall.sh", "launch.sh"):
            text = tar.extractfile(m).read().decode("utf-8", "replace")
            if name == "manifest.json":
                manifest = text
            else:
                hooks[name] = text
    if files == 0:
        raise Problem("package is empty")
    if manifest is None:
        raise Problem("package has no manifest.json")
    try:
        mf = json.loads(manifest)
    except ValueError:
        raise Problem("package manifest.json is not valid JSON")
    if mf.get("id") != pkg_id:
        raise Problem("package says its id is %r, not %r" % (mf.get("id"), pkg_id))
    if mf.get("version") != version:
        raise Problem("package version is %r, not %r" % (mf.get("version"), version))
    missing = [p for p in platforms if p not in (mf.get("supported_platforms") or [])]
    if missing:
        raise Problem("package does not list platform(s): %s" % ", ".join(missing))
    if mf.get("dependencies"):
        raise Problem("packages with dependencies are not supported yet")
    return {"hooks": hooks, "files": files}


def build_entry(args, version, sha):
    return {
        "url": args.url,
        "version": version,
        "dependencies": [],
        "supported_platforms": sorted(args.platforms),
        "sha256": sha,
    }


def update_manifest(manifest, args, version, sha):
    pkgs = manifest.setdefault("packages", {})
    entry = pkgs.setdefault(args.id, {})
    entry.update({"name": args.name, "author": args.author, "description": args.description})
    arts = [a for a in entry.get("artifacts", []) if sorted(a.get("supported_platforms", [])) != sorted(args.platforms)]
    arts.append(build_entry(args, version, sha))
    entry["artifacts"] = arts
    manifest["packages"] = dict(sorted(pkgs.items()))
    return manifest


def check_args(args):
    if not ID_RE.match(args.id):
        raise Problem("id must be letters, digits, . _ - (max 64)")
    if not args.url.startswith("https://"):
        raise Problem("the package url must be https")
    if not args.name.strip() or len(args.name) > 60:
        raise Problem("name must be 1-60 characters")
    if not args.author.strip() or len(args.author) > 60:
        raise Problem("author must be 1-60 characters")
    if not args.description.strip() or len(args.description) > MAX_DESC:
        raise Problem("description must be 1-%d characters" % MAX_DESC)
    bad = [p for p in args.platforms if p not in PLATFORMS]
    if bad or not args.platforms:
        raise Problem("platforms must be from: %s" % ", ".join(sorted(PLATFORMS)))
    if args.category and args.category not in CATEGORIES:
        raise Problem("category must be one of: %s" % ", ".join(sorted(CATEGORIES)))


def run(args, fetch_fn=None, out=print):
    fetch_fn = fetch_fn or fetch
    check_args(args)
    version = parse_version(args.version)
    data = fetch_fn(args.url)
    info = inspect_package(data, args.id, version, args.platforms)
    sha = hashlib.sha256(data).hexdigest()

    out("package ok: %d entries, %d bytes, sha256 %s" % (info["files"], len(data), sha))
    for name in ("install.sh", "uninstall.sh", "launch.sh"):
        if name in info["hooks"]:
            lines = info["hooks"][name].splitlines()
            out("\n===== %s (READ THIS: it runs as root on Kindles) =====" % name)
            out("\n".join(lines[:SHOW_LINES]))
            if len(lines) > SHOW_LINES:
                out("... (%d more lines: read the whole file before merging)" % (len(lines) - SHOW_LINES))
    if "install.sh" not in info["hooks"]:
        out("\nnote: no install.sh (nothing is run on install)")

    with open(args.manifest) as f:
        manifest = json.load(f)
    manifest = update_manifest(manifest, args, version, sha)
    cfg = None
    if args.category and args.config:
        with open(args.config) as f:
            cfg = json.load(f)
        cfg.setdefault("categories", {})[args.id] = args.category

    if args.dry_run:
        out("\n(dry run: nothing written)")
        return manifest, cfg
    with open(args.manifest, "w") as f:
        json.dump(manifest, f, indent=2)
        f.write("\n")
    if cfg is not None:
        with open(args.config, "w") as f:
            json.dump(cfg, f, indent=2)
            f.write("\n")
    out("\nwrote %s" % args.manifest)
    return manifest, cfg


def parse_args(argv):
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--id", required=True)
    p.add_argument("--name", required=True)
    p.add_argument("--author", required=True)
    p.add_argument("--description", required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--platforms", required=True, type=lambda s: [x.strip() for x in s.split(",") if x.strip()])
    p.add_argument("--url", required=True)
    p.add_argument("--category")
    p.add_argument("--manifest", default="catalog/community/manifest.json")
    p.add_argument("--config", default="catalog/config.json")
    p.add_argument("--dry-run", action="store_true")
    return p.parse_args(argv)


if __name__ == "__main__":
    try:
        run(parse_args(sys.argv[1:]))
    except Problem as e:
        print("REJECTED: %s" % e, file=sys.stderr)
        sys.exit(1)
