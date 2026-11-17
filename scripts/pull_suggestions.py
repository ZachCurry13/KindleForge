#!/usr/bin/env python3
"""Turn GitHub issues labelled "app-suggestion" into SUGGESTIONS.md.

usage: pull_suggestions.py <owner/repo> [SUGGESTIONS.md]
Environment: GH_TOKEN or GITHUB_TOKEN (optional, raises the API rate limit).

Issue text is written by strangers, so it is treated as untrusted: only the
issue NUMBER (a plain integer), a sanitised, shortened TITLE and the label and
reaction counts are used. Bodies and comments are never read. The page links
are built from the repo name and the number, never taken from the API.
"""
import json
import os
import re
import sys
import urllib.request

LABEL = "app-suggestion"
MAX_TITLE = 80


def api_get(url, token=None):
    req = urllib.request.Request(url, headers={"User-Agent": "kindleforge-suggestions", "Accept": "application/vnd.github+json"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode("utf-8", "replace"))


def fetch_issues(repo, token=None, get=api_get):
    issues = []
    for page in range(1, 6):  # up to 500 suggestions
        batch = get("https://api.github.com/repos/%s/issues?labels=%s&state=all&per_page=100&page=%d" % (repo, LABEL, page), token)
        if not isinstance(batch, list):
            raise RuntimeError("unexpected API answer")
        issues += batch
        if len(batch) < 100:
            break
    return issues


def clean_title(title):
    t = re.sub(r"^\s*\[app\]\s*", "", str(title or ""), flags=re.I)
    t = re.sub(r"[\r\n\t]+", " ", t)
    t = re.sub(r"<[^>]*>", "", t)            # no HTML
    t = re.sub(r"[`*_~|\[\]()<>\\!#]", "", t)  # nothing that markdown would act on
    t = re.sub(r"https?://\S+", "", t)
    t = re.sub(r"\s+", " ", t).strip()
    if len(t) > MAX_TITLE:
        t = t[: MAX_TITLE - 1].rstrip() + "…"
    return t or "(untitled)"


def label_names(issue):
    return {str(l.get("name", "")).lower() for l in issue.get("labels", []) if isinstance(l, dict)}


def thumbs(issue):
    r = issue.get("reactions")
    n = r.get("+1", 0) if isinstance(r, dict) else 0
    return n if isinstance(n, int) and n >= 0 else 0


def render(repo, issues):
    rows, added = [], []
    for i in issues:
        if "pull_request" in i or not isinstance(i.get("number"), int):
            continue
        labels = label_names(i)
        entry = (i["number"], clean_title(i.get("title")), thumbs(i), labels)
        if i.get("state") == "closed":
            if "added" in labels:
                added.append(entry)
        else:
            rows.append(entry)
    rows.sort(key=lambda e: (-e[2], e[0]))
    added.sort(key=lambda e: -e[0])

    def link(n):
        return "https://github.com/%s/issues/%d" % (repo, n)

    def status(labels):
        if "accepted" in labels:
            return "Accepted: coming"
        if "needs-info" in labels:
            return "Needs info"
        return "Open"

    out = ["# App suggestions", "",
           "_Generated automatically from GitHub issues; do not edit by hand._", "",
           "Want an app in the store? [Suggest it](https://github.com/%s/issues/new?template=suggest-app.yml). "
           "Add a 👍 to suggestions you also want: the most-wanted are reviewed first." % repo, "",
           "## Requested", ""]
    if rows:
        out += ["| 👍 | Suggestion | Status |", "|---:|---|---|"]
        out += ["| %d | [%s](%s) | %s |" % (t, title, link(n), status(l)) for n, title, t, l in rows]
    else:
        out.append("Nothing requested yet.")
    out += ["", "## Added to the store", ""]
    if added:
        out += ["- [%s](%s)" % (title, link(n)) for n, title, _, _ in added]
    else:
        out.append("Nothing yet.")
    return "\n".join(out) + "\n"


def main(argv, get=api_get):
    if len(argv) < 2 or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", argv[1]):
        print(__doc__)
        return 2
    repo, path = argv[1], (argv[2] if len(argv) > 2 else "SUGGESTIONS.md")
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    text = render(repo, fetch_issues(repo, token, get))
    try:
        old = open(path).read()
    except OSError:
        old = None
    if old == text:
        print("unchanged")
        return 0
    with open(path, "w") as f:
        f.write(text)
    print("changed")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
