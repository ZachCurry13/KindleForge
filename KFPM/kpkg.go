package main

// Installing KindleModding-style packages (.kpkg) without KindleModding's KPM.
//
// A repository manifest (like KindleTweaks/Repo) lists packages whose artifacts
// are gzipped tar files holding manifest.json, install.sh, uninstall.sh and a
// payload. This file downloads one, unpacks it safely, runs its hooks the way
// KPM does (from inside the package folder), and remembers what is installed
// and at which version, so Update buttons work.
//
// Trust: hooks are shell scripts from the repository and run as root, exactly
// as they do under KPM, and the manifest carries no checksums. Only https
// sources are accepted, and only repositories listed in the catalog definition
// (or the user's own repos.json) are used.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	kpkgMaxDownload = 64 << 20
	kpkgMaxExpanded = 160 << 20
	kpkgMaxFiles    = 2000
	kpkgHookTimeout = 5 * time.Minute
)

type KpkgRecord struct {
	Version string `json:"version"`
	Repo    string `json:"repo"`
	At      string `json:"at"`
}

func kpkgRoot(c Config) string              { return c.path("kpkg") }
func kpkgPkgDir(c Config, id string) string { return filepath.Join(kpkgRoot(c), "pkg", id) }

func loadKpkgInstalled(c Config) map[string]KpkgRecord {
	m := map[string]KpkgRecord{}
	if data, err := os.ReadFile(filepath.Join(kpkgRoot(c), "installed.json")); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func saveKpkgInstalled(c Config, m map[string]KpkgRecord) error {
	if err := os.MkdirAll(kpkgRoot(c), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(kpkgRoot(c), "installed.json"), data)
}

func parseVersionString(s string) Version {
	var v Version
	for i, p := range strings.SplitN(s, ".", 3) {
		n, _ := strconv.Atoi(p)
		v[i] = n
	}
	return v
}

// ---- sources: which packages the manifests offer ---------------------------

type KpkgSource struct {
	Repo        Repo
	ManifestURL string
	Pkg         ManifestPackage
	Art         *Artifact // best artifact for this device, nil if none
}

// kpkgSources reads every manifest repo (cache fallback) and returns the
// packages they offer. The first repo listed wins on duplicate ids.
func kpkgSources(c Config, rf ReposFile, online bool) (map[string]KpkgSource, []RepoStatus) {
	out := map[string]KpkgSource{}
	var statuses []RepoStatus
	for _, r := range rf.Repos {
		m, src := loadManifest(c, r, online)
		statuses = append(statuses, RepoStatus{ID: r.ID, URL: r.URL, Source: src, Packages: len(m.Packages)})
		for id, p := range m.Packages {
			if _, dup := out[id]; dup || !pkgOK(id) {
				continue
			}
			s := KpkgSource{Repo: r, ManifestURL: r.URL, Pkg: p}
			for i := range p.Artifacts {
				a := &p.Artifacts[i]
				if a.supports(c.Platform) && (s.Art == nil || a.Version.Cmp(s.Art.Version) > 0) {
					s.Art = a
				}
			}
			out[id] = s
		}
	}
	return out, statuses
}

func pkgOK(id string) bool { return validID.MatchString(id) }

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ---- download + safe extraction --------------------------------------------

func resolveArtifactURL(manifestURL, rel string) (string, error) {
	base, err := url.Parse(manifestURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(rel)
	if err != nil {
		return "", err
	}
	u := base.ResolveReference(ref)
	if u.Scheme != "https" {
		return "", fmt.Errorf("refusing non-https package url")
	}
	return u.String(), nil
}

func kpkgDownload(c Config, rawURL, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	if httpClient != nil && httpClient.Transport != nil { // tests swap in a client that trusts the fake server
		client = &http.Client{Timeout: 10 * time.Minute, Transport: httpClient.Transport}
	}
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("User-Agent", "KindleForge-KFPM")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, kpkgMaxDownload+1))
	if err != nil {
		return err
	}
	if n > kpkgMaxDownload {
		return fmt.Errorf("package is larger than %d MB", kpkgMaxDownload>>20)
	}
	if n == 0 {
		return fmt.Errorf("empty download")
	}
	return nil
}

