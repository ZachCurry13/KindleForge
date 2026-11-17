package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Self-update: KindleForge checks the GitHub releases of its own repo, shows
// when a newer one exists, and (on request) downloads, verifies and swaps in
// the new app folder. Only the app folder is replaced; the launcher
// (KindleForge.sh) and KPM packages are never touched.

const (
	assetPrefix   = "KindleForge-KPM-"
	appZipRoot    = "KindleForge/" // folder inside the release zip that holds the app
	maxZipBytes   = 64 << 20
	maxZipFiles   = 500
	spaceHeadroom = 2 << 20
)

// ---- versions (semver with pre-release tags, e.g. v5.0.0-alpha.2) --------

type semver struct {
	core [3]int
	pre  []string
}

func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.Index(s, "+"); i >= 0 {
		s = s[:i]
	}
	var v semver
	core := s
	if i := strings.Index(s, "-"); i >= 0 {
		core = s[:i]
		v.pre = strings.Split(s[i+1:], ".")
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.core[i] = n
	}
	return v, true
}

func (a semver) cmp(b semver) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	// No pre-release outranks any pre-release of the same core.
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xerr == nil: // numeric < alphanumeric
			return -1
		case yerr == nil:
			return 1
		default:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// ---- GitHub releases ------------------------------------------------------

type ghAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"` // "sha256:<hex>"
}

type ghRelease struct {
	Tag    string    `json:"tag_name"`
	Draft  bool      `json:"draft"`
	Notes  string    `json:"body"`
	Assets []ghAsset `json:"assets"`
}

type candidate struct {
	rel   ghRelease
	asset ghAsset
	ver   semver
}

// latestRelease returns the highest-versioned published release that carries
// a KindleForge zip with a SHA-256 digest. Pre-releases are included.
func latestRelease(c Config) (candidate, error) {
	var best candidate
	client := &http.Client{Timeout: 20 * time.Second}
	req, _ := http.NewRequest("GET", c.ReleasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "KindleForge-KFPM")
	resp, err := client.Do(req)
	if err != nil {
		return best, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return best, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	var rels []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels); err != nil {
		return best, fmt.Errorf("unreadable release list: %w", err)
	}
	found := false
	for _, r := range rels {
		if r.Draft {
			continue
		}
		v, ok := parseSemver(r.Tag)
		if !ok {
			continue
		}
		for _, a := range r.Assets {
			if strings.HasPrefix(a.Name, assetPrefix) && strings.HasSuffix(a.Name, ".zip") && strings.HasPrefix(a.Digest, "sha256:") {
				if !found || v.cmp(best.ver) > 0 {
					best = candidate{rel: r, asset: a, ver: v}
					found = true
				}
				break
			}
		}
	}
	if !found {
		return best, fmt.Errorf("no verified release found")
	}
	return best, nil
}

// ---- version file + update.json -------------------------------------------

func readAppVersion(c Config) string {
	if c.AppDir == "" {
		return "0.0.0"
	}
	data, err := os.ReadFile(filepath.Join(c.AppDir, "VERSION"))
	if err != nil {
		return "0.0.0"
	}
	if v := strings.TrimSpace(string(data)); v != "" {
		return v
	}
	return "0.0.0"
}

// UpdateInfo is what the page displays. It deliberately carries no download
// URL or hash: applyUpdate always re-queries GitHub itself.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes"`
	Size      int64  `json:"size"`
	CheckedAt string `json:"checkedAt"`
}

func isNewer(latest, current string) bool {
	l, lok := parseSemver(latest)
	if !lok {
		return false
	}
	cur, cok := parseSemver(current)
	if !cok {
		return true
	}
	return l.cmp(cur) > 0
}

func checkUpdate(c Config) (UpdateInfo, error) {
	info := UpdateInfo{Current: readAppVersion(c)}
	cand, err := latestRelease(c)
	if err != nil {
		return info, err
	}
	info.Latest = strings.TrimPrefix(cand.rel.Tag, "v")
	info.Available = isNewer(cand.rel.Tag, info.Current)
	info.Notes = strings.TrimSpace(cand.rel.Notes)
	if len(info.Notes) > 1500 {
		info.Notes = info.Notes[:1500]
	}
	info.Size = cand.asset.Size
	info.CheckedAt = time.Now().UTC().Format(time.RFC3339)

	if data, err := json.MarshalIndent(info, "", "  "); err == nil {
		_ = os.MkdirAll(c.DataDir, 0o755)
		_ = writeAtomic(c.path("update.json"), data)
	}
	return info, nil
}

// writeLocalVersion records the installed version for the page even when no
// network check has happened yet.
func writeLocalVersion(c Config) {
	p := c.path("update.json")
	var info UpdateInfo
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	cur := readAppVersion(c)
	if info.Current == cur && info.Latest != "" {
		// Re-evaluate against the new version (e.g. right after an update).
		info.Available = isNewer(info.Latest, cur)
	} else {
		info = UpdateInfo{Current: cur}
	}
	if data, err := json.MarshalIndent(info, "", "  "); err == nil {
		_ = writeAtomic(p, data)
	}
}

// ---- applying an update ---------------------------------------------------

