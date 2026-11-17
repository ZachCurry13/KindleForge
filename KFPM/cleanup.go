package main

// Device cleanup. Design rules (deleting files on someone's Kindle is easy to
// get wrong, so the feature is deliberately narrow):
//
//   - FIXED ALLOWLIST. Only the categories below exist. Nothing takes a path
//     from the UI or from a file the user can edit: apply re-scans and derives
//     the items itself from the category ids.
//   - SCAN IS READ-ONLY. It writes a report (cleanup.json) and nothing else.
//   - TRASH, NOT DELETE. Selected items are MOVED into <data>/trash with a
//     manifest, so they can be restored. Only "empty trash" deletes for real.
//   - Never follows symlinks, never touches the user's books, My Clippings,
//     KOReader, extensions, system folders, or anything outside the roots.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

type CleanCategory struct {
	ID          string
	Title       string
	Description string
	Default     bool // pre-selected in the UI
}

// The only things KFPM will ever offer to clean.
var cleanCategories = []CleanCategory{
	{"crash", "Crash dumps", "Crash reports the Kindle saved when a system process or the browser crashed (pillowd_*, mesquite_* ... _crash).", true},
	{"oldforge", "Old KindleForge leftovers", "KF-Update-Temp, KFPM-Temporary and the old installed.txt from the original KindleForge.", true},
	{"dumps", "Indexer dumps", "Indexer_Dump_*.txt diagnostic files.", true},
	{"diag", "KindleForge test reports", "kfpm-diag.txt and kfpm-net.txt written by the diagnostics scriptlets.", true},
	{"kfupdate", "KindleForge download leftovers", "Temporary files from interrupted updates.", true},
	{"orphansdr", "Orphaned book data (.sdr)", "Reading progress and highlights for books that are no longer on the Kindle. Removing them loses that data if you re-add the book.", false},
}

type CleanItem struct {
	Category string
	Path     string // absolute
	Name     string
	Bytes    int64
}

var crashRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]*_[0-9]+_.*_crash`)

func validCleanCategory(id string) bool {
	for _, c := range cleanCategories {
		if c.ID == id {
			return true
		}
	}
	return false
}

// ---- helpers --------------------------------------------------------------

func pathSize(p string) int64 {
	var total int64
	_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// allowedPath is the last line of defence before anything is moved or
// restored: it must sit inside the Kindle's user area (or KFPM's own data
// folder), must not be a protected top-level folder, and must not be a link.
func allowedPath(c Config, p string) bool {
	p = filepath.Clean(p)
	if !within(c.UserRoot, p) && !within(c.DataDir, p) {
		return false
	}
	if isSymlink(p) {
		return false
	}
	if rel, err := filepath.Rel(c.UserRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
		if protectedTop[strings.Split(rel, string(filepath.Separator))[0]] {
			return false
		}
		if rel == "documents" || rel == ".KFPM" {
			return false
		}
	}
	// KFPM's own trash must never be scanned into itself.
	if within(c.path("trash"), p) || filepath.Clean(p) == c.path("trash") {
		return false
	}
	return true
}

// Top-level folders that cleanup must never touch, whatever the scan finds.
var protectedTop = map[string]bool{
	"system": true, "Books": true, "extensions": true, "mrpackages": true, "koreader": true,
	"libkh": true, "audible": true, "voice": true, "fonts": true, "kpm": true,
	"dictionaries": true, "screenshots": true, "kmc": true,
}

func add(c Config, items *[]CleanItem, cat, p string) {
	if !allowedPath(c, p) {
		return
	}
	if _, err := os.Lstat(p); err != nil {
		return
	}
	*items = append(*items, CleanItem{Category: cat, Path: p, Name: filepath.Base(p), Bytes: pathSize(p)})
}

// ---- scan -----------------------------------------------------------------

func scanCleanup(c Config) []CleanItem {
	var items []CleanItem
	docs := filepath.Join(c.UserRoot, "documents")

	// crash dumps: top level of documents/ and of the user area
	for _, dir := range []string{docs, c.UserRoot} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if crashRe.MatchString(e.Name()) {
				add(c, &items, "crash", filepath.Join(dir, e.Name()))
			}
		}
	}

	// old KindleForge
	for _, rel := range []string{"KF-Update-Temp", "KFPM-Temporary", filepath.Join(".KFPM", "installed.txt")} {
		add(c, &items, "oldforge", filepath.Join(c.UserRoot, rel))
	}
	// the data dir may not be /mnt/us/.KFPM (tests); cover its installed.txt too
	if c.DataDir != filepath.Join(c.UserRoot, ".KFPM") {
		add(c, &items, "oldforge", filepath.Join(c.DataDir, "installed.txt"))
	}

	// indexer dumps
	if m, err := filepath.Glob(filepath.Join(c.UserRoot, "Indexer_Dump_*.txt")); err == nil {
		for _, p := range m {
			add(c, &items, "dumps", p)
		}
	}

	// our own test reports
	for _, n := range []string{"kfpm-diag.txt", "kfpm-net.txt"} {
		add(c, &items, "diag", filepath.Join(c.UserRoot, n))
	}

	// interrupted update downloads
	add(c, &items, "kfupdate", c.path("update"))
	for _, pat := range []string{filepath.Join(c.DataDir, "*.tmp"), filepath.Join(c.DataDir, "cache", "*.tmp")} {
		if m, err := filepath.Glob(pat); err == nil {
			for _, p := range m {
				add(c, &items, "kfupdate", p)
			}
		}
	}

	items = append(items, scanOrphanSDR(c, docs)...)
	return items
}

// scanOrphanSDR finds <name>.sdr folders whose book/script no longer exists.
// Kindle names the sidecar after the book WITHOUT its extension
// (Book.azw3 -> Book.sdr) but scriptlets keep it (a.sh -> a.sh.sdr), so a
// sibling named <base> or <base>.<anything> means it is in use.
func scanOrphanSDR(c Config, docs string) []CleanItem {
	var items []CleanItem
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		names := map[string]bool{}
		for _, e := range entries {
			names[e.Name()] = true
		}
		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(dir, name)
			if e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if e.IsDir() && strings.HasSuffix(name, ".sdr") {
				base := strings.TrimSuffix(name, ".sdr")
				inUse := false
				for sib := range names {
					if strings.HasSuffix(sib, ".sdr") {
						continue
					}
					if sib == base || strings.HasPrefix(sib, base+".") {
						inUse = true
						break
					}
				}
				if !inUse {
					add(c, &items, "orphansdr", full)
				}
				continue
			}
			if e.IsDir() && depth < 4 && !strings.HasPrefix(name, ".") {
				walk(full, depth+1)
			}
		}
	}
	walk(docs, 0)
	return items
}

// ---- trash ----------------------------------------------------------------

type TrashEntry struct {
	ID       string `json:"id"`
	Batch    string `json:"batch"`
	Category string `json:"category"`
	Original string `json:"original"`
	Stored   string `json:"stored"` // relative to the trash folder
	Name     string `json:"name"`
	Bytes    int64  `json:"bytes"`
	At       string `json:"at"`
}

func loadTrash(c Config) []TrashEntry {
	var t []TrashEntry
	if data, err := os.ReadFile(c.path("trash", "manifest.json")); err == nil {
		_ = json.Unmarshal(data, &t)
	}
	return t
}

func saveTrash(c Config, t []TrashEntry) error {
	if err := os.MkdirAll(c.path("trash"), 0o755); err != nil {
		return err
	}
	if len(t) == 0 {
		_ = os.Remove(c.path("trash", "manifest.json"))
		return nil
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(c.path("trash", "manifest.json"), data)
}

type CleanupResult struct {
	Action  string   `json:"action"`
	Count   int      `json:"count"`
	Bytes   int64    `json:"bytes"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