// extractKpkg unpacks a .kpkg (gzipped tar) into dest, rejecting path
// traversal, links, devices and oversized archives.
func extractKpkg(file, dest string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a valid package (gzip): %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	var total int64
	files := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("not a valid package (tar): %w", err)
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name == "" || name == "." {
			continue
		}
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("unsafe path in package: %q", h.Name)
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return fmt.Errorf("unsafe path in package: %q", h.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		if !within(dest, target) {
			return fmt.Errorf("unsafe path in package: %q", h.Name)
		}
		files++
		if files > kpkgMaxFiles {
			return fmt.Errorf("package has too many files")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > kpkgMaxExpanded {
				return fmt.Errorf("package expands to more than %d MB", kpkgMaxExpanded>>20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, cerr := io.Copy(out, io.LimitReader(tr, h.Size))
			if err := out.Close(); err != nil || cerr != nil || n != h.Size {
				return fmt.Errorf("could not write %s", clean)
			}
		default:
			return fmt.Errorf("unsupported entry in package (links and devices are not allowed): %q", h.Name)
		}
	}
	if files == 0 {
		return fmt.Errorf("package is empty")
	}
	return nil
}

type kpkgManifestFile struct {
	ID        string   `json:"id"`
	Version   []int    `json:"version"`
	Platforms []string `json:"supported_platforms"`
}

// checkPackageManifest makes sure the unpacked package is the one asked for.
func checkPackageManifest(dir, id string, want Version, platform string) error {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("package has no manifest.json")
	}
	var m kpkgManifestFile
	if json.Unmarshal(data, &m) != nil {
		return fmt.Errorf("package manifest.json is not valid")
	}
	if m.ID != id {
		return fmt.Errorf("package says it is %q, expected %q", m.ID, id)
	}
	if len(m.Version) != 3 || (Version{m.Version[0], m.Version[1], m.Version[2]}) != want {
		return fmt.Errorf("package version does not match the catalog (%v vs %s)", m.Version, want)
	}
	for _, p := range m.Platforms {
		if p == platform {
			return nil
		}
	}
	return fmt.Errorf("package does not support this device (%s)", platform)
}

// ---- hooks -----------------------------------------------------------------

func hookEnv(c Config, pkgDir string) []string {
	v := Version{}
	if cur := readAppVersion(c); cur != "" {
		v = parseVersionString(strings.SplitN(cur, "-", 2)[0])
	}
	return []string{
		"PATH=/usr/local/bin:/mnt/us/kpm/packages/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + envOr("HOME", "/tmp"),
		"KPM_PLATFORM=" + c.Platform,
		fmt.Sprintf("KPM_VERSION_MAJOR=%d", v[0]),
		fmt.Sprintf("KPM_VERSION_MINOR=%d", v[1]),
		fmt.Sprintf("KPM_VERSION_PATCH=%d", v[2]),
		"KPM_PKG_DIR=" + pkgDir,
	}
}

// runHook runs a package hook from inside the package folder. A missing hook
// is fine (every hook is optional).
func runHook(c Config, dir, script string, verbose bool) error {
	p := filepath.Join(dir, script)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), kpkgHookTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", script)
	cmd.Dir = dir
	cmd.Env = hookEnv(c, dir)
	var w io.Writer = &logWriter{c: c}
	if verbose {
		w = io.MultiWriter(w, os.Stdout)
	}
	cmd.Stdout, cmd.Stderr = w, w
	appendLog(c, "$ sh "+script+"  (in "+dir+")")
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s timed out after %s", script, kpkgHookTimeout)
	}
	if err != nil {
		return fmt.Errorf("%s failed: %v", script, err)
	}
	return nil
}

// logWriter streams hook output into the logs as it happens.
type logWriter struct{ c Config }

