import base64
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import discover_sources as ds
import promote_source as ps

README = """# Awesome Kindle
### Quality of Life
- [KindleFetch](https://github.com/justrals/KindleFetch) - Download books
- [Evil <b>name</b> `x`](https://github.com/bad/repo) - `rm -rf` **now** <script>alert(1)</script> [y](http://evil.example) https://evil.example/x
- [Nested [brackets]](https://github.com/skipped/nested) - odd lines like this are skipped, not guessed at
- [Not github](https://example.org/x) - ignored
### Games
- [Chess](https://github.com/jckhng/exact-chess/) - A game
"""


def fake(manifests, archived=()):
    routes = {}
    items_kpm = [{"repository": {"full_name": "a/kpmrepo"}, "path": "manifest.json"},
                 {"repository": {"full_name": "z/archived"}, "path": "manifest.json"},
                 {"repository": {"full_name": "n/notmanifest"}, "path": "manifest.json"},
                 {"repository": {"full_name": "t/traversal"}, "path": "../etc/manifest.json"},
                 {"repository": {"full_name": "bad slug!"}, "path": "manifest.json"}]
    items_zen = [{"repository": {"full_name": "b/zenrepo"}, "path": "manifest.json"},
                 {"repository": {"full_name": "a/kpmrepo"}, "path": "manifest.json"}]  # duplicate of a kpm hit

    def get(url, token=None, raw=False):
        if "search/code" in url:
            if "page=2" in url:
                return {"items": []}
            return {"items": items_kpm if "manifest_version" in url else items_zen}
        if url.endswith("/readme"):
            return {"content": base64.b64encode(README.encode()).decode()}
        if "/releases/latest" in url:
            repo = url.split("/repos/")[1].split("/releases")[0]
            return {"tag_name": "v1", "assets": [{"name": "x.kpkg"}, {"name": "y.zip"}]} if repo == "justrals/KindleFetch" else None
        if url.startswith("https://api.github.com/repos/"):
            full = "/".join(url.split("/repos/")[1].split("/")[:2])
            return {"default_branch": "main", "license": {"spdx_id": "MIT"}, "stargazers_count": 5, "pushed_at": "2026-01-02T00:00:00Z", "archived": full in archived}
        if url.startswith("https://raw.githubusercontent.com/"):
            parts = url.split("/")
            key = parts[3] + "/" + parts[4]
            return manifests.get(key) if raw else None
        return None
    return get


KPM = json.dumps({"manifest_version": 2, "id": "k", "name": "Nice <b>KPM</b> repo", "packages": {"a": {"name": "Alpha"}, "b": {"name": "Beta"}}})
ZEN = json.dumps({"schema_version": "1", "repo": {"id": "z", "name": "Zen"}, "packages": [{"id": "p", "name": "Plug"}]})
MANIFESTS = {"a/kpmrepo": KPM, "b/zenrepo": ZEN, "z/archived": KPM, "n/notmanifest": json.dumps({"hello": "world"})}


