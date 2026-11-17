package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type zipEnt struct {
	name, body string
	symlink    bool
	exec       bool
}

func makeEntZip(t *testing.T, ents []zipEnt) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.symlink:
			h.SetMode(os.ModeSymlink | 0o777)
		case strings.HasSuffix(e.name, "/"):
			h.SetMode(os.ModeDir | 0o755)
		case e.exec:
			h.SetMode(0o755)
		default:
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	zw.Close()
	return buf.Bytes()
}

func goodPlugin(name string) []zipEnt {
	return []zipEnt{
		{name: "README.md", body: "outside the plugin: must not be extracted"},
		{name: name + ".koplugin/", body: ""},
		{name: name + ".koplugin/_meta.lua", body: "return {name='" + name + "'}"},
		{name: name + ".koplugin/main.lua", body: "-- code"},
		{name: name + ".koplugin/lib/util.lua", body: "-- util"},
	}
}

type zenFixture struct {
	*httptest.Server
	version  string
	asset    []byte
	digest   string // "auto" = correct sha256
	assetURL string // override the url written in versions.json
	pkgFor   func() map[string]any
}

func newZen(t *testing.T) *zenFixture {
	zf := &zenFixture{version: "1.0.0", digest: "auto"}
	zf.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repo/manifest.json":
			pkgs := []map[string]any{
				{"id": "hello", "name": "Hello Plugin", "version": zf.version, "description": "Says hello.", "author": "someone",
					"category": "media", "platforms": []string{"koreader"}, "dependencies": []string{}, "source_type": "release",
					"source_asset": "hello.koplugin-v" + zf.version + ".zip", "versions_url": "packages/hello/versions.json"},
				{"id": "patchy", "name": "A patch", "version": "1.0", "platforms": []string{"koreader"}, "source_type": "release",
					"source_asset": "patch.lua", "versions_url": "x"}, // not a plugin zip
				{"id": "needsdep", "name": "Needs dep", "version": "1.0", "platforms": []string{"koreader"}, "dependencies": []string{"other"},
					"source_type": "release", "source_asset": "n.koplugin.zip", "versions_url": "x"},
				{"id": "native", "name": "Native", "version": "1.0", "platforms": []string{"kindle"}, "install_url": "i.sh"},
				{"id": "srcarchive", "name": "Source", "version": "1.0", "platforms": []string{"koreader"}, "source_type": "source", "versions_url": "x"},
			}
			json.NewEncoder(w).Encode(map[string]any{"schema_version": "1", "repo": map[string]any{"id": "ZenRepo", "name": "Zen"}, "packages": pkgs})
		case "/repo/packages/hello/versions.json":
			sum := sha256.Sum256(zf.asset)
			dg := "sha256:" + hex.EncodeToString(sum[:])
			switch zf.digest {
			case "auto":
			case "none":
				dg = ""
			default:
				dg = zf.digest
			}
			u := zf.assetURL
			if u == "" {
				u = zf.URL + "/dl/hello.zip"
			}
			json.NewEncoder(w).Encode(map[string]any{"releases": []map[string]any{
				{"tag_name": "v" + zf.version, "assets": []map[string]any{{"name": "hello.koplugin-v" + zf.version + ".zip", "url": u, "size": len(zf.asset), "digest": dg}}},
			}})
		case "/dl/hello.zip":
			w.Write(zf.asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(zf.Close)
	old := httpClient
	httpClient = zf.Client()
	t.Cleanup(func() { httpClient = old })
	oldOK := zenAssetOK
	zenAssetOK = func(u string) bool { return strings.HasPrefix(u, zf.URL+"/dl/") } // the fixture stands in for github.com
	t.Cleanup(func() { zenAssetOK = oldOK })
	return zf
}

func zenConfig(t *testing.T, zf *zenFixture, withKOReader bool) (Config, ReposFile) {
	c := gingrEnvConfig(t, "ok")
	c.UserRoot = filepath.Join(filepath.Dir(c.DataDir), "us")
	if withKOReader {
		os.MkdirAll(filepath.Join(c.UserRoot, "koreader", "plugins"), 0o755)
	}
	rf := ReposFile{Repos: []Repo{{ID: "ZenRepo", URL: zf.URL + "/repo/manifest.json", Format: "zen"}}}
	return c, rf
}

func pluginDir(c Config, name string) string {
	return filepath.Join(c.UserRoot, "koreader", "plugins", name+".koplugin")
}

func TestZenEligibility(t *testing.T) {
	zf := newZen(t)
	c, rf := zenConfig(t, zf, true)
	zf.asset = makeEntZip(t, goodPlugin("hello"))
	srcs, st := zenSources(c, rf, true)
	if len(srcs) != 1 || srcs["ZenRepo--hello"].Pkg.Name != "Hello Plugin" {
		t.Fatalf("only the release-zip plugin should be offered: %v", srcs)
	}
	if len(st) != 1 || st[0].Source != "network" || st[0].Packages != 5 {
		t.Errorf("status: %+v", st)
	}
	// without KOReader nothing is offered (but the source still reports its size)
	c2, _ := zenConfig(t, zf, false)
	srcs2, st2 := zenSources(c2, rf, true)
	if len(srcs2) != 0 || st2[0].Packages != 5 {
		t.Errorf("no KOReader: %v %+v", srcs2, st2)
	}
}

func TestZenInstallUpdateUninstall(t *testing.T) {
	zf := newZen(t)
	zf.asset = makeEntZip(t, goodPlugin("hello"))
	c, rf := zenConfig(t, zf, true)
	zenSources(c, rf, true) // sync once so the manifest is saved

	if err := zenInstall(c, rf, "ZenRepo--hello", false); err != nil {
		t.Fatal(err)
	}
	dir := pluginDir(c, "hello")
	for _, f := range []string{"_meta.lua", "main.lua", "lib/util.lua"} {
		if !exists_(filepath.Join(dir, f)) {
			t.Errorf("%s missing", f)
		}
	}
	if exists_(filepath.Join(c.UserRoot, "koreader", "plugins", "README.md")) || exists_(filepath.Join(dir, "README.md")) {
		t.Error("files outside the plugin folder must not be extracted")
	}
	for _, left := range []string{dir + ".kf-new", dir + ".kf-old", filepath.Join(kpkgRoot(c), "tmp")} {
		if exists_(left) {
			t.Errorf("leftover %s", left)
		}
	}
	r := loadKpkgInstalled(c)["ZenRepo--hello"]
	if r.Kind != "zen" || r.Module != "hello" || r.Version != "1.0.0" {
		t.Errorf("record %+v", r)
	}

	// catalog: installed, then an update appears
	cat := Catalog{}
	applyZen(c, rf, false, &cat)
	if p := cat.Packages[0]; !p.Installed || p.UpdateAvailable || p.Category != "media" || !strings.Contains(p.Note, "Restart KOReader") {
		t.Errorf("%+v", p)
	}
	zf.version = "1.1.0"
	zf.asset = makeEntZip(t, append(goodPlugin("hello"), zipEnt{name: "hello.koplugin/new.lua", body: "-- new"}))
	zenSources(c, rf, true)
	cat = Catalog{}
	applyZen(c, rf, false, &cat)
	if !cat.Packages[0].UpdateAvailable {
		t.Error("update should be offered")
	}
	if n, err := zenUpgradeAll(c, rf, false); err != nil || n != 1 {
		t.Fatalf("upgrade: %d %v", n, err)
	}
	if !exists_(filepath.Join(dir, "new.lua")) || loadKpkgInstalled(c)["ZenRepo--hello"].Version != "1.1.0" {
		t.Error("update not applied")
	}

	if err := zenUninstall(c, "ZenRepo--hello"); err != nil {
		t.Fatal(err)
	}
	if exists_(dir) || len(loadKpkgInstalled(c)) != 0 {
		t.Error("not removed")
	}
	if zenUninstall(c, "ZenRepo--hello") == nil {
		t.Error("removing twice must fail")
	}
}

func TestZenInstallRefusals(t *testing.T) {
	type tc struct {
		name   string
		asset  func(t *testing.T) []byte
		digest string
		url    string
		want   string
	}
	good := func(t *testing.T) []byte { return makeEntZip(t, goodPlugin("hello")) }
	cases := []tc{
		{"wrong checksum", good, "sha256:" + strings.Repeat("0", 64), "", "checksum"},
		{"no checksum", good, "none", "", "no checksum"},
		{"malformed checksum", good, "md5:abc", "", "no checksum"},
		{"not a github release url", good, "auto", "https://evil.example.org/hello.zip", "only GitHub release files"},
		{"path traversal", func(t *testing.T) []byte {
			return makeEntZip(t, append(goodPlugin("hello"), zipEnt{name: "hello.koplugin/../../evil.lua", body: "x"}))
		}, "auto", "", "unsafe path"},
		{"absolute path", func(t *testing.T) []byte {
			return makeEntZip(t, append(goodPlugin("hello"), zipEnt{name: "/etc/evil", body: "x"}))
		}, "auto", "", "unsafe path"},
		{"symlink inside the plugin", func(t *testing.T) []byte {
			return makeEntZip(t, append(goodPlugin("hello"), zipEnt{name: "hello.koplugin/link", body: "/etc/passwd", symlink: true}))
		}, "auto", "", "links are not allowed"},
		{"two plugins in one zip", func(t *testing.T) []byte {
			return makeEntZip(t, append(goodPlugin("hello"), zipEnt{name: "other.koplugin/main.lua", body: "x"}))
		}, "auto", "", "layout is not supported"},
		{"flat layout (no .koplugin folder)", func(t *testing.T) []byte {
			return makeEntZip(t, []zipEnt{{name: "_meta.lua", body: "x"}, {name: "main.lua", body: "x"}})
		}, "auto", "", "layout is not supported"},
		{"not a plugin (no lua entry points)", func(t *testing.T) []byte {
			return makeEntZip(t, []zipEnt{{name: "hello.koplugin/readme.txt", body: "x"}})
		}, "auto", "", "does not look like a KOReader plugin"},
		{"unsafe plugin name", func(t *testing.T) []byte {
			return makeEntZip(t, []zipEnt{{name: "a b.koplugin/main.lua", body: "x"}})
		}, "auto", "", "unsafe plugin name"},
		{"not a zip", func(t *testing.T) []byte { return []byte("plain bytes") }, "auto", "", "valid zip"},
	}
	for _, c0 := range cases {
		t.Run(c0.name, func(t *testing.T) {
			zf := newZen(t)
			zf.asset, zf.digest, zf.assetURL = c0.asset(t), c0.digest, c0.url
			c, rf := zenConfig(t, zf, true)
			zenSources(c, rf, true)
			err := zenInstall(c, rf, "ZenRepo--hello", false)
			if err == nil || !strings.Contains(err.Error(), c0.want) {
				t.Fatalf("want %q, got %v", c0.want, err)
			}
			if len(loadKpkgInstalled(c)) != 0 || exists_(pluginDir(c, "hello")) || exists_(pluginDir(c, "hello")+".kf-new") {
				t.Error("a refused package left something behind")
			}
			if exists_(filepath.Join(c.UserRoot, "evil.lua")) || exists_("/etc/evil") {
				t.Error("a file escaped")
			}
		})
	}
}

func TestZenNeverReplacesAPluginItDidNotInstall(t *testing.T) {
	zf := newZen(t)
	zf.asset = makeEntZip(t, goodPlugin("hello"))
	c, rf := zenConfig(t, zf, true)
	zenSources(c, rf, true)
	mine := pluginDir(c, "hello")
	os.MkdirAll(mine, 0o755)
	os.WriteFile(filepath.Join(mine, "main.lua"), []byte("HAND-MADE"), 0o644)
	err := zenInstall(c, rf, "ZenRepo--hello", false)
	if err == nil || !strings.Contains(err.Error(), "left alone") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(mine, "main.lua")); string(b) != "HAND-MADE" {
		t.Error("the existing plugin was modified")
	}
}

