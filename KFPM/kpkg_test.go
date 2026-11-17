package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name, body string
	mode       int64
	typeflag   byte
	link       string
}

func makeKpkg(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: e.typeflag, Linkname: e.link}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func goodPkg(t *testing.T, id string, ver [3]int, installBody string) []byte {
	mf, _ := json.Marshal(map[string]any{"manifest_version": 2, "id": id, "version": ver, "supported_platforms": []string{"kindlehf"}})
	return makeKpkg(t, []tarEntry{
		{name: "manifest.json", body: string(mf)},
		{name: "install.sh", body: installBody, mode: 0o755},
		{name: "uninstall.sh", body: "#!/bin/sh\necho bye > ../uninstalled-" + id + ".flag\n", mode: 0o755},
		{name: "payload/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "payload/run.sh", body: "#!/bin/sh\n", mode: 0o755},
		{name: "payload/data.txt", body: "data"},
	})
}

const okInstall = "#!/bin/sh\necho \"$KPM_PLATFORM:$(basename $PWD)\" > ../installed-flag.txt\necho installing\n"

// repoServer serves a manifest at /Repo/manifest.json and artifacts under /Repo/.
type repoServer struct {
	*httptest.Server
	version [3]int
	pkgs    map[string][]byte // artifact path -> bytes
	url     string            // artifact url as written in the manifest
	deps    []string
}

func newRepo(t *testing.T) *repoServer {
	rs := &repoServer{version: [3]int{1, 0, 0}, pkgs: map[string][]byte{}, url: "packages/demo/demo.kpkg"}
	rs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Repo/manifest.json" {
			deps := []string{}
			deps = append(deps, rs.deps...)
			m := map[string]any{"manifest_version": 2, "id": "test", "packages": map[string]any{
				"demo": map[string]any{"name": "Demo", "author": "Tester", "description": "A demo package.",
					"artifacts": []any{map[string]any{"url": rs.url, "version": rs.version, "dependencies": deps, "supported_platforms": []string{"kindlehf"}}}},
			}}
			json.NewEncoder(w).Encode(m)
			return
		}
		if b, ok := rs.pkgs[strings.TrimPrefix(r.URL.Path, "/Repo/")]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(rs.Close)
	old := httpClient
	httpClient = rs.Client()
	t.Cleanup(func() { httpClient = old })
	return rs
}

func kpkgConfig(t *testing.T, rs *repoServer) (Config, ReposFile) {
	c := gingrEnvConfig(t, "ok")
	c.Platform = "kindlehf"
	c.UserRoot = filepath.Join(filepath.Dir(c.DataDir), "us")
	os.MkdirAll(c.UserRoot, 0o755)
	rf := ReposFile{Repos: []Repo{{ID: "test", URL: rs.URL + "/Repo/manifest.json"}}, Categories: map[string]string{"demo": "utilities"}}
	return c, rf
}

