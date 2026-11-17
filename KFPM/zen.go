package main

// Reading ZenPM-format repositories (the "Repository Manifest Contract v1"
// published by xZenLabs/zen-pm) and installing the KOReader plugins they list.
// This is an independent implementation of the documented data format; none of
// ZenPM's code is used.
//
// What is supported, deliberately narrow and strict:
//   - packages for KOReader only (platform "koreader"), published as a GitHub
//     release asset that is a zip of ONE "<name>.koplugin" folder
//   - the download must be a github.com release URL and must carry a sha256
//     digest in the repository's versions file; it is verified before anything
//     is unpacked
//   - the plugin is unpacked into koreader/plugins/<name>.koplugin and nothing
//     else is touched; no scripts are run
// Everything else in such a repository (patches, fonts, themes, icon packs,
// packages that need other packages, Kindle-native installers) is not offered.

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type zenRepoInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type zenPackage struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Author       string   `json:"author"`
	Category     string   `json:"category"`
	Platforms    []string `json:"platforms"`
	Dependencies []string `json:"dependencies"`
	Source       string   `json:"source"`
	SourceAsset  string   `json:"source_asset"`
	SourceType   string   `json:"source_type"`
	VersionsURL  string   `json:"versions_url"`
}

type zenManifest struct {
	SchemaVersion any          `json:"schema_version"`
	Repo          zenRepoInfo  `json:"repo"`
	Packages      []zenPackage `json:"packages"`
}

func parseZenManifest(data []byte) (zenManifest, error) {
	var m zenManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.SchemaVersion == nil || m.Packages == nil {
		return m, fmt.Errorf("not a ZenPM manifest")
	}
	return m, nil
}

