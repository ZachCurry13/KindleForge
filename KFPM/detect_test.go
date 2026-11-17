package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

var koreaderEntry = DetectEntry{
	ID: "koreader", Name: "KOReader", Description: "Document reader.", Paths: []string{"koreader/koreader.sh"},
	VersionFile: "koreader/git-rev", VersionRegex: `v?(\d{4}\.\d{2}(?:\.\d+)?)`, LatestGitHub: "koreader/koreader", Note: "Use KOReader's own updater.",
}

func detectRoot(t *testing.T, files map[string]string) Config {
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	return Config{UserRoot: root, DataDir: filepath.Join(root, ".KFPM")}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v2026.07.1", "2026.07", 1}, {"2026.07", "2026.07.0", 0}, {"2025.04", "v2026.07.1", -1},
		{"1.2", "1.2.0", 0}, {"10.0", "9.9", 1}, {"", "1.0", -1}, {"v2026.07.1", "v2026.07.1", 0},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compare(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestScanDetectedFindsOnlyCompleteMatchesAndReadsVersions(t *testing.T) {
	c := detectRoot(t, map[string]string{
		"koreader/koreader.sh": "#!/bin/sh", "koreader/git-rev": "v2025.04-45-gabcdef_2025-05-07\n",
		"kuala/kuala": "bin", "documents/kuala.sh": "#!/bin/sh",
		"gargoyle/gargoyle": "bin", // the scriptlet is missing: not a complete match
	})
	entries := []DetectEntry{
		koreaderEntry,
		{ID: "kuala", Name: "KUALA", Paths: []string{"kuala/kuala", "documents/kuala.sh"}},
		{ID: "gargoyle", Name: "Gargoyle", Paths: []string{"gargoyle", "documents/gargoyle.sh"}},
	}
	got := scanDetected(c, entries)
	if len(got) != 2 || got[0].Entry.ID != "koreader" || got[1].Entry.ID != "kuala" {
		t.Fatalf("found %+v", got)
	}
	if got[0].Version != "2025.04" || got[1].Version != "" {
		t.Errorf("versions: %q %q", got[0].Version, got[1].Version)
	}
}

func TestDetectRejectsUnsafeEntries(t *testing.T) {
	c := detectRoot(t, map[string]string{"a/b": "x"})
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("v9.9"), 0o644)
	os.Symlink(outside, filepath.Join(c.UserRoot, "link"))
	for name, e := range map[string]DetectEntry{
		"absolute path":  {ID: "x", Name: "X", Paths: []string{"/etc/passwd"}},
		"dotdot":         {ID: "x", Name: "X", Paths: []string{"a/../../etc"}},
		"dotdot version": {ID: "x", Name: "X", Paths: []string{"a/b"}, VersionFile: "../x"},
		"bad regex":      {ID: "x", Name: "X", Paths: []string{"a/b"}, VersionRegex: "(unclosed"},
		"bad repo":       {ID: "x", Name: "X", Paths: []string{"a/b"}, LatestGitHub: "not a slug; rm -rf"},
		"no paths":       {ID: "x", Name: "X"},
		"bad id":         {ID: "../x", Name: "X", Paths: []string{"a/b"}},
		"symlinked path": {ID: "x", Name: "X", Paths: []string{"link"}},
	} {
		if got := scanDetected(c, []DetectEntry{e}); len(got) != 0 {
			t.Errorf("%s accepted: %+v", name, got)
		}
	}
	// version files outside the root are never read
	e := DetectEntry{ID: "x", Name: "X", Paths: []string{"a/b"}, VersionFile: "link/secret"}
	if got := scanDetected(c, []DetectEntry{e}); len(got) != 1 || got[0].Version != "" {
		t.Errorf("a version file reached through a link outside the root must not be read: %+v", got)
	}
}

// rewrite sends every request to the test server, whatever its URL.
type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return r.base.RoundTrip(req)
}

func fakeGitHubAPI(t *testing.T, tag string) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/koreader/koreader/releases/latest" {
			fmt.Fprintf(w, `{"tag_name":%q}`, tag)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	old := httpClient
	httpClient = &http.Client{Transport: rewriteTransport{target: u, base: srv.Client().Transport}}
	t.Cleanup(func() { httpClient = old })
}

func TestLatestIsCachedAndUsedOffline(t *testing.T) {
	fakeGitHubAPI(t, "v2026.07.1")
	c := detectRoot(t, nil)
	if got := loadLatest(c, []DetectEntry{koreaderEntry}, true); got["koreader"] != "v2026.07.1" {
		t.Fatalf("online: %v", got)
	}
	httpClient = &http.Client{Transport: rewriteTransport{target: &url.URL{Scheme: "https", Host: "127.0.0.1:1"}, base: http.DefaultTransport}}
	if got := loadLatest(c, []DetectEntry{koreaderEntry}, true); got["koreader"] != "v2026.07.1" {
		t.Errorf("a failed lookup must keep the cached value: %v", got)
	}
	if got := loadLatest(detectRoot(t, nil), []DetectEntry{koreaderEntry}, false); len(got) != 0 {
		t.Errorf("nothing cached and offline: %v", got)
	}
}

