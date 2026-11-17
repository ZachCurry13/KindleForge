package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// Config holds every path/tunable KFPM uses. Each can be overridden through an
// environment variable so the tool can be exercised on a desktop machine.
type Config struct {
	DataDir     string // KFPM_DATA_DIR    : repos.json, catalog.json, cache/, last.log
	KPMPath     string // KFPM_KPM         : kpm executable
	DBPath      string // KFPM_DB          : KPM sqlite database
	PkgDir      string // KFPM_PKG_DIR     : KPM package folder (fallback for installed state)
	Platform    string // KFPM_PLATFORM    : kindlehf | kindlepw2 | ...
	AppDir      string // KFPM_APP_DIR     : installed KindleForge app folder (self-update target)
	ReleasesURL string // KFPM_RELEASES_URL: GitHub releases API endpoint
	ConfigURL   string // KFPM_CONFIG_URL  : remote catalog definition (repos + categories)

	UserRoot string // KFPM_USER_ROOT: the Kindle's user storage (cleanup scope)

	// Which KPM is installed: "modding" (KindleModding/KPM) or "gingr"
	// (gingrspacecadet/kpm). Empty means modding.
	Backend     string // KFPM_BACKEND
	GingrList   string // KFPM_GINGR_LIST   : gingr kpm installed list (packages.list)
	GingrMirror string // KFPM_GINGR_MIRRORS: gingr kpm mirrors.conf
}

const (
	BackendModding = "modding"
	BackendGingr   = "gingr"

	defaultGingrBin     = "/usr/local/bin/kpm"
	defaultGingrList    = "/mnt/us/kpm/packages.list"
	defaultGingrMirrors = "/etc/kpm/mirrors.conf"
)

const (
	defaultDataDir = "/mnt/us/.KFPM"
	defaultDB      = "/mnt/us/kmc/kpm/kpm.db"
	defaultPkgDir  = "/mnt/us/kmc/kpm/packages"
	lipcService    = "xyz.penguins184.kindleforge"

	defaultReleasesURL = "https://api.github.com/repos/ZachCurry13/KindleForge/releases?per_page=10"
	defaultConfigURL   = "https://raw.githubusercontent.com/ZachCurry13/KindleForge/master/catalog/config.json"
)

var kpmCandidates = []string{
	"/var/local/kmc/bin/kpm",
	"/mnt/us/kmc/bin/kpm",
}

var validID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() Config {
	c := Config{
		DataDir:     envOr("KFPM_DATA_DIR", defaultDataDir),
		DBPath:      envOr("KFPM_DB", defaultDB),
		PkgDir:      envOr("KFPM_PKG_DIR", defaultPkgDir),
		Platform:    envOr("KFPM_PLATFORM", detectPlatform()),
		AppDir:      envOr("KFPM_APP_DIR", detectAppDir()),
		ReleasesURL: envOr("KFPM_RELEASES_URL", defaultReleasesURL),
		ConfigURL:   envOr("KFPM_CONFIG_URL", defaultConfigURL),
		UserRoot:    envOr("KFPM_USER_ROOT", "/mnt/us"),
	}
	c.GingrList = envOr("KFPM_GINGR_LIST", defaultGingrList)
	c.GingrMirror = envOr("KFPM_GINGR_MIRRORS", defaultGingrMirrors)
	c.Backend = detectBackend()
	if c.Backend == BackendGingr {
		c.KPMPath = envOr("KFPM_KPM", findGingrKPM())
	} else {
		c.KPMPath = envOr("KFPM_KPM", findKPM())
	}
	return c
}

