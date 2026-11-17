package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestValidateSourceURL(t *testing.T) {
	good := []string{"https://example.org/repo/manifest.json", "https://user.github.io/Repo/manifest.json", "https://raw.githubusercontent.com/a/b/main/manifest.json"}
	for _, u := range good {
		if _, err := validateSourceURL(u); err != nil {
			t.Errorf("%s rejected: %v", u, err)
		}
	}
	bad := map[string]string{
		"http":        "http://example.org/manifest.json",
		"localhost":   "https://localhost/manifest.json",
		"ip":          "https://192.168.1.5/manifest.json",
		"ipv6":        "https://[::1]/manifest.json",
		"local name":  "https://printer.local/manifest.json",
		"no dot host": "https://intranet/manifest.json",
		"userinfo":    "https://me:pw@example.org/manifest.json",
		"space":       "https://example.org/a b.json",
		"semicolon":   "https://example.org/a.json;rm -rf /",
		"dollar":      "https://example.org/$(id).json",
		"backtick":    "https://example.org/`id`.json",
		"ampersand":   "https://example.org/a.json&echo",
		"quote":       "https://example.org/a'.json",
		"pipe":        "https://example.org/a|b.json",
		"unicode":     "https://exämple.org/a.json",
		"empty":       "",
		"short":       "https://a.b",
		"too long":    "https://example.org/" + strings.Repeat("a", 400),
		"javascript":  "javascript:alert(1)",
		"file":        "file:///etc/passwd",
	}
	for name, u := range bad {
		if _, err := validateSourceURL(u); err == nil {
			t.Errorf("%s accepted: %q", name, u)
		}
	}
}

func dirConfig(t *testing.T, base string) Config {
	c := gingrEnvConfig(t, "ok")
	c.UserRoot = t.TempDir()
	os.MkdirAll(c.path("cache"), 0o755)
	os.WriteFile(c.path("cache", "config.json"), []byte(base), 0o644)
	return c
}

const dirBase = `{
 "repos":[{"id":"main","url":"https://example.org/main/manifest.json"}],
 "sources":[
   {"id":"opt","name":"Optional","url":"https://example.org/opt/manifest.json","format":"kpm","trust":"community","note":"unreviewed"},
   {"id":"zen","name":"Zen","url":"https://example.org/zen/manifest.json","format":"zen"},
   {"id":"ondef","name":"On by default","url":"https://example.org/def/manifest.json","default":true},
   {"id":"bad id!","url":"https://example.org/x"},
   {"id":"http","url":"http://example.org/x"},
   {"id":"weird","url":"https://example.org/w","format":"nope"}
 ]}`

func dirState(rf ReposFile) map[string]SourceInfo {
	m := map[string]SourceInfo{}
	for _, s := range rf.Directory {
		m[s.ID] = s
	}
	return m
}

