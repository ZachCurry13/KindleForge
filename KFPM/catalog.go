package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ---- versions -------------------------------------------------------------

type Version [3]int

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func (v Version) Cmp(o Version) int {
	for i := range v {
		if v[i] != o[i] {
			if v[i] < o[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ---- KPM manifest (manifest_version 2) -----------------------------------

type Manifest struct {
	ManifestVersion int                        `json:"manifest_version"`
	ID              string                     `json:"id"`
	Name            string                     `json:"name"`
	Description     string                     `json:"description"`
	Packages        map[string]ManifestPackage `json:"packages"`
}

type ManifestPackage struct {
	Name        string     `json:"name"`
	Author      string     `json:"author"`
	Description string     `json:"description"`
	Artifacts   []Artifact `json:"artifacts"`
}

type Artifact struct {
	URL       string            `json:"url"`
	Version   Version           `json:"version"`
	Platforms []string          `json:"supported_platforms"`
	Deps      []json.RawMessage `json:"dependencies"`
}

func (a Artifact) supports(platform string) bool {
	for _, p := range a.Platforms {
		if p == platform {
			return true
		}
	}
	return false
}

// ---- catalog (what the frontend reads) -----------------------------------

type CatPackage struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Author           string   `json:"author"`
	Description      string   `json:"description"`
	Repo             string   `json:"repo"`
	Category         string   `json:"category"`
	Version          string   `json:"version"`   // newest version for this device ("" if none)
	Supported        bool     `json:"supported"` // has an artifact for this device's platform
	Installed        bool     `json:"installed"`
	InstalledVersion string   `json:"installedVersion"`
	UpdateAvailable  bool     `json:"updateAvailable"`
	Dependencies     []string `json:"dependencies"`
	Rating           float64  `json:"rating,omitempty"` // average, only once enough votes exist
	RatingCount      int      `json:"ratingCount,omitempty"`
	MyRating         int      `json:"myRating,omitempty"`
}

type RepoStatus struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Source   string `json:"source"` // "network" | "cache" | "unavailable"
	Packages int    `json:"packages"`
}

type Catalog struct {
	GeneratedAt    string       `json:"generatedAt"`
	ConfigSource   string       `json:"configSource"` // github | cache | built-in
	Backend        string       `json:"backend"`      // modding | gingr
	RatingsEnabled bool         `json:"ratingsEnabled"`
	Platform       string       `json:"platform"`
	ABI            string       `json:"abi"`
	Repos          []RepoStatus `json:"repos"`
	Packages       []CatPackage `json:"packages"`
}

// ---- fetching with cache --------------------------------------------------

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func cachePath(c Config, r Repo) string {
	name := unsafeName.ReplaceAllString(r.ID, "_")
	if name == "" {
		name = fmt.Sprintf("%x", sha1.Sum([]byte(r.URL)))[:12]
	}
	return c.path("cache", name+".json")
}

func parseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.ManifestVersion == 0 || m.Packages == nil {
		return m, fmt.Errorf("not a KPM manifest")
	}
	return m, nil
}

// loadManifest fetches a repo manifest (when online is true) and refreshes the
// cache; on any failure it falls back to the cached copy.
func loadManifest(c Config, r Repo, online bool) (Manifest, string) {
	cp := cachePath(c, r)
	if online {
		resp, err := httpClient.Get(r.URL)
		if err == nil {
			data, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == http.StatusOK {
				if m, perr := parseManifest(data); perr == nil {
					_ = os.MkdirAll(filepath.Dir(cp), 0o755)
					_ = writeAtomic(cp, data)
					return m, "network"
				} else {
					logf("%s: bad manifest: %v", r.ID, perr)
				}
			} else if rerr == nil {
				logf("%s: HTTP %d", r.ID, resp.StatusCode)
			}
		} else {
			logf("%s: fetch failed: %v", r.ID, err)
		}
	}
	if data, err := os.ReadFile(cp); err == nil {
		if m, err := parseManifest(data); err == nil {
			return m, "cache"
		}
	}
	return Manifest{}, "unavailable"
}

// ---- building the catalog -------------------------------------------------

func buildCatalog(c Config, rf ReposFile, online bool) Catalog {
	if isGingr(c) {
		return buildGingrCatalog(c, rf, online)
	}
	hidden := map[string]bool{}
	for _, id := range rf.Hidden {
		hidden[id] = true
	}
	cat := Catalog{
		Backend:      BackendModding,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Platform:     c.Platform,
		ConfigSource: rf.Source,
		ABI:          detectABI(),
		Packages:     []CatPackage{},
		Repos:        []RepoStatus{},
	}
	installed := listInstalled(c)
	seen := map[string]bool{} // first repo listed wins on duplicate ids

	for _, r := range rf.Repos {
		m, src := loadManifest(c, r, online)
		cat.Repos = append(cat.Repos, RepoStatus{ID: r.ID, URL: r.URL, Source: src, Packages: len(m.Packages)})

		ids := make([]string, 0, len(m.Packages))
		for id := range m.Packages {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, isInst := installed[id]; hidden[id] && !isInst {
				continue
			}
			p := m.Packages[id]
			cp := CatPackage{
				ID: id, Name: p.Name, Author: p.Author, Description: p.Description,
				Repo: r.ID, Category: rf.Categories[id], Dependencies: []string{},
			}
			if cp.Name == "" {
				cp.Name = id
			}

			var best *Artifact
			for i := range p.Artifacts {
				a := &p.Artifacts[i]
				if a.supports(c.Platform) && (best == nil || a.Version.Cmp(best.Version) > 0) {
					best = a
				}
			}
			if best != nil {
				cp.Supported = true
				cp.Version = best.Version.String()
				cp.Dependencies = depIDs(best.Deps)
			}
			if in, ok := installed[id]; ok {
				cp.Installed = true
				cp.InstalledVersion = in.Version.String()
				cp.UpdateAvailable = best != nil && best.Version.Cmp(in.Version) > 0
			}
			cat.Packages = append(cat.Packages, cp)
		}
	}

	// Packages installed locally / from repos we no longer know about.
	var extra []string
	for id := range installed {
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		in := installed[id]
		name := in.Name
		if name == "" {
			name = id
		}
		cat.Packages = append(cat.Packages, CatPackage{
			ID: id, Name: name, Repo: in.Repository, Category: rf.Categories[id],
			Supported: true, Installed: true, InstalledVersion: in.Version.String(),
			Dependencies: []string{},
		})
	}
	return cat
}

// depIDs pulls the dependency package ids out of an artifact. The manifest
// schema allows either bare id strings or {"id": ...} objects.
func depIDs(raw []json.RawMessage) []string {
	ids := []string{}
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil && s != "" {
			ids = append(ids, s)
			continue
		}
		var o struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(r, &o) == nil && o.ID != "" {
			ids = append(ids, o.ID)
		}
	}
	return ids
}

func writeCatalog(c Config, cat Catalog) error {
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	return writeAtomic(c.path("catalog.json"), data)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func printCatalog(cat Catalog) {
	fmt.Printf("Platform: %s   Repos: ", cat.Platform)
	for _, r := range cat.Repos {
		fmt.Printf("%s(%s) ", r.ID, r.Source)
	}
	fmt.Println()
	for i, p := range cat.Packages {
		state := ""
		switch {
		case !p.Supported:
			state = " [not available for this device]"
		case p.UpdateAvailable:
			state = fmt.Sprintf(" [installed %s, update -> %s]", p.InstalledVersion, p.Version)
		case p.Installed:
			state = fmt.Sprintf(" [installed %s]", p.InstalledVersion)
		}
		fmt.Printf("%d. %s (%s) %s by %s%s\n", i+1, p.Name, p.ID, p.Version, p.Author, state)
		if p.Description != "" {
			fmt.Printf("    %s\n", strings.TrimSpace(p.Description))
		}
	}
}
