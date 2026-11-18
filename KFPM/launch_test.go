package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The launcher exactly as the KUALA package ships it.
const kualaScriptlet = "#!/bin/sh\n# Name: KUALA\n# Author: Kaspar\n\n/var/local/kmc/bin/kpm launch kuala\n"

func launchConfig(t *testing.T, ids ...string) Config {
	root := t.TempDir()
	c := Config{UserRoot: root, DataDir: filepath.Join(root, ".KFPM"), SelfPath: "/var/local/mesquite/KindleForge/binaries/KFPM", Platform: "kindlehf"}
	os.MkdirAll(filepath.Join(root, "documents"), 0o755)
	rec := map[string]KpkgRecord{}
	for _, id := range ids {
		rec[id] = KpkgRecord{Version: "1.0.0", Repo: "kindletweaks"}
	}
	saveKpkgInstalled(c, rec)
	return c
}

func writeDoc(t *testing.T, c Config, name, body string, mode os.FileMode) string {
	p := filepath.Join(c.UserRoot, "documents", name)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFixLauncherRewritesTheKualaScriptlet(t *testing.T) {
	c := launchConfig(t, "kuala")
	p := writeDoc(t, c, "kuala.sh", kualaScriptlet, 0o755)
	if n := fixLaunchers(c); n != 1 {
		t.Fatalf("fixed %d files", n)
	}
	got := read(t, p)
	want := "#!/bin/sh\n# Name: KUALA\n# Author: Kaspar\n\n'/var/local/mesquite/KindleForge/binaries/KFPM' -L kuala\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "/var/local/kmc/bin/kpm") {
		t.Error("the call to the missing program must be gone")
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o755 {
		t.Errorf("mode changed: %v", st.Mode())
	}
	if n := fixLaunchers(c); n != 0 || read(t, p) != want {
		t.Error("a second run must change nothing")
	}
	if exists_(p + ".kf-tmp") {
		t.Error("temporary file left behind")
	}
}

func TestFixLauncherOnlyTouchesWhatItShould(t *testing.T) {
	c := launchConfig(t, "kuala", "gargoyle")
	untouched := map[string]string{
		"echo.sh":       "#!/bin/sh\necho \"/var/local/kmc/bin/kpm launch kuala\"\n", // not a call
		"comment.sh":    "#!/bin/sh\n# /var/local/kmc/bin/kpm launch kuala\n",        // a comment
		"other.sh":      "#!/bin/sh\n/var/local/kmc/bin/kpm launch notinstalled\n",   // not ours
		"prefix.sh":     "#!/bin/sh\n/var/local/kmc/bin/kpm launch kualaextra\n",     // id boundary
		"notes.txt":     "/var/local/kmc/bin/kpm launch kuala\n",                     // not a .sh
		"kpmlist.sh":    "#!/bin/sh\n/var/local/kmc/bin/kpm list\n",                  // different command
		"already-ok.sh": "#!/bin/sh\n'/x/KFPM' -L kuala\n",                           // already fixed
		"substring.sh":  "#!/bin/sh\nmykpm launch kuala\n",                           // not our word
		"midline.sh":    "#!/bin/sh\nfoo /var/local/kmc/bin/kpm launch kuala\n",      // not at line start
	}
	for name, body := range untouched {
		writeDoc(t, c, name, body, 0o755)
	}
	// subfolders, links and oversized files are never touched
	os.MkdirAll(filepath.Join(c.UserRoot, "documents", "sub"), 0o755)
	os.WriteFile(filepath.Join(c.UserRoot, "documents", "sub", "deep.sh"), []byte(kualaScriptlet), 0o755)
	outside := filepath.Join(t.TempDir(), "target.sh")
	os.WriteFile(outside, []byte(kualaScriptlet), 0o755)
	os.Symlink(outside, filepath.Join(c.UserRoot, "documents", "link.sh"))
	big := kualaScriptlet + strings.Repeat("# padding\n", 40000)
	writeDoc(t, c, "huge.sh", big, 0o755)

	// two installed packages in one file, args preserved, indentation kept
	multi := writeDoc(t, c, "multi.sh", "#!/bin/sh\n  /var/local/kmc/bin/kpm launch gargoyle \"$@\"\nkpm launch kuala --fast\n", 0o755)

	if n := fixLaunchers(c); n != 1 {
		t.Errorf("only multi.sh should be fixed, fixed %d", n)
	}
	for name, body := range untouched {
		if read(t, filepath.Join(c.UserRoot, "documents", name)) != body {
			t.Errorf("%s was modified", name)
		}
	}
	if read(t, filepath.Join(c.UserRoot, "documents", "sub", "deep.sh")) != kualaScriptlet || read(t, outside) != kualaScriptlet || read(t, filepath.Join(c.UserRoot, "documents", "huge.sh")) != big {
		t.Error("a subfolder, link target or oversized file was modified")
	}
	want := "#!/bin/sh\n  '/var/local/mesquite/KindleForge/binaries/KFPM' -L gargoyle \"$@\"\n'/var/local/mesquite/KindleForge/binaries/KFPM' -L kuala --fast\n"
	if got := read(t, multi); got != want {
		t.Errorf("multi:\n%s", got)
	}
}

func TestFixLauncherQuotesUnusualPathsAndIgnoresPlugins(t *testing.T) {
	c := launchConfig(t, "kuala")
	c.SelfPath = "/odd path/it's here/KFPM"
	p := writeDoc(t, c, "kuala.sh", kualaScriptlet, 0o755)
	fixLaunchers(c)
	if !strings.Contains(read(t, p), `'/odd path/it'\''s here/KFPM' -L kuala`) {
		t.Errorf("not quoted safely: %s", read(t, p))
	}
	// plugins (kind zen) have no launcher; no installed packages means no work
	c2 := launchConfig(t)
	saveKpkgInstalled(c2, map[string]KpkgRecord{"repo--p": {Kind: "zen", Module: "p"}})
	q := writeDoc(t, c2, "x.sh", "#!/bin/sh\nkpm launch repo--p\n", 0o755)
	if fixLaunchers(c2) != 0 || read(t, q) != "#!/bin/sh\nkpm launch repo--p\n" {
		t.Error("plugins must not be touched")
	}
	c3 := launchConfig(t, "kuala")
	c3.SelfPath = ""
	r := writeDoc(t, c3, "kuala.sh", kualaScriptlet, 0o755)
	if fixLaunchers(c3) != 0 || read(t, r) != kualaScriptlet {
		t.Error("without a known program path nothing is rewritten")
	}
}

func installPackageDir(t *testing.T, c Config, id, launch string) string {
	dir := kpkgPkgDir(c, id)
	os.MkdirAll(dir, 0o755)
	if launch != "" {
		os.WriteFile(filepath.Join(dir, "launch.sh"), []byte(launch), 0o755)
	}
	return dir
}

func TestLaunchPlanAndRealExecution(t *testing.T) {
	c := launchConfig(t, "demo")
	dir := installPackageDir(t, c, "demo", "#!/bin/sh\necho \"$KPM_PLATFORM|$PWD|$1|$2\" > launched.txt\n")

	var gotPath string
	var gotArgv []string
	old := launchExec
	launchExec = func(path string, argv []string, env []string) error { gotPath, gotArgv = path, argv; return nil }
	defer func() { launchExec = old }()
	if err := runLaunch(c, "demo", []string{"a", "b c"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/bin/sh" || strings.Join(gotArgv, ",") != "/bin/sh,launch.sh,a,b c" {
		t.Errorf("plan: %s %v", gotPath, gotArgv)
	}

	// and for real: the script runs inside the package folder with KPM's environment
	p, err := planLaunch(c, "demo", []string{"one", "two words"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(p.Path, p.Argv[1:]...)
	cmd.Dir, cmd.Env = p.Dir, p.Env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := strings.TrimSpace(read(t, filepath.Join(dir, "launched.txt"))); got != "kindlehf|"+dir+"|one|two words" {
		t.Errorf("ran as: %q", got)
	}
}

func TestLaunchRefusals(t *testing.T) {
	c := launchConfig(t, "demo", "nolaunch")
	installPackageDir(t, c, "demo", "#!/bin/sh\n")
	installPackageDir(t, c, "nolaunch", "")
	saveKpkgInstalled(c, map[string]KpkgRecord{"demo": {}, "nolaunch": {}, "plug": {Kind: "zen", Module: "p"}})
	for _, id := range []string{"missing", "nolaunch", "plug", "../demo", "a b", ""} {
		if _, err := planLaunch(c, id, nil); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
	if _, err := planLaunch(c, "demo", nil); err != nil {
		t.Errorf("a normal package should launch: %v", err)
	}
}

func TestInstallThenRefreshRepairsTheLauncher(t *testing.T) {
	rs := newRepo(t)
	mf := `{"id":"demo","version":[1,0,0],"supported_platforms":["kindlehf"]}`
	hook := "#!/bin/sh\nmkdir -p ../../documents\ncp ./scriptlets/demo.sh ../../documents/demo.sh\n" // what KUALA's install.sh does, relative to the package folder
	rs.pkgs[rs.url] = makeKpkg(t, []tarEntry{
		{name: "manifest.json", body: mf},
		{name: "install.sh", body: hook, mode: 0o755},
		{name: "launch.sh", body: "#!/bin/sh\necho launched\n", mode: 0o755},
		{name: "scriptlets/", typeflag: 53, mode: 0o755}, // directory
		{name: "scriptlets/demo.sh", body: "#!/bin/sh\n# Name: Demo\n\n/var/local/kmc/bin/kpm launch demo\n", mode: 0o755},
	})
	c, rf := kpkgConfig(t, rs)
	c.UserRoot = kpkgRoot(c) // so the hook's documents folder is the one the repair looks in
	c.SelfPath = "/path/to/KFPM"
	if err := kpkgInstall(c, rf, "demo", false); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(c.UserRoot, "documents", "demo.sh")
	if !strings.Contains(read(t, doc), "/var/local/kmc/bin/kpm launch demo") {
		t.Fatal("test setup: the hook should have installed the original launcher")
	}
	// any later operation (here a local refresh) repairs it, with no reinstall
	if err := refreshCatalog(c, false, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, doc); !strings.Contains(got, "'/path/to/KFPM' -L demo") || strings.Contains(got, "kmc/bin/kpm") {
		t.Errorf("not repaired:\n%s", got)
	}
}
