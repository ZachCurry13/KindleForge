#!/usr/bin/env python3
"""Turn the ratings endpoint's /aggregate JSON into catalog/ratings.json.

usage: update_ratings.py <aggregate.json> <catalog/ratings.json>

- Validates the input strictly (the endpoint is the only untrusted input).
- Only rewrites the file when the ratings actually changed, so the repo does
  not collect a commit every few hours. Exit code 0 either way; prints
  "changed" or "unchanged".
"""
import json
import re
import sys
from datetime import datetime, timezone

PKG_RE = re.compile(r"^[A-Za-z0-9._-]{1,64}$")


def clean(agg):
    if not isinstance(agg, dict) or not isinstance(agg.get("packages"), dict):
        raise ValueError("not an aggregate document")
    min_votes = agg.get("minVotes", 3)
    if not isinstance(min_votes, int) or not 1 <= min_votes <= 1000:
        raise ValueError("bad minVotes")
    packages = {}
    for pkg, v in agg["packages"].items():
        if not PKG_RE.match(pkg):
            raise ValueError("bad package id %r" % pkg)
        if not isinstance(v, dict):
            raise ValueError("bad entry for %s" % pkg)
        avg, count = v.get("avg"), v.get("count")
        if isinstance(avg, bool) or not isinstance(avg, (int, float)) or not 1 <= avg <= 5:
            raise ValueError("bad average for %s" % pkg)
        if isinstance(count, bool) or not isinstance(count, int) or count < 1:
            raise ValueError("bad count for %s" % pkg)
        packages[pkg] = {"avg": round(float(avg), 1), "count": count}
    if len(packages) > 1000:
        raise ValueError("too many packages")
    return {"minVotes": min_votes, "packages": dict(sorted(packages.items()))}


def main(argv):
    if len(argv) != 3:
        print(__doc__)
        return 2
    with open(argv[1]) as f:
        new = clean(json.load(f))
    try:
        with open(argv[2]) as f:
            old = json.load(f)
    except (OSError, ValueError):
        old = {}
    if old.get("minVotes") == new["minVotes"] and old.get("packages") == new["packages"]:
        print("unchanged")
        return 0
    new = {"generatedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), **new}
    with open(argv[2], "w") as f:
        json.dump(new, f, indent=2, sort_keys=False)
        f.write("\n")
    print("changed")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
