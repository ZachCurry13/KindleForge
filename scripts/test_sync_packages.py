import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import sync_packages as sp

RAW = sp.RAW
API = sp.API


def fake_net(pages):
    def get(url, token=None):
        return pages.get(url)
    return get


def repo_json(desc):
    return json.dumps({"description": desc})


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.pages = {
            RAW + "/packages.list": "kpm\nblackjack\ncmsg\nhello\nlibz\nmusl\nkindleforge\nbrandnew\nblackjack\nbad id\n",
            # blackjack: upstream project named in install.kpm
            RAW + "/packages/blackjack/install.kpm": "curl https://github.com/Serialbocks/kindle-blackjack/releases/download/v1/x.zip",
            API + "/repos/Serialbocks/kindle-blackjack": repo_json("Blackjack for Kindle touch screens."),
            # cmsg: no URL, but a same-named repo by the KPM author
            RAW + "/packages/cmsg/install.kpm": "cp chat /bin/chat",
            API + "/repos/gingrspacecadet/cmsg": repo_json("cli messaging platform in C"),
            # hello: only the wiki knows
            RAW + "/packages/hello/install.kpm": "echo hi",
            RAW + "/wiki/pages/packages/hello.md": "# hello\n\n## Overview\n\nThe **hello** package is a [simple](x) example.\n\n## More\n",
            # brandnew: nothing known anywhere
            RAW + "/packages/brandnew/install.kpm": "echo x",
        }
        self.get = fake_net(self.pages)

    def run_sync(self, cfg):
        return sp.sync(cfg, self.get, None)

    def test_new_packages_are_described_from_upstream(self):
        cfg = {"packages": {}, "hidden": []}
        added, filled, hid = self.run_sync(cfg)
        p = cfg["packages"]
        self.assertEqual(p["blackjack"]["description"], "Blackjack for Kindle touch screens.")
        self.assertEqual(p["blackjack"]["author"], "Serialbocks")
        self.assertEqual(p["blackjack"]["category"], "games")
        self.assertEqual(p["cmsg"]["description"], "cli messaging platform in C")
        self.assertEqual(p["cmsg"]["author"], "gingrspacecadet")
        self.assertEqual(p["hello"]["description"], "The hello package is a simple example.")
        self.assertIn("brandnew", p)  # still listed, just without a description
        self.assertNotIn("description", p["brandnew"])
        self.assertEqual(set(added), {"blackjack", "cmsg", "hello", "brandnew"})

    def test_infrastructure_is_hidden_not_described(self):
        cfg = {"packages": {}, "hidden": []}
        _, _, hid = self.run_sync(cfg)
        for pkg in ("kpm", "libz", "musl", "kindleforge"):
            self.assertIn(pkg, cfg["hidden"])
            self.assertNotIn(pkg, cfg["packages"])
        self.assertEqual(set(hid), {"kpm", "libz", "musl", "kindleforge"})

    def test_human_written_fields_are_never_overwritten(self):
        cfg = {"packages": {"blackjack": {"name": "Blackjack!", "description": "My own words."}}, "hidden": []}
        self.run_sync(cfg)
        e = cfg["packages"]["blackjack"]
        self.assertEqual(e["name"], "Blackjack!")
        self.assertEqual(e["description"], "My own words.")
        self.assertEqual(e["author"], "Serialbocks")  # empty field was filled
        self.assertNotIn("description", e.get("auto", []))
        self.assertNotIn("name", e.get("auto", []))

    def test_auto_fields_refresh_but_stay_marked(self):
        cfg = {"packages": {}, "hidden": []}
        self.run_sync(cfg)
        self.pages[API + "/repos/gingrspacecadet/cmsg"] = repo_json("a chat tool for the terminal")
        self.run_sync(cfg)
        self.assertEqual(cfg["packages"]["cmsg"]["description"], "a chat tool for the terminal")
        self.assertEqual(sorted(cfg["packages"]["cmsg"]["auto"]), ["author", "category", "description", "name"])

    def test_second_run_changes_nothing(self):
        cfg = {"packages": {}, "hidden": []}
        self.run_sync(cfg)
        before = json.dumps(cfg, sort_keys=True)
        added, filled, hid = self.run_sync(cfg)
        self.assertEqual(json.dumps(cfg, sort_keys=True), before)
        self.assertEqual((added, filled, hid), ([], [], []))

    def test_upstream_unreachable_aborts_instead_of_wiping(self):
        cfg = {"packages": {"hello": {"name": "Hello"}}, "hidden": ["x"]}
        with self.assertRaises(RuntimeError):
            sp.sync(cfg, fake_net({}), None)
        self.assertEqual(cfg, {"packages": {"hello": {"name": "Hello"}}, "hidden": ["x"]})

    def test_unknown_extra_keys_in_config_survive(self):
        cfg = {"ratingsURL": "https://r.example", "repos": [1], "packages": {}, "hidden": []}
        self.run_sync(cfg)
        self.assertEqual(cfg["ratingsURL"], "https://r.example")
        self.assertEqual(cfg["repos"], [1])

    def test_tidy_and_category(self):
        self.assertEqual(sp.tidy("**Bold** `code` [link](http://x) https://y.z end"), "Bold code link end")
        self.assertEqual(sp.tidy(None), "")
        self.assertLessEqual(len(sp.tidy("word " * 100)), sp.MAX_DESC + 1)
        self.assertEqual(sp.guess_category("A Wordle game"), "games")
        self.assertEqual(sp.guess_category("music player"), "media")
        self.assertEqual(sp.guess_category("Anki flashcards"), "readers")
        self.assertEqual(sp.guess_category("does a thing"), "utilities")


if __name__ == "__main__":
    unittest.main()