// applyCleanup moves the items of the chosen categories into the trash.
func applyCleanup(c Config, cats []string) CleanupResult {
	res := CleanupResult{Action: "trash"}
	chosen := map[string]bool{}
	for _, id := range cats {
		if !validCleanCategory(id) {
			res.Errors = append(res.Errors, "unknown category: "+id)
			return res
		}
		chosen[id] = true
	}
	if len(chosen) == 0 {
		res.Errors = append(res.Errors, "nothing selected")
		return res
	}

	batch := time.Now().UTC().Format("20060102-150405")
	trash := loadTrash(c)
	n := len(trash)
	for _, it := range scanCleanup(c) {
		if !chosen[it.Category] {
			continue
		}
		if !allowedPath(c, it.Path) { // re-checked right before the move
			res.Skipped++
			continue
		}
		n++
		stored := filepath.Join(batch, fmt.Sprintf("%04d", n))
		dest := c.path("trash", stored)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		if err := os.Rename(it.Path, dest); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", it.Name, err))
			continue
		}
		trash = append(trash, TrashEntry{
			ID: fmt.Sprintf("%s-%04d", batch, n), Batch: batch, Category: it.Category,
			Original: it.Path, Stored: stored, Name: it.Name, Bytes: it.Bytes,
			At: time.Now().UTC().Format(time.RFC3339),
		})
		res.Count++
		res.Bytes += it.Bytes
	}
	if err := saveTrash(c, trash); err != nil {
		res.Errors = append(res.Errors, "could not save the trash list: "+err.Error())
	}
	return res
}

// restoreTrash puts everything back where it came from. Items whose original
// location is taken (or no longer allowed) are left in the trash.
func restoreTrash(c Config) CleanupResult {
	res := CleanupResult{Action: "restore"}
	var keep []TrashEntry
	for _, e := range loadTrash(c) {
		src := c.path("trash", e.Stored)
		orig := filepath.Clean(e.Original)
		if !filepath.IsAbs(orig) || !(within(c.UserRoot, orig) || within(c.DataDir, orig)) {
			res.Skipped++
			keep = append(keep, e) // a tampered manifest must not move files elsewhere
			continue
		}
		if _, err := os.Lstat(orig); err == nil {
			res.Skipped++
			keep = append(keep, e)
			continue
		}
		if _, err := os.Lstat(src); err != nil {
			continue // gone from the trash; drop the stale record
		}
		if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
			res.Skipped++
			keep = append(keep, e)
			continue
		}
		if err := os.Rename(src, orig); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", e.Name, err))
			keep = append(keep, e)
			continue
		}
		res.Count++
		res.Bytes += e.Bytes
	}
	if err := saveTrash(c, keep); err != nil {
		res.Errors = append(res.Errors, err.Error())
	}
	return res
}

