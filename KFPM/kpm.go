package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// runBin runs c.KPMPath, appends everything it prints to last-log.txt and (when
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

// Logging. Two files:
//   <data>/last-log.txt        the latest run only; the app's "View log" shows it
//   <root>/kindleforge-log.txt a visible, size-limited HISTORY at the top level
//                              of the Kindle's storage, so it can be pulled over
//                              USB without hunting in a hidden folder.
// Both are plain text. Clean Up can move them to the trash.

const (
	historyName    = "kindleforge-log.txt"
	historyMaxSize = 256 << 10
	historyKeep    = 128 << 10
)

func historyPath(c Config) string {
	if c.UserRoot == "" {
		return ""
	}
	return filepath.Join(c.UserRoot, historyName)
}

func appendFile(path string, b []byte) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(b)
}

// logBytes records output in both log files.
func logBytes(c Config, b []byte) {
	appendFile(c.path("last-log.txt"), b)
	appendFile(historyPath(c), b)
}

// appendLog keeps last-log.txt as a record of the most recent KFPM run, so the
// frontend can show why something failed. It is truncated by resetLog.
func appendLog(c Config, s string) {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	logBytes(c, []byte(s))
}

// resetLog starts a new run: the latest-run log is emptied, and the history
// gets a header line (and is trimmed if it has grown too big).
func resetLog(c Config) {
	_ = os.MkdirAll(c.DataDir, 0o755)
	_ = os.WriteFile(c.path("last-log.txt"), nil, 0o644)
	if h := historyPath(c); h != "" {
		trimHistory(h)
		header := fmt.Sprintf("\n===== %s  kfpm %s =====\n", time.Now().Format("2006-01-02 15:04:05"), strings.Join(os.Args[1:], " "))
		appendFile(h, []byte(header))
	}
}

// trimHistory keeps only the newest part of an oversized history file.
func trimHistory(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= historyMaxSize {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	data = data[len(data)-historyKeep:]
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		data = data[i+1:] // start at a line boundary
	}
	_ = os.WriteFile(path, append([]byte("(older entries trimmed)\n"), data...), 0o644)
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
		out := gingrInstalled(c)
		for id, r := range loadKpkgInstalled(c) { // packages KFPM installed itself
			out[id] = Installed{ID: id, Name: id, Repository: r.Repo, Version: parseVersionString(r.Version)}
		}
		return out
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
