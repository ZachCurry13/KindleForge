package main

// Detecting apps that are on the Kindle but were not installed by KindleForge
// (KOReader, or the KindleTweaks apps copied over by hand), working out their
// version where possible, and saying whether a newer one exists.
//
// What to look for lives in the GitHub catalog definition ("detect" in
// catalog/config.json), so new apps can be recognised without an app update.
// Detection only READS files. Nothing here changes the Kindle.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type DetectEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Paths        []string `json:"paths"`                  // ALL must exist (relative to the user root)
	VersionFile  string   `json:"versionFile,omitempty"`  // file holding the version (relative)
	VersionRegex string   `json:"versionRegex,omitempty"` // first capture group = version
	LatestGitHub string   `json:"latestGitHub,omitempty"` // owner/repo whose latest release is "newest"
	Note         string   `json:"note,omitempty"`
	ShortNote    string   `json:"shortNote,omitempty"` // one short line for the card (the full note shows in Details)
}

var (
	repoSlugRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	defaultVerRe   = regexp.MustCompile(`v?(\d+(?:\.\d+)+)`)
	numberRe       = regexp.MustCompile(`\d+`)
	maxVersionFile = 4096
)

func safeRel(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.Contains(p, "\\") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func validDetect(e DetectEntry) bool {
	if !validID.MatchString(e.ID) || e.Name == "" || len(e.Paths) == 0 {
		return false
	}
	for _, p := range e.Paths {
		if !safeRel(p) {
			return false
		}
	}
	if e.VersionFile != "" && !safeRel(e.VersionFile) {
		return false
	}
	if e.VersionRegex != "" {
		if len(e.VersionRegex) > 200 {
			return false
		}
		if _, err := regexp.Compile(e.VersionRegex); err != nil {
			return false
		}
	}
	return e.LatestGitHub == "" || repoSlugRe.MatchString(e.LatestGitHub)
}

type Found struct {
	Entry   DetectEntry
	Version string // "" when it cannot be read
}

// scanDetected returns the known apps whose files are all present.
func scanDetected(c Config, entries []DetectEntry) []Found {
	var out []Found
	for _, e := range entries {
		if !validDetect(e) {
			logf("ignoring invalid detect entry %q", e.ID)
			continue
		}
		all := true
		for _, p := range e.Paths {
			full := filepath.Join(c.UserRoot, filepath.FromSlash(p))
			if st, err := os.Lstat(full); err != nil || st.Mode()&os.ModeSymlink != 0 {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		out = append(out, Found{Entry: e, Version: readDetectedVersion(c, e)})
	}
	return out
}

func readDetectedVersion(c Config, e DetectEntry) string {
	if e.VersionFile == "" {
		return ""
	}
	// Resolve links first: the file must really live inside the user area.
	root, err := filepath.EvalSymlinks(c.UserRoot)
	if err != nil {
		return ""
	}
	full, err := filepath.EvalSymlinks(filepath.Join(c.UserRoot, filepath.FromSlash(e.VersionFile)))
	if err != nil || !within(root, full) {
		return ""
	}
	f, err := os.Open(full)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, int64(maxVersionFile)))
	re := defaultVerRe
	if e.VersionRegex != "" {
		re = regexp.MustCompile(e.VersionRegex)
	}
	if m := re.FindStringSubmatch(string(data)); len(m) > 1 {
		return m[1]
	}
	return ""
}

// compareVersions compares dotted numbers ("2026.07.1" vs "v2026.07"): numeric
// parts in order, missing parts count as 0.
func compareVersions(a, b string) int {
	pa, pb := numberRe.FindAllString(a, -1), numberRe.FindAllString(b, -1)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ---- newest versions (GitHub releases), cached ------------------------------

type latestRecord struct {
	Tag string `json:"tag"`
	At  string `json:"at"`
}

func loadLatest(c Config, entries []DetectEntry, online bool) map[string]string {
	cp := c.path("cache", "latest.json")
	cache := map[string]latestRecord{}
	if data, err := os.ReadFile(cp); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	if online {
		changed := false
		for _, e := range entries {
			if !validDetect(e) || e.LatestGitHub == "" {
				continue
			}
			req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+e.LatestGitHub+"/releases/latest", nil)
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("User-Agent", "KindleForge-KFPM")
			resp, err := httpClient.Do(req)
			if err != nil {
				continue
			}
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if rerr != nil || resp.StatusCode != http.StatusOK {
				continue
			}
			var rel struct {
				Tag string `json:"tag_name"`
			}
			if json.Unmarshal(data, &rel) == nil && rel.Tag != "" && len(rel.Tag) < 64 {
				cache[e.ID] = latestRecord{Tag: rel.Tag, At: time.Now().UTC().Format(time.RFC3339)}
				changed = true
			}
		}
		if changed {
			if data, err := json.MarshalIndent(cache, "", "  "); err == nil {
				_ = os.MkdirAll(filepath.Dir(cp), 0o755)
				_ = writeAtomic(cp, data)
			}
		}
	}
	out := map[string]string{}
	for id, r := range cache {
		out[id] = r.Tag
	}
	return out
}

// applyDetected merges what was found into the catalog.
//   - A found app that the catalog can install (KUALA, ...) is marked
//     Detected; the page then offers "Take over", which installs the
//     catalog version over the hand-made copy, exactly like an update.
//   - Any other found app is listed read-only, with its version and whether a
//     newer one exists.
func applyDetected(c Config, rf ReposFile, online bool, cat *Catalog) {
	if len(rf.Detect) == 0 {
		return
	}
	found := scanDetected(c, rf.Detect)
	if len(found) == 0 {
		return
	}
	latest := loadLatest(c, rf.Detect, online)
	for _, f := range found {
		e := f.Entry
		idx := -1
		for i := range cat.Packages {
			if cat.Packages[i].ID == e.ID {
				idx = i
				break
			}
		}
		if idx >= 0 {
			p := &cat.Packages[idx]
			if !p.Installed { // present on disk but not managed by KindleForge
				p.Detected = true
				p.Note = e.Note
				p.ShortNote = e.ShortNote
			}
			continue
		}
		p := CatPackage{
			ID: e.ID, Name: e.Name, Description: e.Description, Repo: "found on your Kindle",
			Supported: true, Installed: true, InstalledVersion: f.Version, Detected: true,
			ReadOnly: true, Note: e.Note, ShortNote: e.ShortNote, Dependencies: []string{}, Category: rf.Categories[e.ID],
		}
		if t := latest[e.ID]; t != "" {
			p.Version = strings.TrimPrefix(t, "v")
			p.UpdateAvailable = f.Version != "" && compareVersions(t, f.Version) > 0
		}
		cat.Packages = append(cat.Packages, p)
	}
}