func (l *logWriter) Write(p []byte) (int, error) {
	logBytes(l.c, p)
	return len(p), nil
}

// ---- install / update / uninstall ------------------------------------------

func kpkgInstall(c Config, rf ReposFile, id string, verbose bool) error {
	srcs, _ := kpkgSources(c, rf, false)
	src, ok := srcs[id]
	if !ok {
		srcs, _ = kpkgSources(c, rf, true) // not in the saved lists: look online once
		src, ok = srcs[id]
	}
	if !ok {
		return fmt.Errorf("package %q is not in any repository", id)
	}
	if src.Art == nil {
		return fmt.Errorf("%s is not available for this device (%s)", id, c.Platform)
	}
	if len(src.Art.Deps) > 0 {
		return fmt.Errorf("%s needs other packages, which this installer does not handle yet", id)
	}
	artURL, err := resolveArtifactURL(src.ManifestURL, src.Art.URL)
	if err != nil {
		return err
	}

	tmp := filepath.Join(kpkgRoot(c), "tmp")
	_ = os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, id+".kpkg")
	appendLog(c, "downloading "+artURL)
	if err := kpkgDownload(c, artURL, archive); err != nil {
		return err
	}

	pkgDir := kpkgPkgDir(c, id)
	newDir, oldDir := pkgDir+".new", pkgDir+".old"
	_ = os.RemoveAll(newDir)
	_ = os.RemoveAll(oldDir)
	defer os.RemoveAll(newDir)
	if err := extractKpkg(archive, newDir); err != nil {
		return err
	}
	if err := checkPackageManifest(newDir, id, src.Art.Version, c.Platform); err != nil {
		return err
	}

	// swap the new folder in (keeping the old one until the hook succeeds)
	hadOld := false
	if _, err := os.Lstat(pkgDir); err == nil {
		if err := os.Rename(pkgDir, oldDir); err != nil {
			return err
		}
		hadOld = true
	}
	if err := os.MkdirAll(filepath.Dir(pkgDir), 0o755); err != nil {
		return err
	}
	if err := os.Rename(newDir, pkgDir); err != nil {
		if hadOld {
			_ = os.Rename(oldDir, pkgDir)
		}
		return err
	}
	if err := runHook(c, pkgDir, "install.sh", verbose); err != nil {
		_ = os.RemoveAll(pkgDir)
		if hadOld {
			_ = os.Rename(oldDir, pkgDir)
		}
		return err
	}
	_ = os.RemoveAll(oldDir)

	rec := loadKpkgInstalled(c)
	rec[id] = KpkgRecord{Version: src.Art.Version.String(), Repo: src.Repo.ID, At: time.Now().UTC().Format(time.RFC3339)}
	return saveKpkgInstalled(c, rec)
}

func kpkgUninstall(c Config, id string, verbose bool) error {
	rec := loadKpkgInstalled(c)
	if _, ok := rec[id]; !ok {
		return fmt.Errorf("%s was not installed by KindleForge", id)
	}
	pkgDir := kpkgPkgDir(c, id)
	if err := runHook(c, pkgDir, "uninstall.sh", verbose); err != nil {
		return err // keep the package so the removal can be retried
	}
	if err := os.RemoveAll(pkgDir); err != nil {
		return err
	}
	delete(rec, id)
	return saveKpkgInstalled(c, rec)
}

// kpkgUpgradeAll updates every installed package that has a newer version.
// Returns how many were updated.
func kpkgUpgradeAll(c Config, rf ReposFile, verbose bool) (int, error) {
	srcs, _ := kpkgSources(c, rf, false)
	rec := loadKpkgInstalled(c)
	done := 0
	var failed []string
	for _, id := range sortedKeys(rec) {
		s, ok := srcs[id]
		if !ok || s.Art == nil || s.Art.Version.Cmp(parseVersionString(rec[id].Version)) <= 0 {
			continue
		}
		if err := kpkgInstall(c, rf, id, verbose); err != nil {
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
