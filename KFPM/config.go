package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// Config holds every path/tunable KFPM uses. Each can be overridden through an
// environment variable so the tool can be exercised on a desktop machine.
type Config struct {
	DataDir  string // KFPM_DATA_DIR : repos.json, catalog.json, cache/, last.log
	KPMPath  string // KFPM_KPM      : kpm executable
	DBPath   string // KFPM_DB       : KPM sqlite database
	PkgDir   string // KFPM_PKG_DIR  : KPM package folder (fallback for installed state)
	Platform string // KFPM_PLATFORM : kindlehf | kindlepw2 | ...
}

const (
	defaultDataDir = "/mnt/us/.KFPM"
	defaultDB      = "/mnt/us/kmc/kpm/kpm.db"
	defaultPkgDir  = "/mnt/us/kmc/kpm/packages"
	lipcService    = "xyz.penguins184.kindleforge"
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
		DataDir:  envOr("KFPM_DATA_DIR", defaultDataDir),
		DBPath:   envOr("KFPM_DB", defaultDB),
		PkgDir:   envOr("KFPM_PKG_DIR", defaultPkgDir),
		Platform: envOr("KFPM_PLATFORM", detectPlatform()),
	}
	c.KPMPath = envOr("KFPM_KPM", findKPM())
	return c
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

// ---- repos.json -----------------------------------------------------------

type Repo struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type ReposFile struct {
	Repos []Repo `json:"repos"`
	// Optional package-id -> category map. The manifests carry no category
	// data, so the storefront tabs rely on this.
	Categories map[string]string `json:"categories"`
}

func defaultRepos() ReposFile {
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

// loadRepos reads repos.json, seeding it with the defaults on first run.
func loadRepos(c Config) ReposFile {
	p := c.path("repos.json")
	data, err := os.ReadFile(p)
	if err == nil {
		var rf ReposFile
		if json.Unmarshal(data, &rf) == nil && len(rf.Repos) > 0 {
			return rf
		}
		logf("repos.json is invalid, using defaults")
		return defaultRepos()
	}
	rf := defaultRepos()
	if out, err := json.MarshalIndent(rf, "", "  "); err == nil {
		_ = os.MkdirAll(c.DataDir, 0o755)
		_ = os.WriteFile(p, append(out, '\n'), 0o644)
	}
	return rf
}
