package main

// Sources: every repository KindleForge can read, with a trust tier.
//
//	reviewed   shipped on by default; a maintainer has looked at it
//	community  listed in the directory but OFF until the user switches it on;
//	           found by the discovery scripts, not reviewed line by line
//	yours      added by the user; entirely their responsibility
//
// Packages run code on the Kindle, so nothing outside "reviewed" is ever
// enabled without an explicit action by the user.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
)

var validFormats = map[string]bool{"kpm": true, "zen": true}

func validSource(r Repo) bool {
	if !validID.MatchString(r.ID) || !strings.HasPrefix(r.URL, "https://") {
		return false
	}
	if r.Format != "" && !validFormats[r.Format] {
		return false
	}
	return len(r.URL) <= 300
}

// ---- URL validation (also protects the shell command the page sends) -------

var urlChars = regexp.MustCompile(`^[A-Za-z0-9:/._~%+-]+$`)

type urlT = url.URL

// validateSourceURLHook lets tests accept their own local servers.
var validateSourceURLHook func(raw string) (*urlT, error)

func parseURLForTest(raw string) (*urlT, error) { return url.Parse(strings.TrimSpace(raw)) }

// validateSourceURL checks a repository address typed by a user.
func validateSourceURL(raw string) (*url.URL, error) {
	if validateSourceURLHook != nil {
		return validateSourceURLHook(raw)
	}
	raw = strings.TrimSpace(raw)
	if len(raw) < 12 || len(raw) > 300 {
		return nil, fmt.Errorf("that does not look like a web address")
	}
	if !urlChars.MatchString(raw) {
		return nil, fmt.Errorf("the address may only use letters, digits and : / . _ ~ %% + -")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("the address must start with https://")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") {
		return nil, fmt.Errorf("that address points at this device or a private network")
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil, fmt.Errorf("use a website name, not a numeric address")
	}
	return u, nil
}

// ---- the user's own file (never overwritten by KFPM except through -g) -------

func readUserFile(c Config) ReposFile {
	var rf ReposFile
	if data, err := os.ReadFile(c.path("repos.json")); err == nil {
		_ = json.Unmarshal(data, &rf)
	}
	if rf.Categories == nil {
		rf.Categories = map[string]string{}
	}
	return rf
}

func writeUserFile(c Config, rf ReposFile) error {
	if rf.Repos == nil {
		rf.Repos = []Repo{}
	}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(c.DataDir, 0o755)
	return writeAtomic(c.path("repos.json"), append(data, '\n'))
}

func addUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func removeString(list []string, s string) []string {
	var out []string
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// setSourceEnabled switches a known source on or off by editing the user file.
func setSourceEnabled(c Config, id string, on bool) error {
	rf := loadRepos(c, false)
	var src *SourceInfo
	for i := range rf.Directory {
		if rf.Directory[i].ID == id {
			src = &rf.Directory[i]
		}
	}
	if src == nil {
		return fmt.Errorf("no source called %q", id)
	}
	user := readUserFile(c)
	isDefaultOn := false
	base := loadCatalogDefinition(c, false)
	for _, r := range base.Repos {
		if r.ID == id {
			isDefaultOn = true
		}
	}
	for _, r := range base.Sources {
		if r.ID == id && r.Default {
			isDefaultOn = true
		}
	}
	switch {
	case isDefaultOn || src.User:
		if on {
			user.Disable = removeString(user.Disable, id)
		} else {
			user.Disable = addUnique(user.Disable, id)
		}
	default: // opt-in source
		if on {
			user.Enable = addUnique(user.Enable, id)
			user.Disable = removeString(user.Disable, id)
		} else {
			user.Enable = removeString(user.Enable, id)
		}
	}
	return writeUserFile(c, user)
}

func removeUserSource(c Config, id string) error {
	user := readUserFile(c)
	found := false
	var keep []Repo
	for _, r := range user.Repos {
		if r.ID == id {
			found = true
			continue
		}
		keep = append(keep, r)
	}
	if !found {
		return fmt.Errorf("%q is not a repository you added", id)
	}
	user.Repos = keep
	user.Disable = removeString(user.Disable, id)
	return writeUserFile(c, user)
}

// ---- checking an address before adding it ----------------------------------

type SourcePreview struct {
	URL      string   `json:"url"`
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	Format   string   `json:"format,omitempty"`
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name,omitempty"`
	Host     string   `json:"host,omitempty"`
	Packages int      `json:"packages"`
	Sample   []string `json:"sample,omitempty"`
	Exists   bool     `json:"exists,omitempty"` // already in the list
}

func slug(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// checkSource downloads an address and works out what kind of repository it is.
func checkSource(c Config, raw string) SourcePreview {
	p := SourcePreview{URL: strings.TrimSpace(raw)}
	u, err := validateSourceURL(raw)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	p.Host = u.Hostname()
	resp, err := httpClient.Get(u.String())
	if err != nil {
		p.Error = "could not reach it (is the Kindle online?)"
		return p
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		p.Error = fmt.Sprintf("the server answered HTTP %d", resp.StatusCode)
		return p
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		p.Error = "the download was interrupted"
		return p
	}
	var probe struct {
		ManifestVersion int             `json:"manifest_version"`
		SchemaVersion   json.RawMessage `json:"schema_version"`
		ID              string          `json:"id"`
		Name            string          `json:"name"`
		Repo            struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"repo"`
		Packages json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(data, &probe) != nil || len(probe.Packages) == 0 {
		p.Error = "that is not a package repository (no package list found)"
		return p
	}
	switch {
	case probe.ManifestVersion > 0:
		m, perr := parseManifest(data)
		if perr != nil {
			p.Error = "it looks like a KPM repository but could not be read"
			return p
		}
		p.Format, p.ID, p.Name, p.Packages = "kpm", probe.ID, probe.Name, len(m.Packages)
		for _, id := range sortedKeys(m.Packages) {
			if len(p.Sample) < 5 {
				p.Sample = append(p.Sample, strings.TrimSpace(m.Packages[id].Name))
			}
		}
	case len(probe.SchemaVersion) > 0:
		zm, perr := parseZenManifest(data)
		if perr != nil {
			p.Error = "it looks like a ZenPM repository but could not be read"
			return p
		}
		p.Format, p.ID, p.Name, p.Packages = "zen", zm.Repo.ID, zm.Repo.Name, len(zm.Packages)
		for _, zp := range zm.Packages {
			if len(p.Sample) < 5 {
				p.Sample = append(p.Sample, zp.Name)
			}
		}
	default:
		p.Error = "unknown repository format"
		return p
	}
	if p.Name == "" {
		p.Name = p.Host
	}
	p.Name = shortName(p.Name)
	for i := range p.Sample {
		p.Sample[i] = shortName(p.Sample[i])
	}
	rf := loadRepos(c, false)
	for _, s := range rf.Directory {
		if s.URL == u.String() {
			p.Exists = true
		}
	}
	p.OK = true
	return p
}

func writePreview(c Config, p SourcePreview) {
	if data, err := json.MarshalIndent(p, "", "  "); err == nil {
		_ = os.MkdirAll(c.DataDir, 0o755)
		_ = writeAtomic(c.path("source-preview.json"), data)
	}
}

// addUserSource validates and records a repository the user typed in.
func addUserSource(c Config, raw string) (string, error) {
	p := checkSource(c, raw)
	writePreview(c, p)
	if !p.OK {
		return "", fmt.Errorf("%s", p.Error)
	}
	if p.Exists {
		return "", fmt.Errorf("that repository is already in the list")
	}
	user := readUserFile(c)
	rf := loadRepos(c, false)
	taken := map[string]bool{}
	for _, s := range rf.Directory {
		taken[s.ID] = true
	}
	base := "my-" + slug(firstNonEmpty(p.ID, p.Name, p.Host))
	if base == "my-" {
		base = "my-repo"
	}
	id := base
	for i := 2; taken[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	user.Repos = append(user.Repos, Repo{ID: id, URL: strings.TrimSpace(raw), Format: p.Format, Name: p.Name, Trust: "yours"})
	return id, writeUserFile(c, user)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// runSources is the entry point for `kfpm -g <list|enable|disable|remove|check|add> [arg]`.
func runSources(c Config, args []string, verbose bool) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: -g list | enable <id> | disable <id> | remove <id> | check <url> | add <url>")
	}
	switch args[0] {
	case "list":
		return refreshCatalog(c, false, verbose)
	case "check":
		if len(args) < 2 {
			return fmt.Errorf("check needs an address")
		}
		p := checkSource(c, args[1])
		writePreview(c, p)
		if !p.OK {
			return fmt.Errorf("%s", p.Error)
		}
		return nil
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("add needs an address")
		}
		if _, err := addUserSource(c, args[1]); err != nil {
			return err
		}
	case "enable", "disable":
		if len(args) < 2 || !validID.MatchString(args[1]) {
			return fmt.Errorf("%s needs a source id", args[0])
		}
		if err := setSourceEnabled(c, args[1], args[0] == "enable"); err != nil {
			return err
		}
	case "remove":
		if len(args) < 2 || !validID.MatchString(args[1]) {
			return fmt.Errorf("remove needs a source id")
		}
		if err := removeUserSource(c, args[1]); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown sources command %q", args[0])
	}
	// the list of sources changed: fetch what is now switched on
	if err := refreshCatalog(c, true, verbose); err != nil {
		appendLog(c, "refresh after changing sources: "+err.Error()) // saved either way
	}
	return nil
}

// annotateSources fills the directory with what the last fetch found.
func annotateSources(rf ReposFile, cat *Catalog) {
	status := map[string]RepoStatus{}
	for _, r := range cat.Repos {
		status[r.ID] = r
	}
	infos := make([]SourceInfo, len(rf.Directory))
	copy(infos, rf.Directory)
	for i := range infos {
		if st, ok := status[infos[i].ID]; ok {
			infos[i].Packages, infos[i].Fetched = st.Packages, st.Source
		}
	}
	sort.SliceStable(infos, func(i, j int) bool {
		order := map[string]int{"reviewed": 0, "yours": 1, "community": 2}
		return order[infos[i].Trust] < order[infos[j].Trust]
	})
	cat.Sources = infos
}
