import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import pull_suggestions as ps

REPO = "me/kf"


def issue(n, title, state="open", thumbs=0, labels=("app-suggestion",), **extra):
    d = {"number": n, "title": title, "state": state, "reactions": {"+1": thumbs},
         "labels": [{"name": l} for l in labels], "html_url": "https://evil.example/x", "body": "SECRET BODY"}
    d.update(extra)
    return d


class SuggestionTests(unittest.TestCase):
    def test_sorted_by_thumbs_then_number(self):
        md = ps.render(REPO, [issue(1, "[App] Old", thumbs=1), issue(2, "[App] Popular", thumbs=9), issue(3, "[App] Newer", thumbs=1)])
        order = [md.index(x) for x in ("Popular", "Old", "Newer")]
        self.assertEqual(order, sorted(order))
        self.assertIn("| 9 | [Popular](https://github.com/me/kf/issues/2) | Open |", md)

    def test_added_and_accepted_sections(self):
        md = ps.render(REPO, [issue(4, "[App] Done", state="closed", labels=("app-suggestion", "added")),
                              issue(5, "[App] Nope", state="closed"),
                              issue(6, "[App] Soon", labels=("app-suggestion", "accepted")),
                              issue(7, "[App] Wait", labels=("app-suggestion", "needs-info"))])
        added = md.split("## Added to the store")[1]
        self.assertIn("[Done](https://github.com/me/kf/issues/4)", added)
        self.assertNotIn("Nope", md)
        self.assertIn("Accepted: coming", md)
        self.assertIn("Needs info", md)

    def test_untrusted_text_is_neutralised(self):
        evil = "[App] <script>alert(1)</script> | x](http://evil.example) ![i](http://e/p.png) `rm -rf` **bold**\nnew line"
        md = ps.render(REPO, [issue(8, evil)])
        for bad in ("<script", "evil.example", "http://e/", "`", "**", "\n new", "](http"):
            self.assertNotIn(bad, md.split("## Requested")[1].split("## Added")[0].replace("](https://github.com/me/kf/issues/8)", ""))
        self.assertNotIn("SECRET BODY", md)
        self.assertNotIn("evil.example", md)  # the API's html_url is never used
        self.assertIn("https://github.com/me/kf/issues/8", md)

    def test_long_and_empty_titles_and_pull_requests(self):
        md = ps.render(REPO, [issue(9, "[App] " + "x" * 500), issue(10, "[App]   "), issue(11, "A PR", pull_request={})])
        self.assertIn("…", md)
        self.assertIn("(untitled)", md)
        self.assertNotIn("A PR", md)
        self.assertLessEqual(max(len(l) for l in md.splitlines() if "issues/9" in l), 200)

    def test_bad_reaction_values_and_empty_list(self):
        md = ps.render(REPO, [issue(12, "[App] Odd", reactions={"+1": "lots"}), issue(13, "[App] Neg", reactions={"+1": -5})])
        self.assertEqual(md.count("| 0 |"), 2)
        self.assertIn("Nothing requested yet.", ps.render(REPO, []))

    def test_main_writes_only_when_changed_and_validates_repo(self):
        d = tempfile.mkdtemp()
        out = os.path.join(d, "S.md")
        get = lambda url, token=None: [issue(1, "[App] One")]
        self.assertEqual(ps.main(["x", REPO, out], get=get), 0)
        first = open(out).read()
        os.utime(out, (1, 1))
        self.assertEqual(ps.main(["x", REPO, out], get=get), 0)
        self.assertEqual(os.stat(out).st_mtime, 1)  # untouched
        self.assertEqual(ps.main(["x", "not a repo", out], get=get), 2)
        self.assertIn("One", first)

    def test_pagination_stops_when_a_page_is_short(self):
        calls = []
        def get(url, token=None):
            calls.append(url)
            return [issue(i, "[App] t%d" % i) for i in range(100)] if url.endswith("&page=1") else [issue(500, "[App] last")]
        got = ps.fetch_issues(REPO, None, get)
        self.assertEqual(len(got), 101)
        self.assertEqual(len(calls), 2)


if __name__ == "__main__":
    unittest.main()