func isGingr(c Config) bool { return c.Backend == BackendGingr }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// detectBackend picks the installed KPM. KindleModding's wins if both are
// present (richer metadata); gingrspacecadet's is chosen when only its files
// exist. KFPM_BACKEND forces a choice.
func detectBackend() string {
	if v := os.Getenv("KFPM_BACKEND"); v == BackendModding || v == BackendGingr {
		return v
	}
	for _, p := range kpmCandidates {
		if exists(p) {
			return BackendModding
		}
	}
	if exists(defaultDB) {
		return BackendModding
	}
	for _, p := range []string{defaultGingrBin, defaultGingrList, "/etc/kpm/kpm.conf"} {
		if exists(p) {
			return BackendGingr
		}
	}
	return BackendModding
}

func findGingrKPM() string {
	if exists(defaultGingrBin) {
		return defaultGingrBin
	}
	if p, err := exec.LookPath("kpm"); err == nil {
		return p
	}
	return defaultGingrBin
}

// detectAppDir finds the installed app folder: KFPM lives in <app>/binaries/.
func detectAppDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	if filepath.Base(dir) != "binaries" {
		return ""
	}
	return filepath.Dir(dir)
}

func findKPM() string {
	for _, p := range kpmCandidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("kpm"); err == nil {
		return p
	}
	return kpmCandidates[0] // let the exec error explain what is missing
}

// detectPlatform maps the device ABI onto KPM's platform names.
func detectPlatform() string {
	if detectABI() == "hf" {
		return "kindlehf"
	}
	return "kindlepw2"
}

// detectABI keeps the "hf"/"sf" vocabulary the frontend already understands.
func detectABI() string {
	if _, err := os.Stat("/lib/ld-linux-armhf.so.3"); err == nil {
		return "hf"
	}
	return "sf"
}

func (c Config) path(parts ...string) string {
	return filepath.Join(append([]string{c.DataDir}, parts...)...)
}

// ---- catalog definition: repos + categories -------------------------------
//
// Two layers are merged:
//
//  1. The *catalog definition*, published in the GitHub repo at
//     catalog/config.json. It is fetched on every Refresh and cached, so the
//     list of repositories and the category map can change WITHOUT shipping a
//     new app version. If GitHub is unreachable the cached copy is used, and
//     if there is none, the built-in defaults below.
//  2. The user's own /mnt/us/.KFPM/repos.json: extra repos, category
//     overrides, and repo ids to disable. KFPM never overwrites this file.

type Repo struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type ReposFile struct {
	Repos []Repo `json:"repos"`
	// package-id -> category. Manifests carry no category data, so the
	// storefront tabs rely on this.
	Categories map[string]string `json:"categories"`
	// Base URL of the ratings endpoint (https). Empty disables rating.
	RatingsURL string `json:"ratingsURL,omitempty"`

	// User file only: ids of catalog repos to hide.
	Disable []string `json:"disable,omitempty"`

	// Per-package display data. Needed for KPMs whose repositories carry only
	// package names (gingrspacecadet/kpm); fills gaps for the others.
	Packages map[string]PkgMeta `json:"packages,omitempty"`
	// Package ids to hide from the store (libraries, toolchains, ...).
	Hidden []string `json:"hidden,omitempty"`
	// User file only: ids to show even though the catalog hides them.
	Show []string `json:"show,omitempty"`

	Source string `json:"-"` // where the catalog definition came from
}

