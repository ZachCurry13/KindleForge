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
	"strings"
	"testing"
)

// ---- semver ---------------------------------------------------------------

func TestSemverOrdering(t *testing.T) {
	order := []string{
		"v5.0.0-alpha.1", "v5.0.0-alpha.2", "v5.0.0-alpha.10",
		"v5.0.0-beta.1", "v5.0.0-rc.1", "v5.0.0", "v5.0.1", "v5.1.0", "v6.0.0",
	}
	for i := 0; i < len(order); i++ {
		for j := 0; j < len(order); j++ {
			a, ok1 := parseSemver(order[i])
			b, ok2 := parseSemver(order[j])
			if !ok1 || !ok2 {
				t.Fatalf("parse failed: %s %s", order[i], order[j])
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := a.cmp(b); got != want {
				t.Errorf("cmp(%s,%s)=%d want %d", order[i], order[j], got, want)
			}
		}
	}
	if _, ok := parseSemver("garbage"); ok {
		t.Error("garbage should not parse")
	}
	if !isNewer("v5.0.0-alpha.2", "5.0.0-alpha.1") || isNewer("5.0.0-alpha.1", "5.0.0-alpha.1") || isNewer("4.9.9", "5.0.0") {
		t.Error("isNewer wrong")
	}
	if !isNewer("5.0.0", "0.0.0") {
		t.Error("anything beats the 0.0.0 fallback")
	}
}

// ---- fake GitHub ----------------------------------------------------------

type zipFile struct{ name, body string }

func makeZip(t *testing.T, files []zipFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f.body))
	}
	zw.Close()
	return buf.Bytes()
}

func goodApp(version string) []zipFile {
	return []zipFile{
		{"KindleForge/index.html", "<html>new</html>"},
		{"KindleForge/script.js", "//js"},
		{"KindleForge/style.css", "/*css*/"},
		{"KindleForge/VERSION", version + "\n"},
		{"KindleForge/binaries/KFPM", "binary"},
		{"KindleForge.sh", "#!/bin/sh"}, // outside the app folder: must be ignored
	}
}

// fakeGitHub serves a releases list pointing at zipBytes. digest may be
// overridden to simulate tampering / missing digests.
func fakeGitHub(t *testing.T, tag string, zipBytes []byte, digest string) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(zipBytes)
	if digest == "auto" {
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		rels := []map[string]any{{
			"tag_name": tag, "draft": false, "body": "notes for " + tag,
			"assets": []map[string]any{{
				"name": assetPrefix + tag + ".zip", "size": len(zipBytes),
				"browser_download_url": srv.URL + "/dl.zip", "digest": digest,
			}},
		}}
		json.NewEncoder(w).Encode(rels)
	})
	mux.HandleFunc("/dl.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(zipBytes) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testEnv(t *testing.T, installedVersion string, srv *httptest.Server) Config {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "mesquite", "KindleForge")
	os.MkdirAll(filepath.Join(app, "binaries"), 0o755)
	os.WriteFile(filepath.Join(app, "index.html"), []byte("<html>old</html>"), 0o644)
	os.WriteFile(filepath.Join(app, "VERSION"), []byte(installedVersion+"\n"), 0o644)
	os.WriteFile(filepath.Join(app, "binaries", "KFPM"), []byte("oldbin"), 0o755)
	c := Config{DataDir: filepath.Join(root, "data"), AppDir: app, Platform: "kindlehf"}
	if srv != nil {
		c.ReleasesURL = srv.URL + "/releases"
	}
	os.MkdirAll(c.DataDir, 0o755)
	return c
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertAppUntouched(t *testing.T, c Config) {
	t.Helper()
	if read(t, filepath.Join(c.AppDir, "index.html")) != "<html>old</html>" {
		t.Error("app files were modified")
	}
	for _, leftover := range []string{c.AppDir + ".new", c.AppDir + ".old"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("leftover %s", leftover)
		}
	}
}

// ---- self-update ----------------------------------------------------------