func TestKpkgInstallUpdateUninstall(t *testing.T) {
	rs := newRepo(t)
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{1, 0, 0}, okInstall)
	c, rf := kpkgConfig(t, rs)

	if err := kpkgInstall(c, rf, "demo", false); err != nil {
		t.Fatal(err)
	}
	pkgDir := kpkgPkgDir(c, "demo")
	if flag := read(t, filepath.Join(kpkgRoot(c), "pkg", "installed-flag.txt")); strings.TrimSpace(flag) != "kindlehf:demo" {
		t.Errorf("hook env/cwd wrong: %q", flag)
	}
	if st, err := os.Stat(filepath.Join(pkgDir, "payload", "run.sh")); err != nil || st.Mode()&0o111 == 0 {
		t.Error("executable bit lost")
	}
	if st, _ := os.Stat(filepath.Join(pkgDir, "payload", "data.txt")); st.Mode()&0o111 != 0 {
		t.Error("data file became executable")
	}
	if loadKpkgInstalled(c)["demo"].Version != "1.0.0" {
		t.Errorf("record: %+v", loadKpkgInstalled(c))
	}
	if _, ok := listInstalled(c)["demo"]; !ok {
		t.Error("installed list should include KFPM-installed packages")
	}
	for _, leftover := range []string{pkgDir + ".new", pkgDir + ".old", filepath.Join(kpkgRoot(c), "tmp")} {
		if exists_(leftover) {
			t.Errorf("leftover %s", leftover)
		}
	}

	// catalog: installed, no update yet
	cat := buildCatalog(c, rf, true)
	var demo CatPackage
	for _, p := range cat.Packages {
		if p.ID == "demo" {
			demo = p
		}
	}
	if !demo.Installed || demo.InstalledVersion != "1.0.0" || demo.Version != "1.0.0" || demo.UpdateAvailable || demo.Name != "Demo" || demo.Author != "Tester" {
		t.Errorf("catalog entry: %+v", demo)
	}

	// a newer version is published
	rs.version = [3]int{1, 1, 0}
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{1, 1, 0}, okInstall)
	cat = buildCatalog(c, rf, true)
	for _, p := range cat.Packages {
		if p.ID == "demo" && !p.UpdateAvailable {
			t.Error("update should be offered")
		}
	}
	n, err := kpkgUpgradeAll(c, rf, false)
	if err != nil || n != 1 {
		t.Fatalf("upgrade: %d %v", n, err)
	}
	if loadKpkgInstalled(c)["demo"].Version != "1.1.0" {
		t.Error("version not updated")
	}
	if n, _ := kpkgUpgradeAll(c, rf, false); n != 0 {
		t.Error("nothing left to update")
	}

	if err := kpkgUninstall(c, "demo", false); err != nil {
		t.Fatal(err)
	}
	if !exists_(filepath.Join(kpkgRoot(c), "pkg", "uninstalled-demo.flag")) {
		t.Error("uninstall hook did not run")
	}
	if exists_(pkgDir) || len(loadKpkgInstalled(c)) != 0 {
		t.Error("package not removed")
	}
	if kpkgUninstall(c, "demo", false) == nil {
		t.Error("removing something not installed must fail")
	}
}

func TestKpkgRejectsBadPackages(t *testing.T) {
	cases := map[string]func(t *testing.T) []byte{
		"wrong id":      func(t *testing.T) []byte { return goodPkg(t, "other", [3]int{1, 0, 0}, okInstall) },
		"wrong version": func(t *testing.T) []byte { return goodPkg(t, "demo", [3]int{9, 9, 9}, okInstall) },
		"platform not supported": func(t *testing.T) []byte {
			mf := `{"id":"demo","version":[1,0,0],"supported_platforms":["kindlepw2"]}`
			return makeKpkg(t, []tarEntry{{name: "manifest.json", body: mf}, {name: "install.sh", body: okInstall}})
		},
		"no manifest": func(t *testing.T) []byte {
			return makeKpkg(t, []tarEntry{{name: "install.sh", body: okInstall}})
		},
		"path traversal": func(t *testing.T) []byte {
			return makeKpkg(t, []tarEntry{{name: "../../evil.sh", body: "x"}, {name: "manifest.json", body: "{}"}})
		},
		"absolute path": func(t *testing.T) []byte {
			return makeKpkg(t, []tarEntry{{name: "/etc/evil", body: "x"}})
		},
		"symlink": func(t *testing.T) []byte {
			return makeKpkg(t, []tarEntry{{name: "link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}})
		},
		"not a package": func(t *testing.T) []byte { return []byte("this is not gzip") },
		"hook fails": func(t *testing.T) []byte {
			return goodPkg(t, "demo", [3]int{1, 0, 0}, "#!/bin/sh\necho oops >&2\nexit 7\n")
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			rs := newRepo(t)
			rs.pkgs[rs.url] = mk(t)
			c, rf := kpkgConfig(t, rs)
			if err := kpkgInstall(c, rf, "demo", false); err == nil {
				t.Fatal("accepted")
			}
			if len(loadKpkgInstalled(c)) != 0 || exists_(kpkgPkgDir(c, "demo")) || exists_(kpkgPkgDir(c, "demo")+".new") {
				t.Error("failed install left state behind")
			}
			outside := filepath.Join(filepath.Dir(kpkgRoot(c)), "..", "evil.sh")
			if exists_(outside) || exists_("/etc/evil") {
				t.Error("file escaped")
			}
		})
	}
}

func TestKpkgFailedUpdateRestoresOldVersion(t *testing.T) {
	rs := newRepo(t)
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{1, 0, 0}, okInstall)
	c, rf := kpkgConfig(t, rs)
	if err := kpkgInstall(c, rf, "demo", false); err != nil {
		t.Fatal(err)
	}
	rs.version = [3]int{2, 0, 0}
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{2, 0, 0}, "#!/bin/sh\nexit 3\n")
	if err := kpkgInstall(c, rf, "demo", false); err == nil {
		t.Fatal("failing hook accepted")
	}
	if loadKpkgInstalled(c)["demo"].Version != "1.0.0" {
		t.Error("record changed on failure")
	}
	if !exists_(filepath.Join(kpkgPkgDir(c, "demo"), "payload", "run.sh")) || exists_(kpkgPkgDir(c, "demo")+".old") {
		t.Error("old version was not restored")
	}
}

