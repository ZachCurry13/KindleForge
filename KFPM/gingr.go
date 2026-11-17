package main

// Support for gingrspacecadet/kpm, the pacman-style KPM:
//
//	kpm -S <pkg>   install      kpm -R <pkg>   remove
//	kpm -Ql        list local   kpm -Qr        list remote
//
// It keeps installed ids in a plain packages.list, downloads zips named by a
// mirror template, and its repositories are just lists of package names, so
// display data (descriptions, categories, hidden infrastructure packages)
// comes from the GitHub-hosted catalog definition instead.

import (
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// httpClient fetches the mirror lists (swapped out in tests).
var httpClient = &http.Client{Timeout: 20 * time.Second}

const defaultGingrListURL = "https://raw.githubusercontent.com/gingrspacecadet/kpm/refs/heads/main/packages.list"

// gingrEnv gives kpm a sane PATH. Run from Utild the PATH can be minimal, and
// kpm shells out to `kpm -Ql` itself after installing (it crashes if that
// lookup fails).
func gingrEnv() []string {
	path := "/usr/local/bin:/mnt/us/kpm/packages/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	if p := os.Getenv("PATH"); p != "" {
		path += ":" + p
	}
	env := []string{"PATH=" + path, "HOME=" + envOr("HOME", "/tmp")}
	return env
}

func runGingr(c Config, verbose bool, args ...string) (string, error) {
	return runBin(c, verbose, gingrEnv(), args...)
}

// gingrInstalled reads packages.list. Versions are not tracked by this KPM.
func gingrInstalled(c Config) map[string]Installed {
	out := map[string]Installed{}
	data, err := os.ReadFile(c.GingrList)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		id := strings.TrimSpace(line)
		if validID.MatchString(id) {
			out[id] = Installed{ID: id, Name: id}
		}
	}
	return out
}

// gingrPackageOp runs -S / -R and then checks packages.list, because this
// KPM's exit status is not fully reliable (it can crash after a successful
// install). The list is the source of truth.
func gingrPackageOp(c Config, action, id string, verbose bool) error {
	flag, want := "-S", true
	if action == "uninstall" {
		flag, want = "-R", false
	}
	_, err := runGingr(c, verbose, flag, id)
	_, now := gingrInstalled(c)[id]
	switch {
	case err == nil && now == want:
		return nil
	case err != nil && now == want:
		appendLog(c, fmt.Sprintf("kpm exited with an error (%v) but %s is now %s; treating as success", err, id, map[bool]string{true: "installed", false: "removed"}[want]))
		return nil
	case err == nil:
		return fmt.Errorf("kpm finished but %s is still %s", id, map[bool]string{true: "not installed", false: "installed"}[want])
	}
	return err
}

// gingrMirrors returns the package-list URLs from mirrors.conf (first field of
// each non-comment line), or the project's default list.
func gingrMirrors(c Config) []string {
	var urls []string
	if data, err := os.ReadFile(c.GingrMirror); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 1 && !strings.HasPrefix(f[0], "#") && strings.HasPrefix(f[0], "https://") {
				urls = append(urls, f[0])
			}
		}
	}
	if len(urls) == 0 {
		urls = []string{defaultGingrListURL}
	}
	return urls
}