func TestCheckUpdate(t *testing.T) {
	srv := fakeGitHub(t, "v5.0.0-alpha.2", makeZip(t, goodApp("5.0.0-alpha.2")), "auto")
	c := testEnv(t, "5.0.0-alpha.1", srv)
	info, err := checkUpdate(c)
	if err != nil || !info.Available || info.Latest != "5.0.0-alpha.2" || info.Current != "5.0.0-alpha.1" {
		t.Fatalf("got %+v, %v", info, err)
	}
	if !strings.Contains(read(t, c.path("update.json")), `"available": true`) {
		t.Error("update.json not written")
	}
	c2 := testEnv(t, "5.0.0-alpha.2", srv)
	if info, _ := checkUpdate(c2); info.Available {
		t.Error("same version must not be available")
	}
}

func TestApplyUpdateSuccess(t *testing.T) {
	srv := fakeGitHub(t, "v5.0.0-alpha.2", makeZip(t, goodApp("5.0.0-alpha.2")), "auto")
	c := testEnv(t, "5.0.0-alpha.1", srv)
	if err := applyUpdate(c); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(c.AppDir, "index.html")) != "<html>new</html>" {
		t.Error("new files not installed")
	}
	if got := strings.TrimSpace(read(t, filepath.Join(c.AppDir, "VERSION"))); got != "5.0.0-alpha.2" {
		t.Errorf("VERSION=%s", got)
	}
	if st, err := os.Stat(filepath.Join(c.AppDir, "binaries", "KFPM")); err != nil || st.Mode()&0o100 == 0 {
		t.Error("KFPM must be executable")
	}
	if _, err := os.Stat(filepath.Join(c.AppDir, "KindleForge.sh")); err == nil {
		t.Error("launcher must not be copied into the app folder")
	}
	for _, leftover := range []string{c.AppDir + ".new", c.AppDir + ".old", c.path("update")} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("leftover %s", leftover)
		}
	}
	// update.json now reflects the new version and no pending update
	if !strings.Contains(read(t, c.path("update.json")), `"available": false`) {
		t.Error("update.json should report no update after applying")
	}
}

func TestApplyUpdateRejects(t *testing.T) {
	good := makeZip(t, goodApp("5.0.0-alpha.2"))

	t.Run("checksum mismatch", func(t *testing.T) {
		srv := fakeGitHub(t, "v5.0.0-alpha.2", good, "sha256:"+strings.Repeat("0", 64))
		c := testEnv(t, "5.0.0-alpha.1", srv)
		err := applyUpdate(c)
		if err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("want checksum error, got %v", err)
		}
		assertAppUntouched(t, c)
	})
	t.Run("no digest from GitHub", func(t *testing.T) {
		srv := fakeGitHub(t, "v5.0.0-alpha.2", good, "")
		c := testEnv(t, "5.0.0-alpha.1", srv)
		if err := applyUpdate(c); err == nil {
			t.Fatal("must refuse releases without a digest")
		}
		assertAppUntouched(t, c)
	})
	t.Run("already current", func(t *testing.T) {
		srv := fakeGitHub(t, "v5.0.0-alpha.2", good, "auto")
		c := testEnv(t, "5.0.0-alpha.2", srv)
		if err := applyUpdate(c); err == nil {
			t.Fatal("should not reinstall the same version")
		}
		assertAppUntouched(t, c)
	})
	t.Run("zip slip", func(t *testing.T) {
		bad := makeZip(t, append(goodApp("5.0.0-alpha.2"), zipFile{"KindleForge/../../evil", "x"}))
		srv := fakeGitHub(t, "v5.0.0-alpha.2", bad, "auto")
		c := testEnv(t, "5.0.0-alpha.1", srv)
		if err := applyUpdate(c); err == nil {
			t.Fatal("path traversal must be rejected")
		}
		assertAppUntouched(t, c)
		if _, err := os.Stat(filepath.Join(filepath.Dir(c.AppDir), "evil")); err == nil {
			t.Error("file escaped the app folder")
		}
	})
	t.Run("version mismatch inside zip", func(t *testing.T) {
		srv := fakeGitHub(t, "v5.0.0-alpha.2", makeZip(t, goodApp("5.0.0-alpha.1")), "auto")
		c := testEnv(t, "5.0.0-alpha.1", srv)
		if err := applyUpdate(c); err == nil {
			t.Fatal("zip VERSION must match the release tag")
		}
		assertAppUntouched(t, c)
	})
	t.Run("incomplete package", func(t *testing.T) {
		srv := fakeGitHub(t, "v5.0.0-alpha.2", makeZip(t, []zipFile{{"KindleForge/index.html", "x"}, {"KindleForge/VERSION", "5.0.0-alpha.2"}}), "auto")
		c := testEnv(t, "5.0.0-alpha.1", srv)
		if err := applyUpdate(c); err == nil {
			t.Fatal("incomplete package must be rejected")
		}
		assertAppUntouched(t, c)
	})
}