func TestZenUninstallOnlyTouchesTheRecordedPlugin(t *testing.T) {
	zf := newZen(t)
	c, rf := zenConfig(t, zf, true)
	_ = rf
	other := pluginDir(c, "precious")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "main.lua"), []byte("x"), 0o644)
	// a tampered record naming a path outside plugins/ must not be followed
	for _, mod := range []string{"../../documents", "../precious", "a/b", ""} {
		saveKpkgInstalled(c, map[string]KpkgRecord{"ZenRepo--x": {Kind: "zen", Module: mod, Version: "1"}})
		if zenUninstall(c, "ZenRepo--x") == nil {
			t.Errorf("module %q accepted", mod)
		}
	}
	if !exists_(other) {
		t.Error("an unrelated plugin was removed")
	}
	// records of the other kind are not removable through this path
	saveKpkgInstalled(c, map[string]KpkgRecord{"kuala": {Version: "1.0.0"}})
	if zenUninstall(c, "kuala") == nil {
		t.Error("a KindleModding package must not be removed by the plugin remover")
	}
}

func TestChangeRoutesPluginsToTheZenInstaller(t *testing.T) {
	zf := newZen(t)
	zf.asset = makeEntZip(t, goodPlugin("hello"))
	c, _ := zenConfig(t, zf, true)
	os.MkdirAll(c.path("cache"), 0o755)
	cfg := fmt.Sprintf(`{"repos":[{"id":"ZenRepo","url":%q,"format":"zen"}]}`, zf.URL+"/repo/manifest.json")
	os.WriteFile(c.path("cache", "config.json"), []byte(cfg), 0o644)
	loadZenManifest(c, Repo{ID: "ZenRepo", URL: zf.URL + "/repo/manifest.json", Format: "zen"}, true)

	if err := change(c, "install", "ZenRepo--hello", "packageInstallStatus", false); err != nil {
		t.Fatal(err)
	}
	if !exists_(pluginDir(c, "hello")) || loadKpkgInstalled(c)["ZenRepo--hello"].Kind != "zen" {
		t.Error("the plugin was not installed through the zen path")
	}
	if len(gingrInstalled(c)) != 1 { // only the fake list's own "hello"
		t.Errorf("gingr kpm must not be involved: %v", gingrInstalled(c))
	}
	if err := change(c, "uninstall", "ZenRepo--hello", "packageUninstallStatus", false); err != nil {
		t.Fatal(err)
	}
	if exists_(pluginDir(c, "hello")) {
		t.Error("not removed")
	}
}