func parseNameList(data []byte) []string {
	var ids []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		id := strings.TrimSpace(line)
		if validID.MatchString(id) && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// fetchNameList downloads one mirror list (when online) with a cache fallback.
func fetchNameList(c Config, url string, online bool) ([]string, string) {
	sum := sha1.Sum([]byte(url))
	cp := c.path("cache", fmt.Sprintf("gingr-%x.list", sum[:6]))
	if online {
		if resp, err := httpClient.Get(url); err == nil {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == http.StatusOK {
				if ids := parseNameList(data); len(ids) > 0 {
					_ = os.MkdirAll(filepath.Dir(cp), 0o755)
					_ = writeAtomic(cp, data)
					return ids, "network"
				}
			} else if rerr == nil {
				logf("mirror list %s: HTTP %d", url, resp.StatusCode)
			}
		} else {
			logf("mirror list fetch failed: %v", err)
		}
	}
	if data, err := os.ReadFile(cp); err == nil {
		if ids := parseNameList(data); len(ids) > 0 {
			return ids, "cache"
		}
	}
	return nil, "unavailable"
}

func buildGingrCatalog(c Config, rf ReposFile, online bool) Catalog {
	cat := Catalog{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		ConfigSource: rf.Source,
		Backend:      BackendGingr,
		Platform:     c.Platform,
		ABI:          detectABI(),
		Packages:     []CatPackage{},
		Repos:        []RepoStatus{},
	}
	installed := gingrInstalled(c)
	hidden := map[string]bool{}
	for _, id := range rf.Hidden {
		hidden[id] = true
	}

	seen := map[string]bool{}
	var ids []string
	for i, u := range gingrMirrors(c) {
		list, src := fetchNameList(c, u, online)
		cat.Repos = append(cat.Repos, RepoStatus{ID: fmt.Sprintf("kpm-mirror-%d", i+1), URL: u, Source: src, Packages: len(list)})
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	for id := range installed { // installed but no longer listed: still manageable
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	for _, id := range ids {
		_, isInst := installed[id]
		if hidden[id] && !isInst {
			continue
		}
		m := rf.Packages[id]
		p := CatPackage{
			ID: id, Name: m.Name, Author: m.Author, Description: m.Description,
			Repo: "kpm", Category: rf.Categories[id], Supported: true,
			Installed: isInst, Dependencies: []string{},
		}
		if p.Name == "" {
			p.Name = id
		}
		if p.Category == "" {
			p.Category = m.Category
		}
		cat.Packages = append(cat.Packages, p)
	}
	// Packages from KindleModding-style manifests (installed by KFPM itself).
	// A manifest package wins over a gingr package with the same id.
	srcs, mstat := kpkgSources(c, rf, online)
	cat.Repos = append(cat.Repos, mstat...)
	kinst := loadKpkgInstalled(c)
	kept := cat.Packages[:0]
	for _, p := range cat.Packages {
		if _, dup := srcs[p.ID]; !dup {
			kept = append(kept, p)
		}
	}
	cat.Packages = kept
	for _, id := range sortedKeys(srcs) {
		s := srcs[id]
		rec, isInst := kinst[id]
		if hidden[id] && !isInst {
			continue
		}
		m := rf.Packages[id]
		p := CatPackage{
			ID: id, Name: s.Pkg.Name, Author: s.Pkg.Author, Description: s.Pkg.Description,
			Repo: s.Repo.ID, Category: rf.Categories[id], Dependencies: []string{},
			Installed: isInst,
		}
		if p.Name == "" {
			p.Name = id
		}
		if p.Category == "" {
			p.Category = m.Category
		}
		if s.Art != nil {
			p.Supported = true
			p.Version = s.Art.Version.String()
			p.Dependencies = depIDs(s.Art.Deps)
		}
		if isInst {
			p.InstalledVersion = rec.Version
			p.UpdateAvailable = s.Art != nil && s.Art.Version.Cmp(parseVersionString(rec.Version)) > 0
		}
		cat.Packages = append(cat.Packages, p)
	}
	for _, id := range sortedKeys(kinst) { // installed from a repo we no longer list
		if _, ok := srcs[id]; ok || kinst[id].Kind == "zen" { // plugins are listed by applyZen
			continue
		}
		cat.Packages = append(cat.Packages, CatPackage{
			ID: id, Name: id, Repo: kinst[id].Repo, Category: rf.Categories[id], Supported: true,
			Installed: true, InstalledVersion: kinst[id].Version, Dependencies: []string{},
		})
	}

	sort.SliceStable(cat.Packages, func(i, j int) bool {
		return strings.ToLower(cat.Packages[i].Name) < strings.ToLower(cat.Packages[j].Name)
	})
	return cat
}