var (
	kopluginAssetRe = regexp.MustCompile(`(?i)koplugin.*\.zip$`)
	moduleRe        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	githubAssetRe   = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases/download/[^/]+/[^/]+$`)
	sha256DigestRe  = regexp.MustCompile(`^sha256:[0-9a-fA-F]{64}$`)

	// seam for tests: which download addresses are acceptable
	zenAssetOK = func(u string) bool { return githubAssetRe.MatchString(u) }
)

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// zenEligible says whether KindleForge offers this package at all.
func zenEligible(p zenPackage) bool {
	return validID.MatchString(p.ID) && p.Name != "" && p.Version != "" &&
		has(p.Platforms, "koreader") && !has(p.Platforms, "kindle") && !has(p.Platforms, "kobo") &&
		p.SourceType == "release" && kopluginAssetRe.MatchString(p.SourceAsset) &&
		len(p.Dependencies) == 0 && p.VersionsURL != ""
}

func koreaderPluginsDir(c Config) string {
	p := filepath.Join(c.UserRoot, "koreader", "plugins")
	if st, err := os.Lstat(p); err != nil || !st.IsDir() {
		return ""
	}
	return p
}

// ---- manifest fetching with a saved copy ------------------------------------

func loadZenManifest(c Config, r Repo, online bool) (zenManifest, string) {
	cp := c.path("cache", "zen-"+unsafeName.ReplaceAllString(r.ID, "_")+".json")
	if online {
		req, _ := http.NewRequest("GET", r.URL, nil)
		req.Header.Set("User-Agent", "KindleForge-KFPM")
		if resp, err := httpClient.Do(req); err == nil {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == http.StatusOK {
				if m, perr := parseZenManifest(data); perr == nil {
					_ = os.MkdirAll(filepath.Dir(cp), 0o755)
					_ = writeAtomic(cp, data)
					return m, "network"
				}
			}
		}
	}
	if data, err := os.ReadFile(cp); err == nil {
		if m, err := parseZenManifest(data); err == nil {
			return m, "cache"
		}
	}
	return zenManifest{}, "unavailable"
}

type ZenSource struct {
	Repo Repo
	Pkg  zenPackage
	ID   string // namespaced: <repo>--<package>
}

func zenSources(c Config, rf ReposFile, online bool) (map[string]ZenSource, []RepoStatus) {
	out := map[string]ZenSource{}
	var statuses []RepoStatus
	for _, r := range rf.Repos {
		if r.Format != "zen" {
			continue
		}
		m, src := loadZenManifest(c, r, online)
		statuses = append(statuses, RepoStatus{ID: r.ID, URL: r.URL, Source: src, Packages: len(m.Packages)})
		if koreaderPluginsDir(c) == "" {
			continue // no KOReader here: none of these can be used
		}
		for _, p := range m.Packages {
			id := r.ID + "--" + p.ID
			if !zenEligible(p) || len(id) > 64 || !validID.MatchString(id) {
				continue
			}
			out[id] = ZenSource{Repo: r, Pkg: p, ID: id}
		}
	}
	return out, statuses
}

func zenCategory(c string) string {
	switch c {
	case "media", "games":
		return c
	case "reference":
		return "readers"
	}
	return "utilities"
}

// applyZen adds the offered plugins to the catalog.
func applyZen(c Config, rf ReposFile, online bool, cat *Catalog) {
	srcs, statuses := zenSources(c, rf, online)
	cat.Repos = append(cat.Repos, statuses...)
	rec := loadKpkgInstalled(c) // installed plugins are always listed, even if their source is off
	for _, id := range sortedKeys(srcs) {
		s := srcs[id]
		r, isInst := rec[id]
		p := CatPackage{
			ID: id, Name: s.Pkg.Name, Author: s.Pkg.Author, Description: s.Pkg.Description,
			Repo: s.Repo.ID, Category: zenCategory(s.Pkg.Category), Version: strings.TrimPrefix(s.Pkg.Version, "v"),
			Supported: true, Installed: isInst, Dependencies: []string{},
			Note: "A KOReader plugin. Restart KOReader after installing, updating or removing it.",
		}
		if isInst {
			p.InstalledVersion = r.Version
			p.UpdateAvailable = compareVersions(s.Pkg.Version, r.Version) > 0
		}
		cat.Packages = append(cat.Packages, p)
	}
	for _, id := range sortedKeys(rec) { // installed earlier from a source that is off now
		if rec[id].Kind != "zen" {
			continue
		}
		if _, ok := srcs[id]; ok {
			continue
		}
		cat.Packages = append(cat.Packages, CatPackage{ID: id, Name: rec[id].Module, Repo: rec[id].Repo, Supported: true,
			Installed: true, InstalledVersion: rec[id].Version, Dependencies: []string{},
			Note: "A KOReader plugin installed by KindleForge. Its source is switched off."})
	}
}

// ---- versions file + asset choice -------------------------------------------

type zenAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type zenRelease struct {
	Tag        string     `json:"tag_name"`
	Prerelease bool       `json:"prerelease"`
	Assets     []zenAsset `json:"assets"`
}

func resolveRepoURL(base, rel string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(rel)
	if err != nil {
		return "", err
	}
	u := b.ResolveReference(r)
	if u.Scheme != "https" {
		return "", fmt.Errorf("refusing a non-https address")
	}
	return u.String(), nil
}

func chooseZenAsset(c Config, s ZenSource) (zenAsset, error) {
	vu, err := resolveRepoURL(s.Repo.URL, s.Pkg.VersionsURL)
	if err != nil {
		return zenAsset{}, err
	}
	req, _ := http.NewRequest("GET", vu, nil)
	req.Header.Set("User-Agent", "KindleForge-KFPM")
	resp, err := httpClient.Do(req)
	if err != nil {
		return zenAsset{}, fmt.Errorf("could not read the version list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return zenAsset{}, fmt.Errorf("version list: HTTP %d", resp.StatusCode)
	}
	var v struct {
		Releases []zenRelease `json:"releases"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&v); err != nil {
		return zenAsset{}, fmt.Errorf("unreadable version list")
	}
	want := strings.TrimPrefix(s.Pkg.Version, "v")
	var fallback *zenAsset
	for _, r := range v.Releases {
		if r.Prerelease {
			continue
		}
		for i := range r.Assets {
			if r.Assets[i].Name != s.Pkg.SourceAsset {
				continue
			}
			if strings.TrimPrefix(r.Tag, "v") == want {
				return r.Assets[i], nil
			}
			if fallback == nil {
				fallback = &r.Assets[i]
			}
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return zenAsset{}, fmt.Errorf("the release file %q is not in the version list", s.Pkg.SourceAsset)
}

// ---- safe extraction of one .koplugin folder ----------------------------------

// extractKoPlugin unpacks the single top-level "<module>.koplugin" folder of a
// zip into dest. Anything outside that folder (README files and the like) is
// ignored. Returns the module name.
func extractKoPlugin(zipPath, dest string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("not a valid zip: %w", err)
	}
	defer zr.Close()

	modules := map[string]bool{}
	for _, f := range zr.File {
		name := f.Name
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			return "", fmt.Errorf("unsafe path in package: %q", name)
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return "", fmt.Errorf("unsafe path in package: %q", name)
		}
		top := strings.SplitN(clean, "/", 2)[0]
		if strings.HasSuffix(top, ".koplugin") && (strings.Contains(clean, "/") || f.FileInfo().IsDir()) {
			modules[strings.TrimSuffix(top, ".koplugin")] = true
		}
	}
	if len(modules) != 1 {
		return "", fmt.Errorf("this package's layout is not supported (it must hold exactly one <name>.koplugin folder)")
	}
	var module string
	for m := range modules {
		module = m
	}
	if !moduleRe.MatchString(module) {
		return "", fmt.Errorf("unsafe plugin name %q", module)
	}
	prefix := module + ".koplugin/"

	var total int64
	files := 0
	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) || f.Name == prefix {
			continue
		}
		rel := strings.TrimPrefix(path.Clean(f.Name), strings.TrimSuffix(prefix, "/")+"/")
		if f.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("links are not allowed in packages: %q", f.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if !within(dest, target) {
			return "", fmt.Errorf("unsafe path in package: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		files++
		total += int64(f.UncompressedSize64)
		if files > kpkgMaxFiles || total > kpkgMaxExpanded {
			return "", fmt.Errorf("package is too large")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		mode := os.FileMode(0o644)
		if f.Mode()&0o111 != 0 {
			mode = 0o755
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return "", err
		}
		n, cerr := io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		if err := out.Close(); err != nil || cerr != nil || n != int64(f.UncompressedSize64) {
			return "", fmt.Errorf("could not write %s", rel)
		}
	}
	if files == 0 {
		return "", fmt.Errorf("the plugin folder is empty")
	}
	for _, must := range []string{"_meta.lua", "main.lua"} {
		if st, err := os.Stat(filepath.Join(dest, must)); err == nil && !st.IsDir() {
			return module, nil
		}
	}
	return "", fmt.Errorf("that folder does not look like a KOReader plugin (no _meta.lua or main.lua)")
}

// ---- install / update / uninstall -------------------------------------------

func zenInstall(c Config, rf ReposFile, id string, verbose bool) error {
	srcs, _ := zenSources(c, rf, false)
	s, ok := srcs[id]
	if !ok {
		return fmt.Errorf("package %q is not offered by any switched-on source", id)
	}
	plugins := koreaderPluginsDir(c)
	if plugins == "" {
		return fmt.Errorf("KOReader was not found on this Kindle")
	}
	asset, err := chooseZenAsset(c, s)
	if err != nil {
		return err
	}
	if !zenAssetOK(asset.URL) {
		return fmt.Errorf("refusing to download from %s (only GitHub release files are allowed)", asset.URL)
	}
	if !sha256DigestRe.MatchString(asset.Digest) {
		return fmt.Errorf("the repository gives no checksum for this file, so it will not be installed")
	}
	sum := strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")

	tmp := filepath.Join(kpkgRoot(c), "tmp")
	_ = os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "plugin.zip")
	appendLog(c, "downloading "+asset.URL)
	if err := kpkgDownload(c, asset.URL, archive, sum); err != nil {
		return err
	}

	staging := filepath.Join(tmp, "unpacked")
	module, err := extractKoPlugin(archive, staging)
	if err != nil {
		return err
	}
	final := filepath.Join(plugins, module+".koplugin")
	rec := loadKpkgInstalled(c)
	if _, err := os.Lstat(final); err == nil {
		if r, mine := rec[id]; !mine || r.Module != module {
			return fmt.Errorf("%s.koplugin is already in KOReader and was not installed by KindleForge, so it was left alone", module)
		}
	}
	newDir, oldDir := final+".kf-new", final+".kf-old"
	_ = os.RemoveAll(newDir)
	_ = os.RemoveAll(oldDir)
	if err := os.Rename(staging, newDir); err != nil {
		// different filesystem: copy instead
		if err := copyTree(staging, newDir); err != nil {
			_ = os.RemoveAll(newDir)
			return err
		}
	}
	hadOld := false
	if _, err := os.Lstat(final); err == nil {
		if err := os.Rename(final, oldDir); err != nil {
			_ = os.RemoveAll(newDir)
			return err
		}
		hadOld = true
	}
	if err := os.Rename(newDir, final); err != nil {
		if hadOld {
			_ = os.Rename(oldDir, final)
		}
		_ = os.RemoveAll(newDir)
		return err
	}
	_ = os.RemoveAll(oldDir)

	rec[id] = KpkgRecord{Version: strings.TrimPrefix(s.Pkg.Version, "v"), Repo: s.Repo.ID, Kind: "zen", Module: module,
		At: time.Now().UTC().Format(time.RFC3339)}
	appendLog(c, "installed "+module+".koplugin; restart KOReader to load it")
	return saveKpkgInstalled(c, rec)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, cerr := io.Copy(out, in)
		if err := out.Close(); err != nil || cerr != nil {
			return fmt.Errorf("copy failed")
		}
		return nil
	})
}