func TestDefaultAssetPatternOnlyAllowsGitHubReleases(t *testing.T) {
	ok := []string{"https://github.com/o/r/releases/download/v1.0/a.zip", "https://github.com/o-x/r.y/releases/download/1/a-b.koplugin.zip"}
	bad := []string{"http://github.com/o/r/releases/download/v1/a.zip", "https://github.com.evil.org/o/r/releases/download/v1/a.zip",
		"https://github.com/o/r/archive/main.zip", "https://raw.githubusercontent.com/o/r/main/a.zip", "https://github.com/o/r/releases/download/v1/", "https://github.com/o/r/releases/download/v1/a/../../x"}
	re := regexp.MustCompile(githubAssetRe.String())
	for _, u := range ok {
		if !re.MatchString(u) {
			t.Errorf("%s rejected", u)
		}
	}
	for _, u := range bad {
		if re.MatchString(u) {
			t.Errorf("%s accepted", u)
		}
	}
}

func TestInstalledPluginIsListedExactlyOnce(t *testing.T) {
	zf := newZen(t)
	zf.asset = makeEntZip(t, goodPlugin("hello"))
	c, rf := zenConfig(t, zf, true)
	zenSources(c, rf, true)
	if err := zenInstall(c, rf, "ZenRepo--hello", false); err != nil {
		t.Fatal(err)
	}
	// the same sequence a refresh runs: backend catalog, then the plugin sources
	for _, online := range []bool{false, true} {
		cat := buildCatalog(c, rf, online)
		applyZen(c, rf, online, &cat)
		n := 0
		for _, p := range cat.Packages {
			if p.ID == "ZenRepo--hello" {
				n++
				if !p.Installed || p.Note == "" {
					t.Errorf("entry: %+v", p)
				}
			}
		}
		if n != 1 {
			t.Errorf("listed %d times (online=%v)", n, online)
		}
	}
	// and still exactly once when its source has been switched off
	cat := buildCatalog(c, ReposFile{}, false)
	applyZen(c, ReposFile{}, false, &cat)
	n := 0
	for _, p := range cat.Packages {
		if p.ID == "ZenRepo--hello" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("source off: listed %d times", n)
	}
}