// emptyTrash permanently deletes the trash. This is the only real delete.
func emptyTrash(c Config) CleanupResult {
	res := CleanupResult{Action: "empty"}
	for _, e := range loadTrash(c) {
		res.Count++
		res.Bytes += e.Bytes
	}
	if err := os.RemoveAll(c.path("trash")); err != nil {
		res.Errors = append(res.Errors, err.Error())
		res.Count = 0
	}
	return res
}

// ---- report (what the page reads) -----------------------------------------

type ReportCategory struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Default     bool     `json:"default"`
	Count       int      `json:"count"`
	Bytes       int64    `json:"bytes"`
	Examples    []string `json:"examples"`
}

type BigItem struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Approx bool   `json:"approx,omitempty"`
}

type CleanupReport struct {
	GeneratedAt string           `json:"generatedAt"`
	FreeBytes   int64            `json:"freeBytes"`
	TrashItems  int              `json:"trashItems"`
	TrashBytes  int64            `json:"trashBytes"`
	Categories  []ReportCategory `json:"categories"`
	Biggest     []BigItem        `json:"biggest"`
	Last        *CleanupResult   `json:"last,omitempty"`
}

func shortName(s string) string {
	r := []rune(s)
	if len(r) > 48 {
		return string(r[:47]) + "…"
	}
	return s
}

// biggest lists the largest top-level entries of the user area, within a time
// budget (a big library can take a while to measure on a Kindle's CPU).
func biggest(c Config, budget time.Duration) []BigItem {
	skip := map[string]bool{"system": true, ".bcache": true, ".cache": true}
	entries, err := os.ReadDir(c.UserRoot)
	if err != nil {
		return nil
	}
	deadline := time.Now().Add(budget)
	var out []BigItem
	for _, e := range entries {
		if skip[e.Name()] || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		p := filepath.Join(c.UserRoot, e.Name())
		if time.Now().After(deadline) {
			out = append(out, BigItem{Path: e.Name(), Approx: true})
			continue
		}
		out = append(out, BigItem{Path: e.Name(), Bytes: pathSize(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func buildCleanupReport(c Config, last *CleanupResult, withBiggest bool) CleanupReport {
	rep := CleanupReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Last: last, Categories: []ReportCategory{}}
	var st syscall.Statfs_t
	if syscall.Statfs(c.UserRoot, &st) == nil {
		rep.FreeBytes = int64(uint64(st.Bavail) * uint64(st.Bsize))
	}
	for _, t := range loadTrash(c) {
		rep.TrashItems++
		rep.TrashBytes += t.Bytes
	}
	items := scanCleanup(c)
	for _, cat := range cleanCategories {
		rc := ReportCategory{ID: cat.ID, Title: cat.Title, Description: cat.Description, Default: cat.Default, Examples: []string{}}
		for _, it := range items {
			if it.Category != cat.ID {
				continue
			}
			rc.Count++
			rc.Bytes += it.Bytes
			if len(rc.Examples) < 3 {
				rc.Examples = append(rc.Examples, shortName(it.Name))
			}
		}
		rep.Categories = append(rep.Categories, rc)
	}
	if withBiggest {
		rep.Biggest = biggest(c, 15*time.Second)
	} else if prev, err := os.ReadFile(c.path("cleanup.json")); err == nil {
		var old CleanupReport // keep the (slow) size list from the last full scan
		if json.Unmarshal(prev, &old) == nil {
			rep.Biggest = old.Biggest
		}
	}
	return rep
}

func writeCleanupReport(c Config, rep CleanupReport) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	return writeAtomic(c.path("cleanup.json"), data)
}

// runCleanup is the entry point for `kfpm -k <scan|apply|restore|empty> [cats]`.
func runCleanup(c Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: -k scan | apply <cat,cat> | restore | empty")
	}
	var last *CleanupResult
	full := false
	switch args[0] {
	case "scan":
		full = true
	case "apply":
		if len(args) < 2 {
			return fmt.Errorf("apply needs a category list")
		}
		r := applyCleanup(c, strings.Split(args[1], ","))
		last = &r
	case "restore":
		r := restoreTrash(c)
		last = &r
	case "empty":
		r := emptyTrash(c)
		last = &r
	default:
		return fmt.Errorf("unknown cleanup command %q", args[0])
	}
	rep := buildCleanupReport(c, last, full)
	if err := writeCleanupReport(c, rep); err != nil {
		return err
	}
	if last != nil && len(last.Errors) > 0 && last.Count == 0 {
		return fmt.Errorf("%s", strings.Join(last.Errors, "; "))
	}
	return nil
}