class DiscoverTests(unittest.TestCase):
    def test_classify(self):
        self.assertEqual(ds.classify(json.loads(KPM))[:2], ("kpm", 2))
        self.assertEqual(ds.classify(json.loads(ZEN))[:2], ("zen", 1))
        for bad in (None, [], {"packages": []}, {"manifest_version": 2, "packages": []}, {"manifest_version": "2", "packages": {"a": {}}}, "x"):
            self.assertEqual(ds.classify(bad)[0], None)

    def test_finds_only_real_unarchived_repositories_once(self):
        repos = ds.find_repositories(fake(MANIFESTS, archived=("z/archived",)), "tok")
        self.assertEqual(sorted(r["repo"] for r in repos), ["a/kpmrepo", "b/zenrepo"])
        k = [r for r in repos if r["repo"] == "a/kpmrepo"][0]
        self.assertEqual((k["format"], k["packages"], k["license"], k["stars"]), ("kpm", 2, "MIT", 5))
        self.assertEqual(k["url"], "https://raw.githubusercontent.com/a/kpmrepo/main/manifest.json")
        self.assertNotIn("<", k["name"])  # third-party text is cleaned

    def test_one_entry_per_repository(self):
        two = {"a/kpmrepo": KPM, "b/zenrepo": ZEN}
        base = fake(two)
        def get(url, token=None, raw=False):
            if "search/code" in url and "page=2" not in url:
                res = base(url, token)
                if res and res.get("items"):
                    res = {"items": res["items"] + [{"repository": {"full_name": "a/kpmrepo"}, "path": "other/manifest.json"}]}
                return res
            if url.endswith("/other/manifest.json"):
                return json.dumps({"manifest_version": 2, "id": "o", "packages": {"x": {"name": "X"}}}) if raw else None
            return base(url, token, raw)
        repos = ds.find_repositories(get, "tok")
        self.assertEqual(sorted(r["repo"] for r in repos), ["a/kpmrepo", "b/zenrepo"])
        self.assertEqual([r for r in repos if r["repo"] == "a/kpmrepo"][0]["packages"], 2)

    def test_awesome_parsing_and_untrusted_text(self):
        apps = ds.parse_awesome(README)
        self.assertEqual([a["repo"] for a in apps], ["justrals/KindleFetch", "bad/repo", "jckhng/exact-chess"])
        bad = apps[1]
        for field in ("name", "description"):
            for evil in ("<script", "evil.example", "`", "**", "](", "http"):
                self.assertNotIn(evil, bad[field])
        self.assertEqual(apps[2]["section"], "Games")

    def test_release_signals(self):
        apps = ds.find_apps(fake(MANIFESTS), "tok")
        fetch = apps[0]
        self.assertTrue(fetch["kpkg"] and fetch["zip"] and fetch["release"] == "v1")
        self.assertFalse(apps[1]["kpkg"])

    def test_known_flag_and_markdown(self):
        repos = ds.find_repositories(fake(MANIFESTS, archived=("z/archived",)), "tok")
        cfg = {"repos": [{"id": "x", "url": "https://raw.githubusercontent.com/a/kpmrepo/main/manifest.json"}]}
        data = ds.build(repos, ds.find_apps(fake(MANIFESTS), "tok"), cfg)
        known = {r["repo"]: r["known"] for r in data["repositories"]}
        self.assertEqual(known, {"a/kpmrepo": True, "b/zenrepo": False})
        # a repo served through GitHub Pages is recognised too
        pages = ds.build([{"repo": "KindleTweaks/Repo", "url": "https://raw.githubusercontent.com/KindleTweaks/Repo/main/manifest.json"}], [],
                         {"repos": [{"id": "kt", "url": "https://kindletweaks.github.io/Repo/manifest.json"}]})
        self.assertTrue(pages["repositories"][0]["known"])
        self.assertFalse(ds.build([{"repo": "other/Repo", "url": "https://x/y"}], [], {"repos": [{"url": "https://kindletweaks.github.io/Repo/manifest.json"}]})["repositories"][0]["known"])
        md = ds.render_md(data)
        self.assertIn("in the directory", md)
        self.assertIn("not reviewed yet", md)
        self.assertIn("Awesome-Kindle (CC0)", md)
        self.assertIn("kpkg, zip", md)
        self.assertNotIn("<script", md)

    def test_main_writes_once_and_never_on_empty_results(self):
        d = tempfile.mkdtemp()
        out, md, cfg = (os.path.join(d, n) for n in ("d.json", "D.md", "c.json"))
        json.dump({"repos": []}, open(cfg, "w"))
        args = ["x", "--out", out, "--md", md, "--config", cfg]
        self.assertEqual(ds.main(args, get=fake(MANIFESTS)), 0)
        first = open(out).read()
        os.utime(out, (1, 1))
        self.assertEqual(ds.main(args, get=fake(MANIFESTS)), 0)
        self.assertEqual(os.stat(out).st_mtime, 1)  # unchanged: not rewritten
        self.assertEqual(open(out).read(), first)
        # nothing found (rate limited): keep the old files and signal failure
        self.assertEqual(ds.main(args, get=lambda *a, **k: None), 1)
        self.assertEqual(open(out).read(), first)


class PromoteTests(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.mkdtemp()
        self.disc = os.path.join(self.d, "d.json")
        self.cfg = os.path.join(self.d, "c.json")
        repos = ds.find_repositories(fake(MANIFESTS), "tok")
        json.dump({"repositories": repos, "apps": []}, open(self.disc, "w"))
        json.dump({"repos": [], "ratingsURL": "https://r.example", "categories": {"a": "games"}}, open(self.cfg, "w"))

    def args(self, **kw):
        from types import SimpleNamespace
        d = dict(id="a-kpmrepo", discovered=self.disc, config=self.cfg, note=None, default=False)
        d.update(kw)
        return SimpleNamespace(**d)

    def test_promote_adds_an_OFF_community_entry_and_keeps_the_rest(self):
        e = ps.promote(self.args())
        cfg = json.load(open(self.cfg))
        self.assertEqual(cfg["sources"][0]["trust"], "community")
        self.assertNotIn("default", cfg["sources"][0])
        self.assertEqual(cfg["ratingsURL"], "https://r.example")
        self.assertEqual(cfg["categories"], {"a": "games"})
        self.assertTrue(e["url"].startswith("https://"))
        self.assertIn("not reviewed", e["note"])

    def test_rejections(self):
        ps.promote(self.args())
        for kw in ({}, {"id": "nope"}, {"id": "b-zenrepo", "note": "x" * 300}):
            with self.assertRaises(ps.Problem, msg=str(kw)):
                ps.promote(self.args(**kw))

    def test_default_flag_is_explicit(self):
        ps.promote(self.args(id="b-zenrepo", default=True, note="Reviewed by me."))
        entry = json.load(open(self.cfg))["sources"][0]
        self.assertTrue(entry["default"])
        self.assertEqual(entry["format"], "zen")


if __name__ == "__main__":
    unittest.main()
