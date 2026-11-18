package main

// "Where did it go?" Installers copy launchers and tools into different
// places (the Library, KUAL, a terminal command folder, new folders), and some
// do nothing visible at all. KFPM looks at those places before and after an
// install and records what was added, so the app can say where to find it, or
// that nothing appeared.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Where struct {
	Checked  bool     `json:"checked"`
	Library  []string `json:"library,omitempty"`  // new launchers in documents/ (shown in the Library)
	KUAL     []string `json:"kual,omitempty"`     // new folders in extensions/ (shown in KUAL)
	Terminal []string `json:"terminal,omitempty"` // new commands (run from KTerm)
	Folders  []string `json:"folders,omitempty"`  // new top-level folders
	KOReader []string `json:"koreader,omitempty"` // plugin folders (restart KOReader)
	At       string   `json:"at,omitempty"`
}

func (w Where) empty() bool {
	return len(w.Library)+len(w.KUAL)+len(w.Terminal)+len(w.Folders)+len(w.KOReader) == 0
}

// top-level folders that are not an app's own
var knownRootDirs = map[string]bool{
	"documents": true, "extensions": true, "system": true, "Books": true, "koreader": true, "kpm": true, "audible": true,
	"voice": true, "fonts": true, "libkh": true, "mrpackages": true, "screenshots": true, "dictionaries": true, "kmc": true, "kforge": true,
}

func terminalDir(c Config) string {
	return filepath.Join(filepath.Dir(c.GingrList), "packages", "bin")
}

func listNames(dir string, keep func(os.DirEntry) bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !keep(e) {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// placesSnapshot records the names present in the places an install can add to.
func placesSnapshot(c Config) map[string]bool {
	snap := map[string]bool{}
	for _, n := range listNames(filepath.Join(c.UserRoot, "documents"), func(e os.DirEntry) bool {
		return !e.IsDir() && strings.HasSuffix(e.Name(), ".sh")
	}) {
		snap["lib:"+n] = true
	}
	for _, n := range listNames(filepath.Join(c.UserRoot, "extensions"), func(e os.DirEntry) bool { return e.IsDir() }) {
		snap["kual:"+n] = true
	}
	for _, n := range listNames(terminalDir(c), func(e os.DirEntry) bool { return !e.IsDir() }) {
		snap["term:"+n] = true
	}
	for _, n := range listNames(c.UserRoot, func(e os.DirEntry) bool {
		return e.IsDir() && !knownRootDirs[e.Name()] && !strings.HasSuffix(e.Name(), ".sdr")
	}) {
		snap["dir:"+n] = true
	}
	return snap
}

func diffPlaces(before, after map[string]bool) Where {
	w := Where{Checked: true, At: time.Now().UTC().Format(time.RFC3339)}
	var keys []string
	for k := range after {
		if !before[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		kind, name := k[:strings.Index(k, ":")], k[strings.Index(k, ":")+1:]
		switch kind {
		case "lib":
			w.Library = append(w.Library, name)
		case "kual":
			w.KUAL = append(w.KUAL, name)
		case "term":
			w.Terminal = append(w.Terminal, name)
		case "dir":
			w.Folders = append(w.Folders, name)
		}
	}
	return w
}

func loadWhere(c Config) map[string]Where {
	m := map[string]Where{}
	if data, err := os.ReadFile(c.path("where.json")); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func saveWhere(c Config, m map[string]Where) {
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.MkdirAll(c.DataDir, 0o755)
		_ = writeAtomic(c.path("where.json"), data)
	}
}

// recordWhere stores what an install added. An update that adds nothing new
// (it only overwrote files) keeps the earlier record.
func recordWhere(c Config, id string, before map[string]bool) {
	w := diffPlaces(before, placesSnapshot(c))
	m := loadWhere(c)
	if old, ok := m[id]; ok && !old.empty() && w.empty() {
		return
	}
	m[id] = w
	saveWhere(c, m)
}

func forgetWhere(c Config, id string) {
	m := loadWhere(c)
	if _, ok := m[id]; ok {
		delete(m, id)
		saveWhere(c, m)
	}
}

// applyWhere puts the records of installed packages into the catalog.
func applyWhere(c Config, cat *Catalog) {
	m := loadWhere(c)
	rec := loadKpkgInstalled(c)
	for i := range cat.Packages {
		p := &cat.Packages[i]
		if !p.Installed {
			continue
		}
		if r, ok := rec[p.ID]; ok && r.Kind == "zen" {
			p.Where = &Where{Checked: true, KOReader: []string{r.Module + ".koplugin"}}
			continue
		}
		if w, ok := m[p.ID]; ok {
			ww := w
			p.Where = &ww
		}
	}
}