func TestApplyDetectedMergesIntoCatalog(t *testing.T) {
	fakeGitHubAPI(t, "v2026.07.1")
	c := detectRoot(t, map[string]string{
		"koreader/koreader.sh": "x", "koreader/git-rev": "v2025.04-45-gabc\n",
		"kuala/kuala": "b", "documents/kuala.sh": "s",
		"ranki/ranki": "b", "documents/ranki.sh": "s",
	})
	rf := ReposFile{Detect: []DetectEntry{
		koreaderEntry,
		{ID: "kuala", Name: "KUALA", Paths: []string{"kuala/kuala", "documents/kuala.sh"}, Note: "found by hand"},
		{ID: "ranki", Name: "RAnki", Paths: []string{"ranki/ranki", "documents/ranki.sh"}},
	}}
	cat := Catalog{Packages: []CatPackage{
		{ID: "kuala", Name: "KUALA", Version: "1.1.0", Supported: true},                  // in the store, not installed by us
		{ID: "ranki", Name: "RAnki", Version: "0.2.0", Supported: true, Installed: true}, // already managed
	}}
	applyDetected(c, rf, true, &cat)

	byID := map[string]CatPackage{}
	for _, p := range cat.Packages {
		byID[p.ID] = p
	}
	if k := byID["kuala"]; !k.Detected || k.Installed || k.ReadOnly || k.Note != "found by hand" {
		t.Errorf("kuala should be offered for take-over: %+v", k)
	}
	if r := byID["ranki"]; r.Detected {
		t.Errorf("a managed package must not be flagged: %+v", r)
	}
	ko := byID["koreader"]
	if !ko.ReadOnly || !ko.Installed || !ko.Detected || ko.InstalledVersion != "2025.04" || ko.Version != "2026.07.1" || !ko.UpdateAvailable {
		t.Errorf("koreader: %+v", ko)
	}

	// up to date, and a nightly ahead of the release, are not "updates"
	for _, rev := range []string{"v2026.07.1\n", "v2026.07.1-12-gdeadbee_2026-08-02\n"} {
		os.WriteFile(filepath.Join(c.UserRoot, "koreader", "git-rev"), []byte(rev), 0o644)
		cat2 := Catalog{}
		applyDetected(c, rf, false, &cat2)
		for _, p := range cat2.Packages {
			if p.ID == "koreader" && p.UpdateAvailable {
				t.Errorf("%q wrongly flagged as outdated", rev)
			}
		}
	}
	// unknown version: no false claim either way
	os.Remove(filepath.Join(c.UserRoot, "koreader", "git-rev"))
	cat3 := Catalog{}
	applyDetected(c, rf, false, &cat3)
	for _, p := range cat3.Packages {
		if p.ID == "koreader" && (p.UpdateAvailable || p.InstalledVersion != "") {
			t.Errorf("unknown version must stay unknown: %+v", p)
		}
	}
}

func TestTakeOverInstallsCatalogVersionOverManualCopy(t *testing.T) {
	rs := newRepo(t)
	rs.pkgs[rs.url] = goodPkg(t, "demo", [3]int{1, 0, 0}, okInstall)
	c, rf := kpkgConfig(t, rs)
	for name, body := range map[string]string{"demo/demo": "hand-made binary", "documents/demo.sh": "#!/bin/sh"} {
		p := filepath.Join(c.UserRoot, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	rf.Detect = []DetectEntry{{ID: "demo", Name: "Demo", Paths: []string{"demo/demo", "documents/demo.sh"}}}

	cat := buildCatalog(c, rf, true)
	applyDetected(c, rf, false, &cat)
	var p CatPackage
	for _, x := range cat.Packages {
		if x.ID == "demo" {
			p = x
		}
	}
	if !p.Detected || p.Installed || p.Version != "1.0.0" {
		t.Fatalf("before: %+v", p)
	}
	if err := kpkgInstall(c, rf, "demo", false); err != nil {
		t.Fatal(err)
	}
	cat = buildCatalog(c, rf, false)
	applyDetected(c, rf, false, &cat)
	for _, x := range cat.Packages {
		if x.ID == "demo" && (x.Detected || !x.Installed || x.InstalledVersion != "1.0.0") {
			t.Errorf("after take-over it should be managed: %+v", x)
		}
	}
}
