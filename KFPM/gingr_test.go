package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGingrKPM writes a shell script that behaves like gingrspacecadet/kpm:
// -S appends to packages.list, -R removes from it. mode shapes the quirks.
func fakeGingrKPM(t *testing.T, list, mode string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
LIST='%s'
MODE='%s'
echo "fake kpm $@"
case "$1" in
 -S)
   [ "$MODE" = fail ] && { echo "Download failed" >&2; exit 1; }
   [ "$MODE" = silent ] && exit 0
   echo "$2" >> "$LIST"
   [ "$MODE" = crash ] && exit 139
   exit 0 ;;
 -R)
   [ "$MODE" = fail ] && exit 1
   grep -vx "$2" "$LIST" > "$LIST.t"; mv "$LIST.t" "$LIST"
   exit 0 ;;
esac
exit 2
`, list, mode)
	p := filepath.Join(t.TempDir(), "kpm")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func gingrEnvConfig(t *testing.T, mode string) Config {
	root := t.TempDir()
	list := filepath.Join(root, "packages.list")
	os.WriteFile(list, []byte("hello\n"), 0o644)
	c := Config{
		DataDir: filepath.Join(root, "data"), Backend: BackendGingr, Platform: "kindlepw2",
		GingrList: list, GingrMirror: filepath.Join(root, "mirrors.conf"),
	}
	c.KPMPath = fakeGingrKPM(t, list, mode)
	os.MkdirAll(c.DataDir, 0o755)
	return c
}

func TestDetectBackend(t *testing.T) {
	t.Setenv("KFPM_BACKEND", "gingr")
	if detectBackend() != BackendGingr {
		t.Error("override to gingr ignored")
	}
	t.Setenv("KFPM_BACKEND", "modding")
	if detectBackend() != BackendModding {
		t.Error("override to modding ignored")
	}
	t.Setenv("KFPM_BACKEND", "nonsense")
	_ = detectBackend() // must not panic; falls back to file probing
}

func TestGingrInstalledParsing(t *testing.T) {
	c := gingrEnvConfig(t, "ok")
	os.WriteFile(c.GingrList, []byte("hello\n  kwordle \n\nbad id;rm\nblackjack\r\n"), 0o644)
	got := gingrInstalled(c)
	if len(got) != 3 || got["kwordle"].ID == "" || got["blackjack"].ID == "" {
		t.Errorf("got %v", got)
	}
}

func TestGingrPackageOps(t *testing.T) {
	t.Run("install and uninstall", func(t *testing.T) {
		c := gingrEnvConfig(t, "ok")
		if err := gingrPackageOp(c, "install", "kwordle", false); err != nil {
			t.Fatal(err)
		}
		if _, ok := gingrInstalled(c)["kwordle"]; !ok {
			t.Error("not installed")
		}
		if err := gingrPackageOp(c, "uninstall", "kwordle", false); err != nil {
			t.Fatal(err)
		}
		if _, ok := gingrInstalled(c)["kwordle"]; ok {
			t.Error("still installed")
		}
	})
	t.Run("crash after a successful install still counts", func(t *testing.T) {
		c := gingrEnvConfig(t, "crash")
		if err := gingrPackageOp(c, "install", "kwordle", false); err != nil {
			t.Fatalf("list says installed, should succeed: %v", err)
		}
		if !strings.Contains(read(t, c.path("last.log")), "treating as success") {
			t.Error("override not logged")
		}
	})
	t.Run("real failure", func(t *testing.T) {
		c := gingrEnvConfig(t, "fail")
		if err := gingrPackageOp(c, "install", "kwordle", false); err == nil {
			t.Fatal("expected failure")
		}
		if err := gingrPackageOp(c, "uninstall", "hello", false); err == nil {
			t.Fatal("failed removal must be reported")
		}
	})
	t.Run("exit 0 but nothing installed", func(t *testing.T) {
		c := gingrEnvConfig(t, "silent")
		if err := gingrPackageOp(c, "install", "kwordle", false); err == nil {
			t.Fatal("must not report success when the list did not change")
		}
	})
}

func TestGingrCatalog(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "kpm\nhello\nkwordle\nkindlefetch\nmusl\napk\napk\nblackjack\n")
	}))
	defer srv.Close()
	old := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = old }()

	c := gingrEnvConfig(t, "ok")
	os.WriteFile(c.GingrMirror, []byte("# comment\n"+srv.URL+"/packages.list "+srv.URL+"/{pkg}.zip\n"), 0o644)
	os.WriteFile(c.GingrList, []byte("hello\nmusl\nlegacy\n"), 0o644) // musl hidden-but-installed, legacy unlisted

	rf := ReposFile{
		Source:     "github",
		Categories: map[string]string{"kwordle": "games"},
		Hidden:     []string{"kpm", "musl", "apk"},
		Packages: map[string]PkgMeta{
			"kwordle":     {Name: "Kwordle", Description: "Wordle clone", Category: "ignored-by-categories"},
			"kindlefetch": {Description: "Download books", Category: "utilities"},
		},
	}
	cat := buildCatalog(c, rf, true)
	if cat.Backend != BackendGingr || cat.ConfigSource != "github" || len(cat.Repos) != 1 || cat.Repos[0].Source != "network" {
		t.Fatalf("header: %+v", cat)
	}
	byID := map[string]CatPackage{}
	var order []string
	for _, p := range cat.Packages {
		byID[p.ID] = p
		order = append(order, p.Name)
	}
	if _, ok := byID["kpm"]; ok {
		t.Error("hidden kpm shown")
	}
	if _, ok := byID["apk"]; ok {
		t.Error("hidden apk shown")
	}
	if p, ok := byID["musl"]; !ok || !p.Installed {
		t.Error("hidden-but-installed package must stay manageable")
	}
	if p := byID["legacy"]; !p.Installed || p.Name != "legacy" {
		t.Errorf("unlisted installed package: %+v", p)
	}
	if p := byID["kwordle"]; p.Name != "Kwordle" || p.Category != "games" || p.Installed || !p.Supported {
		t.Errorf("kwordle: %+v", p)
	}
	if p := byID["kindlefetch"]; p.Category != "utilities" || p.Description != "Download books" {
		t.Errorf("meta category fallback: %+v", p)
	}
	if p := byID["hello"]; !p.Installed || p.Version != "" || p.UpdateAvailable {
		t.Errorf("hello: %+v", p)
	}
	for i := 1; i < len(order); i++ {
		if strings.ToLower(order[i-1]) > strings.ToLower(order[i]) {
			t.Errorf("not sorted: %v", order)
			break
		}
	}

	// Offline: served from cache.
	httpClient = &http.Client{}
	cat2 := buildCatalog(c, rf, false)
	if cat2.Repos[0].Source != "cache" || len(cat2.Packages) != len(cat.Packages) {
		t.Errorf("cache fallback: %+v", cat2.Repos)
	}
}

func TestMirrorsDefault(t *testing.T) {
	c := gingrEnvConfig(t, "ok") // mirrors.conf does not exist
	if m := gingrMirrors(c); len(m) != 1 || !strings.HasPrefix(m[0], "https://") {
		t.Errorf("default mirror: %v", m)
	}
	os.WriteFile(c.GingrMirror, []byte("http://insecure/list http://x/{pkg}.zip\n"), 0o644)
	if m := gingrMirrors(c); len(m) != 1 || strings.HasPrefix(m[0], "http://") {
		t.Errorf("insecure mirror accepted: %v", m)
	}
}

func TestPackageMetaMerge(t *testing.T) {
	srv := configServer(t, `{"repos":[{"id":"a","url":"https://e.org/a.json"}],
	  "packages":{"x":{"name":"X","description":"from github"},"y":{"name":"Y"}},
	  "hidden":["lib1","lib2"]}`, 200)
	c := testEnv(t, "1.0.0", nil)
	c.ConfigURL = srv.URL
	os.WriteFile(c.path("repos.json"), []byte(`{"repos":[],"categories":{},
	  "packages":{"x":{"name":"X mine","description":"override"}}, "show":["lib2"]}`), 0o644)
	rf := loadRepos(c, true)
	if rf.Packages["x"].Description != "override" || rf.Packages["y"].Name != "Y" {
		t.Errorf("packages: %+v", rf.Packages)
	}
	if strings.Join(rf.Hidden, ",") != "lib1" {
		t.Errorf("hidden = %v (lib2 was un-hidden by the user)", rf.Hidden)
	}
}

func TestKeepSyncSources(t *testing.T) {
	c := gingrEnvConfig(t, "ok")
	prev := Catalog{GeneratedAt: "2026-01-01T00:00:00Z", ConfigSource: "github",
		Repos: []RepoStatus{{ID: "kpm-mirror-1", Source: "network"}, {ID: "gone", Source: "network"}}}
	data, _ := json.Marshal(prev)
	os.WriteFile(c.path("catalog.json"), data, 0o644)

	cat := Catalog{ConfigSource: "cache", GeneratedAt: "now",
		Repos: []RepoStatus{{ID: "kpm-mirror-1", Source: "cache"}, {ID: "new", Source: "cache"}, {ID: "kpm-mirror-2", Source: "unavailable"}}}
	keepSyncSources(c, &cat)
	if cat.ConfigSource != "github" || cat.GeneratedAt != prev.GeneratedAt {
		t.Errorf("header not preserved: %+v", cat)
	}
	if cat.Repos[0].Source != "network" || cat.Repos[1].Source != "cache" || cat.Repos[2].Source != "unavailable" {
		t.Errorf("repos: %+v", cat.Repos)
	}
}
