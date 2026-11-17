package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const kpmTimeout = 5 * time.Minute

// runKPM runs `kpm -y <args...>` (KindleModding KPM).
func runKPM(c Config, verbose bool, args ...string) (string, error) {
	return runBin(c, verbose, nil, append([]string{"-y"}, args...)...)
}

// runBin runs c.KPMPath, appends everything it prints to last.log and (when
// verbose) mirrors it to stdout. env, if non-nil, replaces the environment.
func runBin(c Config, verbose bool, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kpmTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.KPMPath, args...)
	if env != nil {
		cmd.Env = env
	}

	var buf bytes.Buffer
	var w io.Writer = &buf
	if verbose {
		w = io.MultiWriter(&buf, os.Stdout)
	}
	cmd.Stdout = w
	cmd.Stderr = w

	err := cmd.Run()
	out := buf.String()
	appendLog(c, fmt.Sprintf("$ kpm %s\n%s", strings.Join(args, " "), out))

	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("kpm %s timed out after %s", args[0], kpmTimeout)
	}
	return out, err
}

// appendLog keeps last.log as a record of the most recent KFPM run, so the
// frontend can show why something failed. It is truncated by resetLog.
func appendLog(c Config, s string) {
	f, err := os.OpenFile(c.path("last.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	_, _ = f.WriteString(s)
}

func resetLog(c Config) {
	_ = os.MkdirAll(c.DataDir, 0o755)
	_ = os.WriteFile(c.path("last.log"), nil, 0o644)
}

// ---- installed packages ---------------------------------------------------

type Installed struct {
	ID          string
	Repository  string
	Name        string
	Version     Version
	AsDependent bool
}

const installedQuery = "SELECT id, IFNULL(repository,''), IFNULL(name,''), version_major, version_minor, version_patch, installed_as_dependency FROM installed_packages;"

// listInstalled reads KPM's own database through the sqlite3 CLI (already
// required by KindleForge.sh), which avoids a CGO build. If that is not
// possible it falls back to the package folder names (versions unknown).
func listInstalled(c Config) map[string]Installed {
	if isGingr(c) {
		return gingrInstalled(c)
	}
	out := map[string]Installed{}

	if _, err := os.Stat(c.DBPath); err == nil {
		if sq, err := exec.LookPath("sqlite3"); err == nil {
			raw, err := exec.Command(sq, "-separator", "\x1f", c.DBPath, installedQuery).Output()
			if err == nil {
				for _, line := range strings.Split(string(raw), "\n") {
					f := strings.Split(strings.TrimRight(line, "\r"), "\x1f")
					if len(f) < 7 || f[0] == "" {
						continue
					}
					n := func(s string) int { v, _ := strconv.Atoi(s); return v }
					out[f[0]] = Installed{
						ID: f[0], Repository: f[1], Name: f[2],
						Version:     Version{n(f[3]), n(f[4]), n(f[5])},
						AsDependent: f[6] == "1",
					}
				}
				return out
			}
			logf("sqlite3 query failed: %v", err)
		}
	}

	if entries, err := os.ReadDir(c.PkgDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && validID.MatchString(e.Name()) {
				out[e.Name()] = Installed{ID: e.Name(), Name: e.Name()}
			}
		}
	}
	return out
}

// ---- repository registration ---------------------------------------------

// ensureRepos makes sure every repo in repos.json is known to kpm itself, so
// that `kpm install` can resolve packages from it.
func ensureRepos(c Config, repos []Repo, verbose bool) {
	out, err := runKPM(c, verbose, "list-repo")
	if err != nil {
		return
	}
	for _, r := range repos {
		if strings.Contains(out, r.URL) {
			continue
		}
		if _, err := runKPM(c, verbose, "add-repo", r.URL); err != nil {
			logf("could not add repo %s: %v", r.URL, err)
		}
	}
}

func sortedInstalled(m map[string]Installed) []Installed {
	out := make([]Installed, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