func zenUninstall(c Config, id string) error {
	rec := loadKpkgInstalled(c)
	r, ok := rec[id]
	if !ok || r.Kind != "zen" {
		return fmt.Errorf("%s was not installed by KindleForge", id)
	}
	plugins := koreaderPluginsDir(c)
	if plugins == "" || !moduleRe.MatchString(r.Module) {
		return fmt.Errorf("cannot find the plugin folder")
	}
	target := filepath.Join(plugins, r.Module+".koplugin")
	if !within(plugins, target) || isSymlink(target) {
		return fmt.Errorf("refusing to remove %s", target)
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	delete(rec, id)
	return saveKpkgInstalled(c, rec)
}

func zenUpgradeAll(c Config, rf ReposFile, verbose bool) (int, error) {
	srcs, _ := zenSources(c, rf, false)
	rec := loadKpkgInstalled(c)
	done := 0
	var failed []string
	for _, id := range sortedKeys(rec) {
		s, ok := srcs[id]
		if !ok || rec[id].Kind != "zen" || compareVersions(s.Pkg.Version, rec[id].Version) <= 0 {
			continue
		}
		if err := zenInstall(c, rf, id, verbose); err != nil {
			appendLog(c, fmt.Sprintf("update of %s failed: %v", id, err))
			failed = append(failed, id)
			continue
		}
		done++
	}
	if len(failed) > 0 {
		return done, fmt.Errorf("could not update: %s", strings.Join(failed, ", "))
	}
	return done, nil
}