func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func download(url, dest string, wantSize int64, wantSHA string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "KindleForge-KFPM")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	if wantSize <= 0 || wantSize > maxZipBytes {
		return fmt.Errorf("unexpected package size %d", wantSize)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, wantSize+1))
	if err != nil {
		return err
	}
	if n != wantSize {
		return fmt.Errorf("download size mismatch (got %d, expected %d)", n, wantSize)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(wantSHA) {
		return fmt.Errorf("checksum mismatch: refusing to install")
	}
	return nil
}

// extractApp unpacks the KindleForge/ tree of the release zip into dest,
// rejecting path traversal, symlinks and oversized archives.
func extractApp(zipPath, dest string) (int64, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	type entry struct {
		f   *zip.File
		rel string
	}
	var entries []entry
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			return 0, fmt.Errorf("unsafe path in package: %q", name)
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return 0, fmt.Errorf("unsafe path in package: %q", name)
		}
		if !strings.HasPrefix(name, appZipRoot) || f.FileInfo().IsDir() {
			continue // launcher, docs etc. are not part of an in-app update
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("symlink in package: %q", name)
		}
		total += int64(f.UncompressedSize64)
		if total > maxZipBytes || len(entries) >= maxZipFiles {
			return 0, fmt.Errorf("package too large")
		}
		entries = append(entries, entry{f, strings.TrimPrefix(clean, strings.TrimSuffix(appZipRoot, "/")+"/")})
	}
	if len(entries) == 0 {
		return 0, fmt.Errorf("package has no %s folder", appZipRoot)
	}

	if free, err := freeBytes(filepath.Dir(dest)); err == nil && free < uint64(total)+spaceHeadroom {
		return 0, fmt.Errorf("not enough free space (%d KB needed)", (total+spaceHeadroom)/1024)
	}

	_ = os.RemoveAll(dest)
	for _, e := range entries {
		target := filepath.Join(dest, filepath.FromSlash(e.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(e.rel, "binaries/") {
			mode = 0o755
		}
		rc, err := e.f.Open()
		if err != nil {
			return 0, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return 0, err
		}
		_, cerr := io.Copy(out, rc)
		rc.Close()
		if err := out.Close(); err != nil || cerr != nil {
			return 0, fmt.Errorf("could not write %s", e.rel)
		}
	}
	return total, nil
}

// validateStaged makes sure an extracted app is complete and is the version
// the release claims to be.
func validateStaged(dir, wantVersion string) error {
	for _, f := range []string{"index.html", "script.js", "style.css", "VERSION", "binaries/KFPM"} {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil || st.IsDir() {
			return fmt.Errorf("package is missing %s", f)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "VERSION"))
	if got := strings.TrimPrefix(strings.TrimSpace(string(data)), "v"); got != strings.TrimPrefix(wantVersion, "v") {
		return fmt.Errorf("package VERSION is %q, expected %q", got, wantVersion)
	}
	return nil
}

// swapDirs replaces appDir with newDir, restoring the old app if the final
// step fails.
func swapDirs(appDir, newDir string) error {
	old := appDir + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(appDir, old); err != nil {
		return fmt.Errorf("could not move the current app aside: %w", err)
	}
	if err := os.Rename(newDir, appDir); err != nil {
		if rerr := os.Rename(old, appDir); rerr != nil {
			return fmt.Errorf("update failed AND rollback failed: %v / %v", err, rerr)
		}
		return fmt.Errorf("update failed, previous version restored: %w", err)
	}
	_ = os.RemoveAll(old)
	return nil
}

func applyUpdate(c Config) error {
	if c.AppDir == "" {
		return fmt.Errorf("cannot locate the installed app folder")
	}
	if st, err := os.Stat(filepath.Join(c.AppDir, "index.html")); err != nil || st.IsDir() {
		return fmt.Errorf("%s does not look like a KindleForge install", c.AppDir)
	}

	cand, err := latestRelease(c)
	if err != nil {
		return err
	}
	current := readAppVersion(c)
	if !isNewer(cand.rel.Tag, current) {
		return fmt.Errorf("already up to date (%s)", current)
	}
	sum := strings.TrimPrefix(cand.asset.Digest, "sha256:")

	appendLog(c, fmt.Sprintf("updating %s -> %s (%s)", current, cand.rel.Tag, cand.asset.Name))
	zipPath := c.path("update", cand.asset.Name)
	defer os.RemoveAll(c.path("update"))
	if err := download(cand.asset.URL, zipPath, cand.asset.Size, sum); err != nil {
		return err
	}

	newDir := c.AppDir + ".new"
	defer os.RemoveAll(newDir)
	if _, err := extractApp(zipPath, newDir); err != nil {
		return err
	}
	if err := validateStaged(newDir, cand.rel.Tag); err != nil {
		return err
	}
	if err := swapDirs(c.AppDir, newDir); err != nil {
		return err
	}
	writeLocalVersion(c)
	return nil
}

// sortedReleaseTags is a small helper for tests and diagnostics.
func sortedVersions(vs []string) []string {
	out := append([]string(nil), vs...)
	sort.Slice(out, func(i, j int) bool {
		a, _ := parseSemver(out[i])
		b, _ := parseSemver(out[j])
		return a.cmp(b) < 0
	})
	return out
}