func TestKpkgRefusesHTTPAndDependencies(t *testing.T) {
	rs := newRepo(t)
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{1, 0, 0}, okInstall)
	c, rf := kpkgConfig(t, rs)
	sync := func() { loadManifest(c, rf.Repos[0], true) } // what Refresh does: re-read the manifest

	rs.url = "http://example.org/demo.kpkg"
	sync()
	if err := kpkgInstall(c, rf, "demo", false); err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Errorf("http artifact: %v", err)
	}
	rs.url = "packages/demo/demo.kpkg"
	rs.deps = []string{"other"}
	sync()
	if err := kpkgInstall(c, rf, "demo", false); err == nil || !strings.Contains(err.Error(), "needs other packages") {
		t.Errorf("dependencies: %v", err)
	}
	rs.deps = nil
	sync()
	if err := kpkgInstall(c, rf, "nope", false); err == nil {
		t.Error("unknown package")
	}
	// an unreachable https location: the download fails and nothing is installed
	rs.url = "https://127.0.0.1:1/x.kpkg"
	sync()
	if err := kpkgInstall(c, rf, "demo", false); err == nil {
		t.Error("failed download accepted")
	}
	if len(loadKpkgInstalled(c)) != 0 || exists_(kpkgPkgDir(c, "demo")) {
		t.Error("failed download left state behind")
	}
}

func TestResolveArtifactURL(t *testing.T) {
	u, err := resolveArtifactURL("https://x.github.io/Repo/manifest.json", "packages/a/a_1.kpkg")
	if err != nil || u != "https://x.github.io/Repo/packages/a/a_1.kpkg" {
		t.Errorf("%s %v", u, err)
	}
	if _, err := resolveArtifactURL("https://x.io/Repo/manifest.json", "http://evil/a"); err == nil {
		t.Error("http accepted")
	}
}

func TestGingrCatalogMergesManifestPackages(t *testing.T) {
	rs := newRepo(t)
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{1, 0, 0}, okInstall)
	c, rf := kpkgConfig(t, rs)
	os.WriteFile(c.GingrList, []byte("hello\n"), 0o644)
	// the gingr mirror also lists "demo": the manifest (versioned) copy must win
	gsrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hello\ndemo\n") }))
	defer gsrv.Close()
	os.WriteFile(c.GingrMirror, []byte(gsrv.URL+"/list "+gsrv.URL+"/{pkg}.zip\n"), 0o644)
	// both servers use self-signed certs from different httptest servers: trust both
	pool := rs.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	pool.AddCert(gsrv.Certificate())

	cat := buildCatalog(c, rf, true)
	count := 0
	for _, p := range cat.Packages {
		if p.ID == "demo" {
			count++
			if p.Version != "1.0.0" || p.Repo != "test" {
				t.Errorf("manifest copy should win: %+v", p)
			}
		}
	}
	if count != 1 {
		t.Errorf("demo listed %d times", count)
	}
	if len(cat.Repos) != 2 {
		t.Errorf("repos: %+v", cat.Repos)
	}

	// hidden manifest packages disappear unless installed
	rf.Hidden = []string{"demo"}
	for _, p := range buildCatalog(c, rf, false).Packages {
		if p.ID == "demo" {
			t.Error("hidden package shown")
		}
	}
	// installed from a repo that is no longer listed: still manageable
	saveKpkgInstalled(c, map[string]KpkgRecord{"gone": {Version: "2.0.0", Repo: "old"}})
	found := false
	for _, p := range buildCatalog(c, rf, false).Packages {
		if p.ID == "gone" && p.Installed && p.InstalledVersion == "2.0.0" {
			found = true
		}
	}
	if !found {
		t.Error("orphaned installed package missing")
	}
}