type PkgMeta struct {
	Name        string `json:"name,omitempty"`
	Author      string `json:"author,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

func builtinConfig() ReposFile {
	return ReposFile{
		Repos: []Repo{{ID: "kindletweaks", URL: "https://kindletweaks.github.io/Repo/manifest.json"}},
		Categories: map[string]string{
			"kuala":      "utilities",
			"gargoyle":   "games",
			"kinamp":     "media",
			"larkplayer": "media",
			"ranki":      "readers",
		},
	}
}

// parseConfig validates a catalog definition. Repo URLs must be https.
func parseConfig(data []byte) (ReposFile, error) {
	var rf ReposFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return rf, err
	}
	if len(rf.Repos) == 0 {
		return rf, fmt.Errorf("no repos listed")
	}
	for _, r := range rf.Repos {
		if r.ID == "" || !strings.HasPrefix(r.URL, "https://") {
			return rf, fmt.Errorf("bad repo entry %q (id required, https url required)", r.ID)
		}
	}
	return rf, nil
}

// loadCatalogDefinition returns the catalog definition, refreshing it from
// GitHub when online is true.
func loadCatalogDefinition(c Config, online bool) ReposFile {
	cp := c.path("cache", "config.json")
	if online && c.ConfigURL != "" {
		client := &http.Client{Timeout: 20 * time.Second}
		resp, err := client.Get(c.ConfigURL)
		if err == nil {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == http.StatusOK {
				if rf, perr := parseConfig(data); perr == nil {
					_ = os.MkdirAll(filepath.Dir(cp), 0o755)
					_ = writeAtomic(cp, data)
					rf.Source = "github"
					return rf
				} else {
					logf("remote catalog config rejected: %v", perr)
				}
			} else if rerr == nil {
				logf("remote catalog config: HTTP %d", resp.StatusCode)
			}
		} else {
			logf("remote catalog config fetch failed: %v", err)
		}
	}
	if data, err := os.ReadFile(cp); err == nil {
		if rf, err := parseConfig(data); err == nil {
			rf.Source = "cache"
			return rf
		}
	}
	rf := builtinConfig()
	rf.Source = "built-in"
	return rf
}

// loadUserFile reads the user's repos.json, creating an empty one on first
// run. The old alpha seeded this file with the full default list; an
// unmodified copy of that seed is ignored so it cannot mask catalog updates.
func loadUserFile(c Config) ReposFile {
	p := c.path("repos.json")
	data, err := os.ReadFile(p)
	if err != nil {
		empty := ReposFile{Repos: []Repo{}, Categories: map[string]string{}}
		if out, err := json.MarshalIndent(empty, "", "  "); err == nil {
			_ = os.MkdirAll(c.DataDir, 0o755)
			_ = os.WriteFile(p, append(out, '\n'), 0o644)
		}
		return ReposFile{}
	}
	var rf ReposFile
	if err := json.Unmarshal(data, &rf); err != nil {
		logf("repos.json is invalid (%v); ignoring it", err)
		return ReposFile{}
	}
	seed := builtinConfig()
	if reflect.DeepEqual(rf.Repos, seed.Repos) && reflect.DeepEqual(rf.Categories, seed.Categories) {
		return ReposFile{}
	}
	return rf
}

// loadRepos returns the effective repos + categories: catalog definition,
// plus the user's additions, minus anything the user disabled.
func loadRepos(c Config, online bool) ReposFile {
	base := loadCatalogDefinition(c, online)
	user := loadUserFile(c)

	disabled := map[string]bool{}
	for _, id := range user.Disable {
		disabled[id] = true
	}

	out := ReposFile{Source: base.Source, RatingsURL: base.RatingsURL, Categories: map[string]string{}, Packages: map[string]PkgMeta{}}
	if v := os.Getenv("KFPM_RATINGS_ENDPOINT"); v != "" {
		out.RatingsURL = v
	}
	seenURL := map[string]bool{}
	add := func(r Repo) {
		if r.ID == "" || r.URL == "" || disabled[r.ID] || seenURL[r.URL] {
			return
		}
		seenURL[r.URL] = true
		out.Repos = append(out.Repos, r)
	}
	for _, r := range base.Repos {
		add(r)
	}
	for _, r := range user.Repos {
		add(r)
	}
	for k, v := range base.Categories {
		out.Categories[k] = v
	}
	for k, v := range user.Categories {
		out.Categories[k] = v
	}
	for k, v := range base.Packages {
		out.Packages[k] = v
	}
	for k, v := range user.Packages {
		out.Packages[k] = v
	}
	shown := map[string]bool{}
	for _, id := range user.Show {
		shown[id] = true
	}
	for _, id := range base.Hidden {
		if !shown[id] {
			out.Hidden = append(out.Hidden, id)
		}
	}
	return out
}
