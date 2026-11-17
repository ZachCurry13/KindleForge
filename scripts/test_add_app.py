import gzip
import hashlib
import io
import json
import os
import sys
import tarfile
import tempfile
import unittest
from types import SimpleNamespace

sys.path.insert(0, os.path.dirname(__file__))
import add_app as aa


def make_pkg(entries):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w") as t:
        for name, body, kind in entries:
            ti = tarfile.TarInfo(name)
            if kind == "file":
                data = body.encode()
                ti.size = len(data)
                t.addfile(ti, io.BytesIO(data))
            elif kind == "dir":
                ti.type = tarfile.DIRTYPE
                t.addfile(ti)
            elif kind == "symlink":
                ti.type = tarfile.SYMTYPE
                ti.linkname = body
                t.addfile(ti)
    return gzip.compress(buf.getvalue())


def good(version=(1, 2, 3), platforms=("kindlehf",), extra=()):
    mf = json.dumps({"id": "foo", "version": list(version), "supported_platforms": list(platforms)})
    return make_pkg([("manifest.json", mf, "file"), ("install.sh", "#!/bin/sh\necho hi\n", "file"),
                     ("uninstall.sh", "#!/bin/sh\necho bye\n", "file"), ("payload", "", "dir")] + list(extra))


def args(tmp, **kw):
    manifest = os.path.join(tmp, "manifest.json")
    config = os.path.join(tmp, "config.json")
    if not os.path.exists(manifest):
        json.dump({"manifest_version": 2, "id": "c", "packages": {}}, open(manifest, "w"))
        json.dump({"categories": {}}, open(config, "w"))
    d = dict(id="foo", name="Foo", author="Someone", description="Does a thing.", version="1.2.3",
             platforms=["kindlehf"], url="https://example.org/foo.kpkg", category="games",
             manifest=manifest, config=config, dry_run=False)
    d.update(kw)
    return SimpleNamespace(**d)


class AddAppTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.lines = []

    def run_add(self, data, **kw):
        return aa.run(args(self.tmp, **kw), fetch_fn=lambda url: data, out=self.lines.append)

    def test_adds_pinned_entry_and_shows_the_hooks(self):
        data = good()
        manifest, cfg = self.run_add(data)
        art = manifest["packages"]["foo"]["artifacts"][0]
        self.assertEqual(art["sha256"], hashlib.sha256(data).hexdigest())
        self.assertEqual(art["version"], [1, 2, 3])
        self.assertEqual(art["supported_platforms"], ["kindlehf"])
        self.assertEqual(json.load(open(os.path.join(self.tmp, "manifest.json")))["packages"]["foo"]["name"], "Foo")
        self.assertEqual(json.load(open(os.path.join(self.tmp, "config.json")))["categories"]["foo"], "games")
        shown = "\n".join(self.lines)
        self.assertIn("READ THIS", shown)
        self.assertIn("echo hi", shown)

    def test_dry_run_writes_nothing(self):
        self.run_add(good(), dry_run=True)
        self.assertEqual(json.load(open(os.path.join(self.tmp, "manifest.json")))["packages"], {})

    def test_second_platform_is_added_and_same_platform_replaced(self):
        self.run_add(good())
        self.run_add(good(platforms=("kindlepw2",)), platforms=["kindlepw2"])
        arts = json.load(open(os.path.join(self.tmp, "manifest.json")))["packages"]["foo"]["artifacts"]
        self.assertEqual(sorted(a["supported_platforms"][0] for a in arts), ["kindlehf", "kindlepw2"])
        self.run_add(good(version=(1, 3, 0)), version="1.3.0")
        arts = json.load(open(os.path.join(self.tmp, "manifest.json")))["packages"]["foo"]["artifacts"]
        hf = [a for a in arts if a["supported_platforms"] == ["kindlehf"]]
        self.assertEqual(len(hf), 1)
        self.assertEqual(hf[0]["version"], [1, 3, 0])

    def test_rejections(self):
        mf = lambda **o: json.dumps(dict({"id": "foo", "version": [1, 2, 3], "supported_platforms": ["kindlehf"]}, **o))
        bad = {
            "path traversal": make_pkg([("manifest.json", mf(), "file"), ("../evil", "x", "file")]),
            "absolute path": make_pkg([("manifest.json", mf(), "file"), ("/etc/evil", "x", "file")]),
            "symlink": make_pkg([("manifest.json", mf(), "file"), ("l", "/etc/passwd", "symlink")]),
            "no manifest": make_pkg([("install.sh", "x", "file")]),
            "wrong id": make_pkg([("manifest.json", mf(id="other"), "file")]),
            "wrong version": make_pkg([("manifest.json", mf(version=[9, 9, 9]), "file")]),
            "platform missing": make_pkg([("manifest.json", mf(supported_platforms=["kindlepw2"]), "file")]),
            "has dependencies": make_pkg([("manifest.json", mf(dependencies=["x"]), "file")]),
            "bad manifest json": make_pkg([("manifest.json", "{nope", "file")]),
            "not gzip": b"plain bytes",
        }
        for name, data in bad.items():
            with self.assertRaises(aa.Problem, msg=name):
                self.run_add(data)
        self.assertEqual(json.load(open(os.path.join(self.tmp, "manifest.json")))["packages"], {})

    def test_argument_checks(self):
        for kw in ({"url": "http://example.org/x"}, {"id": "bad id"}, {"version": "1.2"}, {"platforms": ["windows"]},
                   {"category": "weird"}, {"description": "x" * 300}, {"name": ""}):
            with self.assertRaises(aa.Problem, msg=str(kw)):
                self.run_add(good(), **kw)


if __name__ == "__main__":
    unittest.main()
