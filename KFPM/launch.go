package main

// Launching packages installed by KFPM's own installer.
//
// Packages made for KindleModding's KPM ship a launcher file (a "scriptlet" in
// documents/) whose one real line is `/var/local/kmc/bin/kpm launch <id>`.
// That program does not exist on a Kindle that has the other KPM, so the
// launcher fails with "kpm: not found". Two small pieces fix this:
//
//   - `kfpm -L <id>` does what `kpm launch` does: run the package's own
//     launch.sh from inside its folder, with the same environment.
//   - fixLaunchers rewrites that single line, in launcher files that belong to
//     packages KFPM installed, so they call `kfpm -L <id>` instead.
//
// Only a line that is exactly a `kpm launch <id>` call for an installed
// package is touched, only in top-level documents/*.sh files.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const maxLauncherSize = 256 << 10

func launchLineRe(id string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^([ \t]*)(?:/var/local/kmc/bin/)?kpm[ \t]+launch[ \t]+` + regexp.QuoteMeta(id) + `\b(.*)$`)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fixLaunchers repairs launcher files of installed (non-plugin) packages.
// Returns how many files were changed.
func fixLaunchers(c Config) int {
	if c.SelfPath == "" || c.UserRoot == "" {
		return 0
	}
	var ids []string
	for id, r := range loadKpkgInstalled(c) {
		if r.Kind != "zen" && validID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0
	}
	docs := filepath.Join(c.UserRoot, "documents")
	entries, err := os.ReadDir(docs)
	if err != nil {
		return 0
	}
	fixed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		p := filepath.Join(docs, e.Name())
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > maxLauncherSize {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := string(data)
		changed := false
		for _, id := range ids {
			re := launchLineRe(id)
			next := re.ReplaceAllStringFunc(text, func(line string) string {
				m := re.FindStringSubmatch(line)
				return m[1] + shellQuote(c.SelfPath) + " -L " + id + m[2]
			})
			if next != text {
				text, changed = next, true
			}
		}
		if !changed {
			continue
		}
		tmp := p + ".kf-tmp"
		if err := os.WriteFile(tmp, []byte(text), st.Mode().Perm()); err != nil {
			continue
		}
		if err := os.Rename(tmp, p); err != nil {
			_ = os.Remove(tmp)
			continue
		}
		appendLog(c, "repaired launcher "+e.Name()+" (it called kpm, which this Kindle does not have)")
		fixed++
	}
	return fixed
}

// launchPlan describes what `-L <id>` will run.
type launchPlan struct {
	Path string
	Argv []string
	Env  []string
	Dir  string
}

func planLaunch(c Config, id string, args []string) (launchPlan, error) {
	if !validID.MatchString(id) {
		return launchPlan{}, fmt.Errorf("invalid package id")
	}
	r, ok := loadKpkgInstalled(c)[id]
	if !ok || r.Kind == "zen" {
		return launchPlan{}, fmt.Errorf("%s was not installed by KindleForge", id)
	}
	dir := kpkgPkgDir(c, id)
	script := filepath.Join(dir, "launch.sh")
	if st, err := os.Stat(script); err != nil || st.IsDir() {
		return launchPlan{}, fmt.Errorf("%s has no launch.sh", id)
	}
	return launchPlan{Path: "/bin/sh", Argv: append([]string{"/bin/sh", "launch.sh"}, args...), Env: hookEnv(c, dir), Dir: dir}, nil
}

// launchExec replaces this process (as `kpm launch` does). A variable so tests
// can observe the plan instead of being replaced.
var launchExec = syscall.Exec

func runLaunch(c Config, id string, args []string) error {
	p, err := planLaunch(c, id, args)
	if err != nil {
		return err
	}
	if err := os.Chdir(p.Dir); err != nil {
		return err
	}
	return launchExec(p.Path, p.Argv, p.Env)
}
