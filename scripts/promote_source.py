#!/usr/bin/env python3
"""Add a discovered repository to the directory in catalog/config.json (OFF by default).

usage: promote_source.py <id> [--discovered catalog/discovered.json] [--config catalog/config.json]
                          [--note "one honest line"] [--default]

Run this only after reading the repository's packages. The entry is added to
`sources` with trust "community": KindleForge shows it in Sources, switched OFF
until a user turns it on after a warning. `--default` (turn it on for everyone)
is for sources a maintainer has actually reviewed; those normally belong in
`repos` instead.
"""
import argparse
import json
import re
import sys

ID_RE = re.compile(r"^[A-Za-z0-9._-]{1,64}$")


class Problem(Exception):
    pass


def promote(args):
    disc = json.load(open(args.discovered))
    match = [r for r in disc.get("repositories", []) if r.get("id") == args.id]
    if not match:
        raise Problem("no discovered repository with id %r" % args.id)
    r = match[0]
    if not ID_RE.match(r["id"]) or not r["url"].startswith("https://") or r["format"] not in ("kpm", "zen"):
        raise Problem("that entry is not valid")
    cfg = json.load(open(args.config))
    sources = cfg.setdefault("sources", [])
    if any(s.get("id") == r["id"] or s.get("url") == r["url"] for s in sources) or any(s.get("url") == r["url"] for s in cfg.get("repos", [])):
        raise Problem("already in the directory")
    note = (args.note or "").strip() or "Found by the discovery script; not reviewed. Switch it on only if you trust it."
    if len(note) > 200:
        raise Problem("note is too long (200 characters)")
    entry = {"id": r["id"], "name": r["name"], "url": r["url"], "format": r["format"], "trust": "community", "note": note,
             "homepage": "https://github.com/" + r["repo"]}
    if args.default:
        entry["default"] = True
    sources.append(entry)
    cfg["sources"] = sorted(sources, key=lambda s: s["id"])
    json.dump(cfg, open(args.config, "w"), indent=2)
    open(args.config, "a").write("\n")
    return entry


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("id")
    ap.add_argument("--discovered", default="catalog/discovered.json")
    ap.add_argument("--config", default="catalog/config.json")
    ap.add_argument("--note")
    ap.add_argument("--default", action="store_true")
    try:
        e = promote(ap.parse_args())
        print("added %s (%s, %s) to the directory, OFF by default" % (e["id"], e["format"], e["url"]))
    except Problem as ex:
        print("REJECTED: %s" % ex, file=sys.stderr)
        sys.exit(1)