func TestChangeRoutesToKpkgOrGingr(t *testing.T) {
	rs := newRepo(t)
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{1, 0, 0}, okInstall)
	c, _ := kpkgConfig(t, rs)
	// the catalog definition (cached) points at the test repo
	cfgJSON := fmt.Sprintf(`{"repos":[{"id":"test","url":"%s/Repo/manifest.json"}]}`, rs.URL)
	os.MkdirAll(c.path("cache"), 0o755)
	os.WriteFile(c.path("cache", "config.json"), []byte(cfgJSON), 0o644)
	// make the manifest known offline too
	loadManifest(c, Repo{ID: "test", URL: rs.URL + "/Repo/manifest.json"}, true)

	if err := change(c, "install", "demo", "packageInstallStatus", false); err != nil {
		t.Fatal(err)
	}
	if loadKpkgInstalled(c)["demo"].Version != "1.0.0" {
		t.Error("manifest package should be installed by KFPM")
	}
	if _, ok := gingrInstalled(c)["demo"]; ok {
		t.Error("it must not go through gingr kpm")
	}
	// an id the manifests do not know goes to gingr kpm (the fake appends to packages.list)
	if err := change(c, "install", "kwordle", "packageInstallStatus", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := gingrInstalled(c)["kwordle"]; !ok {
		t.Error("gingr package not installed via kpm -S")
	}
	if err := change(c, "uninstall", "demo", "packageUninstallStatus", false); err != nil {
		t.Fatal(err)
	}
	if len(loadKpkgInstalled(c)) != 0 {
		t.Error("kpkg uninstall did not run")
	}
}

// ---- logs -------------------------------------------------------------------

func TestHistoryLogMirrorsAndTrims(t *testing.T) {
	c := fakeKindle(t)
	os.Remove(filepath.Join(c.UserRoot, historyName))
	os.Remove(c.path("last-log.txt"))
	resetLog(c)
	appendLog(c, "first run line")
	resetLog(c)
	appendLog(c, "second run line")

	last := read(t, c.path("last-log.txt"))
	hist := read(t, filepath.Join(c.UserRoot, historyName))
	if strings.Contains(last, "first run line") || !strings.Contains(last, "second run line") {
		t.Errorf("last-log should hold only the latest run: %q", last)
	}
	if !strings.Contains(hist, "first run line") || !strings.Contains(hist, "second run line") || strings.Count(hist, "=====") < 4 {
		t.Errorf("history should keep both runs with headers: %q", hist)
	}

	// trimming keeps the newest part, on a line boundary
	big := strings.Repeat("old line of history\n", 20000) + "THE-NEWEST-LINE\n"
	os.WriteFile(filepath.Join(c.UserRoot, historyName), []byte(big), 0o644)
	resetLog(c)
	h2 := read(t, filepath.Join(c.UserRoot, historyName))
	if len(h2) > historyKeep+200 || !strings.Contains(h2, "THE-NEWEST-LINE") || !strings.HasPrefix(h2, "(older entries trimmed)") {
		t.Errorf("trim failed: %d bytes", len(h2))
	}

	// no root configured: no stray file
	c2 := Config{DataDir: t.TempDir()}
	appendLog(c2, "x")
	if historyPath(c2) != "" || !strings.Contains(read(t, c2.path("last-log.txt")), "x") {
		t.Error("without a user root only last-log.txt is written")
	}
}

func TestCleanupCanRemoveAndRestoreLogs(t *testing.T) {
	c := fakeKindle(t)
	res := applyCleanup(c, []string{"diag"})
	if res.Count != 5 || len(res.Errors) != 0 {
		t.Fatalf("%+v", res)
	}
	for _, f := range []string{"kindleforge-log.txt", "kfpm-diag.txt", ".KFPM/last-log.txt"} {
		if exists_(filepath.Join(c.UserRoot, f)) {
			t.Errorf("%s not moved", f)
		}
	}
	if !exists_(filepath.Join(c.UserRoot, ".KFPM", "repos.json")) {
		t.Error("settings must survive a log cleanup")
	}
	if r := restoreTrash(c); r.Count != 5 {
		t.Errorf("restore %+v", r)
	}
}