func TestDirectoryDefaultsAndTrust(t *testing.T) {
	c := dirConfig(t, dirBase)
	rf := loadRepos(c, false)
	d := dirState(rf)
	if len(d) != 4 {
		t.Fatalf("malformed entries must be dropped, got %v", d)
	}
	if !d["main"].Enabled || d["main"].Trust != "reviewed" {
		t.Errorf("main: %+v", d["main"])
	}
	if d["opt"].Enabled || d["opt"].Trust != "community" || d["zen"].Enabled {
		t.Errorf("optional sources must be OFF by default: %+v %+v", d["opt"], d["zen"])
	}
	if !d["ondef"].Enabled {
		t.Error("a default:true directory source is on")
	}
	if d["zen"].Format != "zen" || d["opt"].Format != "kpm" {
		t.Errorf("formats: %+v %+v", d["zen"], d["opt"])
	}
	ids := []string{}
	for _, r := range rf.Repos {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "main,ondef" {
		t.Errorf("fetched repos: %v", ids)
	}
}

func TestEnableDisableAndUserSources(t *testing.T) {
	c := dirConfig(t, dirBase)
	if err := setSourceEnabled(c, "opt", true); err != nil {
		t.Fatal(err)
	}
	if !dirState(loadRepos(c, false))["opt"].Enabled {
		t.Error("enable did not stick")
	}
	setSourceEnabled(c, "opt", false)
	if dirState(loadRepos(c, false))["opt"].Enabled {
		t.Error("disable did not stick")
	}
	// a default-on source can be switched off and on again
	setSourceEnabled(c, "main", false)
	if dirState(loadRepos(c, false))["main"].Enabled {
		t.Error("main should be off")
	}
	setSourceEnabled(c, "main", true)
	if !dirState(loadRepos(c, false))["main"].Enabled {
		t.Error("main should be back on")
	}
	if setSourceEnabled(c, "nope", true) == nil {
		t.Error("unknown source accepted")
	}

	// the user's own repo: trust 'yours', removable; catalog sources are not
	u := readUserFile(c)
	u.Repos = append(u.Repos, Repo{ID: "my-thing", URL: "https://mine.example.org/manifest.json", Name: "Mine"})
	writeUserFile(c, u)
	d := dirState(loadRepos(c, false))
	if m := d["my-thing"]; !m.Enabled || m.Trust != "yours" || !m.User || m.Format != "kpm" {
		t.Errorf("user repo: %+v", m)
	}
	setSourceEnabled(c, "my-thing", false)
	if dirState(loadRepos(c, false))["my-thing"].Enabled {
		t.Error("user repo should switch off")
	}
	if removeUserSource(c, "opt") == nil {
		t.Error("a catalog source must not be removable")
	}
	if err := removeUserSource(c, "my-thing"); err != nil {
		t.Fatal(err)
	}
	if _, still := dirState(loadRepos(c, false))["my-thing"]; still {
		t.Error("remove did not remove")
	}
}

func TestUserRepoCannotShadowACatalogSource(t *testing.T) {
	c := dirConfig(t, dirBase)
	u := readUserFile(c)
	u.Repos = []Repo{{ID: "main", URL: "https://evil.example.org/manifest.json"}, {ID: "bad id", URL: "https://x.example.org/m.json"}, {ID: "plain", URL: "http://x.example.org/m.json"}}
	writeUserFile(c, u)
	rf := loadRepos(c, false)
	for _, r := range rf.Repos {
		if strings.Contains(r.URL, "evil") || strings.Contains(r.URL, "x.example") {
			t.Errorf("a user entry got through: %+v", r)
		}
	}
}

func repoFixtureServer(t *testing.T) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/kpm.json":
			fmt.Fprint(w, `{"manifest_version":2,"id":"kpmrepo","name":"A KPM Repo","packages":{"a":{"name":"Alpha","artifacts":[]},"b":{"name":"Beta","artifacts":[]}}}`)
		case "/zen.json":
			fmt.Fprint(w, `{"schema_version":"1","repo":{"id":"ZRepo","name":"A Zen Repo"},"packages":[{"id":"p","name":"Plug","version":"1.0"}]}`)
		case "/html":
			fmt.Fprint(w, "<html>hello</html>")
		case "/nopkgs.json":
			fmt.Fprint(w, `{"hello":"world"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := httpClient
	httpClient = srv.Client()
	t.Cleanup(func() { httpClient = old })
	return srv
}

func TestCheckSourceRecognisesFormats(t *testing.T) {
	srv := repoFixtureServer(t)
	c := dirConfig(t, dirBase)
	host := strings.TrimPrefix(srv.URL, "https://")
	_ = host
	// the fixture host is an IP:port, which the validator refuses: test the
	// recognition logic through the swap point used by tests
	old := validateSourceURLHook
	validateSourceURLHook = func(raw string) (*urlT, error) { return parseURLForTest(raw) }
	defer func() { validateSourceURLHook = old }()

	p := checkSource(c, srv.URL+"/kpm.json")
	if !p.OK || p.Format != "kpm" || p.Packages != 2 || p.Name != "A KPM Repo" || len(p.Sample) != 2 {
		t.Errorf("kpm: %+v", p)
	}
	p = checkSource(c, srv.URL+"/zen.json")
	if !p.OK || p.Format != "zen" || p.Packages != 1 || p.Name != "A Zen Repo" {
		t.Errorf("zen: %+v", p)
	}
	for _, path := range []string{"/html", "/nopkgs.json", "/missing"} {
		if p := checkSource(c, srv.URL+path); p.OK || p.Error == "" {
			t.Errorf("%s should not be accepted: %+v", path, p)
		}
	}
	if p := checkSource(c, "http://example.org/x.json"); p.OK {
		t.Error("an invalid address must not even be fetched")
	}
}

func TestAddUserSourceEndToEnd(t *testing.T) {
	srv := repoFixtureServer(t)
	c := dirConfig(t, dirBase)
	validateSourceURLHook = func(raw string) (*urlT, error) { return parseURLForTest(raw) }
	defer func() { validateSourceURLHook = nil }()

	id, err := addUserSource(c, srv.URL+"/kpm.json")
	if err != nil || id != "my-kpmrepo" {
		t.Fatalf("%q %v", id, err)
	}
	if _, err := addUserSource(c, srv.URL+"/kpm.json"); err == nil {
		t.Error("the same repository twice must be refused")
	}
	id2, err := addUserSource(c, srv.URL+"/zen.json")
	if err != nil || id2 != "my-zrepo" {
		t.Fatalf("%q %v", id2, err)
	}
	d := dirState(loadRepos(c, false))
	if !d[id].Enabled || d[id].Trust != "yours" || d[id2].Format != "zen" {
		t.Errorf("%+v %+v", d[id], d[id2])
	}
	if p := readPreview(t, c); p.Name == "" {
		t.Error("a preview file should be written")
	}
	// the user's other settings survive an add
	u := readUserFile(c)
	u.Categories["x"] = "games"
	writeUserFile(c, u)
	addUserSource(c, srv.URL+"/html")
	if readUserFile(c).Categories["x"] != "games" {
		t.Error("editing sources must keep the user's other settings")
	}
}

func readPreview(t *testing.T, c Config) SourcePreview {
	var p SourcePreview
	readJSON(t, c.path("source-preview.json"), &p)
	return p
}

func TestAnnotateSources(t *testing.T) {
	c := dirConfig(t, dirBase)
	rf := loadRepos(c, false)
	cat := Catalog{Repos: []RepoStatus{{ID: "main", Source: "network", Packages: 7}}}
	annotateSources(rf, &cat)
	byID := map[string]SourceInfo{}
	for _, s := range cat.Sources {
		byID[s.ID] = s
	}
	if byID["main"].Packages != 7 || byID["main"].Fetched != "network" || byID["opt"].Fetched != "" {
		t.Errorf("%+v", byID)
	}
	if cat.Sources[0].Trust != "reviewed" || cat.Sources[len(cat.Sources)-1].Trust != "community" {
		t.Errorf("sources should list reviewed first and community last: %v", cat.Sources)
	}
}