func TestSwapRollback(t *testing.T) {
	c := testEnv(t, "1.0.0", nil)
	err := swapDirs(c.AppDir, c.AppDir+".new") // .new does not exist
	if err == nil {
		t.Fatal("expected failure")
	}
	if read(t, filepath.Join(c.AppDir, "index.html")) != "<html>old</html>" {
		t.Error("old app was not restored")
	}
}

// ---- catalog definition from GitHub --------------------------------------

func configServer(t *testing.T, body string, status int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const remoteCfg = `{"repos":[{"id":"main","url":"https://example.org/a.json"},{"id":"extra","url":"https://example.org/b.json"}],
 "categories":{"foo":"games","bar":"media"}}`

func TestRemoteCatalogDefinition(t *testing.T) {
	srv := configServer(t, remoteCfg, 200)
	c := testEnv(t, "1.0.0", nil)
	c.ConfigURL = srv.URL

	rf := loadRepos(c, true)
	if rf.Source != "github" || len(rf.Repos) != 2 || rf.Categories["foo"] != "games" {
		t.Fatalf("online: %+v", rf)
	}
	// Offline afterwards: served from cache.
	c.ConfigURL = "http://127.0.0.1:1/none"
	rf = loadRepos(c, true)
	if rf.Source != "cache" || len(rf.Repos) != 2 {
		t.Fatalf("cache fallback: %+v", rf)
	}
	// No cache at all: built-in defaults.
	c2 := testEnv(t, "1.0.0", nil)
	c2.ConfigURL = "http://127.0.0.1:1/none"
	if rf := loadRepos(c2, true); rf.Source != "built-in" || len(rf.Repos) != 1 {
		t.Fatalf("builtin: %+v", rf)
	}
}

func TestRemoteConfigRejected(t *testing.T) {
	for name, body := range map[string]string{
		"http repo":  `{"repos":[{"id":"x","url":"http://insecure.example/a.json"}]}`,
		"empty":      `{"repos":[]}`,
		"not json":   `<html>`,
		"missing id": `{"repos":[{"url":"https://example.org/a.json"}]}`,
	} {
		srv := configServer(t, body, 200)
		c := testEnv(t, "1.0.0", nil)
		c.ConfigURL = srv.URL
		if rf := loadRepos(c, true); rf.Source != "built-in" {
			t.Errorf("%s: should fall back to built-in, got %s", name, rf.Source)
		}
	}
}

func TestUserFileMerge(t *testing.T) {
	srv := configServer(t, remoteCfg, 200)
	c := testEnv(t, "1.0.0", nil)
	c.ConfigURL = srv.URL
	user := `{"repos":[{"id":"mine","url":"https://me.example/m.json"},{"id":"dup","url":"https://example.org/a.json"}],
	          "categories":{"foo":"readers"}, "disable":["extra"]}`
	os.WriteFile(c.path("repos.json"), []byte(user), 0o644)

	rf := loadRepos(c, true)
	ids := []string{}
	for _, r := range rf.Repos {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "main,mine" { // extra disabled, dup removed by URL
		t.Errorf("repos = %v", ids)
	}
	if rf.Categories["foo"] != "readers" || rf.Categories["bar"] != "media" {
		t.Errorf("categories = %v", rf.Categories)
	}
}

func TestLegacySeedIgnoredAndUserFileNotOverwritten(t *testing.T) {
	srv := configServer(t, remoteCfg, 200)
	c := testEnv(t, "1.0.0", nil)
	c.ConfigURL = srv.URL
	seed, _ := json.Marshal(builtinConfig())
	os.WriteFile(c.path("repos.json"), seed, 0o644)

	rf := loadRepos(c, true)
	if rf.Categories["foo"] != "games" || len(rf.Repos) != 2 {
		t.Errorf("stale seed masked the remote definition: %+v", rf)
	}

	// A real user file must survive untouched.
	custom := `{"repos":[],"categories":{"zzz":"games"}}`
	os.WriteFile(c.path("repos.json"), []byte(custom), 0o644)
	loadRepos(c, true)
	if read(t, c.path("repos.json")) != custom {
		t.Error("user repos.json was modified")
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
