package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mk(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("data-"+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func exists_(p string) bool { _, err := os.Lstat(p); return err == nil }

// fakeKindle builds a user area like the one in the screenshots.
func fakeKindle(t *testing.T) Config {
	root := filepath.Join(t.TempDir(), "us")
	mk(t, root,
		// crash dumps (documents + root)
		"documents/pillowd_2264_001-juno_18050001_sangria_bellatrix4-455680_crash_Jul.zip",
		"documents/mesquite_2958_001-juno_18050001_sangria_crash_J/core.txt",
		"pillowd_9_001-x_crash_Jul.zip",
		// real user content that must survive
		"documents/Book A.azw3", "documents/Book A.sdr/Book A.apnx",
		"documents/My Clippings.txt", "documents/My Clippings.sdr/x",
		"documents/updateblock.sh", "documents/updateblock.sh.sdr/x",
		"documents/nested/Deep.kfx", "documents/nested/Deep.sdr/x",
		"documents/kwordle.sh", "documents/KUAL.kual", "documents/KindleForge.sh",
		// orphans
		"documents/Orphan_x.sdr/progress", "documents/nested/Gone.sdr/progress",
		// old KindleForge
		"KF-Update-Temp/leftover.zip", ".KFPM/installed.txt",
		// dumps, reports
		"Indexer_Dump_1762403439415.txt", "kfpm-diag.txt", "kfpm-net.txt",
		// untouchables
		"Books/novel.azw3", "system/thumbnails/a.jpg", "koreader/settings.lua", "extensions/kpm/menu.json",
		"mrpackages/update.bin", "screenshots/shot1.png", "adbreak.log",
	)
	data := filepath.Join(root, ".KFPM")
	mk(t, data, "update/pkg.zip", "x.tmp", "cache/y.tmp", "repos.json")
	return Config{UserRoot: root, DataDir: data}
}

func counts(rep CleanupReport) map[string]int {
	m := map[string]int{}
	for _, c := range rep.Categories {
		m[c.ID] = c.Count
	}
	return m
}

func TestScanFindsOnlyAllowlisted(t *testing.T) {
	c := fakeKindle(t)
	before := snapshot(t, c.UserRoot)
	rep := buildCleanupReport(c, nil, true)
	m := counts(rep)
	want := map[string]int{"crash": 3, "oldforge": 2, "dumps": 1, "diag": 2, "kfupdate": 3, "orphansdr": 2}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s: got %d want %d (%v)", k, m[k], v, m)
		}
	}
	// scanning must change nothing
	if after := snapshot(t, c.UserRoot); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Error("scan modified the filesystem")
	}
	if len(rep.Biggest) == 0 || rep.FreeBytes <= 0 {
		t.Errorf("biggest=%v free=%d", rep.Biggest, rep.FreeBytes)
	}
	for _, b := range rep.Biggest {
		if b.Path == "system" {
			t.Error("system folder listed")
		}
	}
}

func snapshot(t *testing.T, root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		out = append(out, p)
		return nil
	})
	return out
}

var mustSurvive = []string{
	"documents/Book A.azw3", "documents/Book A.sdr/Book A.apnx", "documents/My Clippings.txt",
	"documents/My Clippings.sdr/x", "documents/updateblock.sh", "documents/updateblock.sh.sdr/x",
	"documents/nested/Deep.kfx", "documents/nested/Deep.sdr/x", "documents/kwordle.sh", "documents/KUAL.kual",
	"documents/KindleForge.sh", "Books/novel.azw3", "system/thumbnails/a.jpg", "koreader/settings.lua",
	"extensions/kpm/menu.json", "mrpackages/update.bin", "screenshots/shot1.png", "adbreak.log", ".KFPM/repos.json",
}

func TestApplyMovesSelectedOnlyAndSpares(t *testing.T) {
	c := fakeKindle(t)
	res := applyCleanup(c, []string{"crash", "oldforge", "dumps", "diag", "kfupdate"}) // orphansdr NOT chosen
	if res.Count != 11 || res.Skipped != 0 || len(res.Errors) != 0 {
		t.Fatalf("result %+v", res)
	}
	for _, f := range mustSurvive {
		if !exists_(filepath.Join(c.UserRoot, f)) {
			t.Errorf("destroyed %s", f)
		}
	}
	for _, f := range []string{"documents/Orphan_x.sdr", "documents/nested/Gone.sdr"} {
		if !exists_(filepath.Join(c.UserRoot, f)) {
			t.Errorf("orphan %s moved though not selected", f)
		}
	}
	for _, f := range []string{"Indexer_Dump_1762403439415.txt", "KF-Update-Temp", "kfpm-diag.txt", "pillowd_9_001-x_crash_Jul.zip"} {
		if exists_(filepath.Join(c.UserRoot, f)) {
			t.Errorf("%s was not moved", f)
		}
	}
	if len(loadTrash(c)) != 11 {
		t.Errorf("trash entries = %d", len(loadTrash(c)))
	}
}

func TestRestoreAndEmpty(t *testing.T) {
	c := fakeKindle(t)
	applyCleanup(c, []string{"crash", "dumps"})
	// something new takes an original location: must not be overwritten
	os.WriteFile(filepath.Join(c.UserRoot, "Indexer_Dump_1762403439415.txt"), []byte("NEW"), 0o644)

	res := restoreTrash(c)
	if res.Count != 3 || res.Skipped != 1 {
		t.Fatalf("restore %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(c.UserRoot, "Indexer_Dump_1762403439415.txt")); string(b) != "NEW" {
		t.Error("restore overwrote an existing file")
	}
	if !exists_(filepath.Join(c.UserRoot, "documents/mesquite_2958_001-juno_18050001_sangria_crash_J/core.txt")) {
		t.Error("crash dir not restored")
	}
	if left := loadTrash(c); len(left) != 1 {
		t.Errorf("conflicting item should stay in trash, got %d", len(left))
	}
	e := emptyTrash(c)
	if e.Count != 1 || exists_(c.path("trash")) {
		t.Errorf("empty: %+v", e)
	}
}

func TestRefusesBadInput(t *testing.T) {
	c := fakeKindle(t)
	for _, bad := range [][]string{{"../etc"}, {"crash", "everything"}, {""}, {"documents"}} {
		r := applyCleanup(c, bad)
		if r.Count != 0 || len(r.Errors) == 0 {
			t.Errorf("%v accepted: %+v", bad, r)
		}
	}
	if !exists_(filepath.Join(c.UserRoot, "KF-Update-Temp")) {
		t.Error("something moved on bad input")
	}
}

func TestSymlinksAndProtectedAreNeverMoved(t *testing.T) {
	c := fakeKindle(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "precious"), []byte("x"), 0o644)
	// a symlink with a crash-dump name pointing outside the user area
	if err := os.Symlink(outside, filepath.Join(c.UserRoot, "documents", "evil_1_x_crash_y")); err != nil {
		t.Skip("symlinks unsupported")
	}
	res := applyCleanup(c, []string{"crash"})
	if !exists_(filepath.Join(outside, "precious")) {
		t.Fatal("followed a symlink out of the user area")
	}
	if !exists_(filepath.Join(c.UserRoot, "documents", "evil_1_x_crash_y")) {
		t.Error("symlink itself should be left alone")
	}
	_ = res
	for _, p := range []string{"system", "Books", "documents", ".KFPM", "extensions", "koreader", "mrpackages", "screenshots", "system/thumbnails"} {
		if allowedPath(c, filepath.Join(c.UserRoot, p)) {
			t.Errorf("%s must not be allowed", p)
		}
	}
	if allowedPath(c, "/etc/passwd") || allowedPath(c, filepath.Join(c.UserRoot, "..", "x")) {
		t.Error("paths outside the roots allowed")
	}
}

func TestTamperedTrashManifestCannotMoveFilesElsewhere(t *testing.T) {
	c := fakeKindle(t)
	applyCleanup(c, []string{"dumps"})
	tr := loadTrash(c)
	outside := filepath.Join(t.TempDir(), "pwned")
	tr[0].Original = outside
	saveTrash(c, tr)
	res := restoreTrash(c)
	if exists_(outside) || res.Count != 0 || res.Skipped != 1 {
		t.Errorf("tampered manifest honoured: %+v", res)
	}
}

func TestRunCleanupWritesReport(t *testing.T) {
	c := fakeKindle(t)
	if err := runCleanup(c, []string{"scan"}); err != nil {
		t.Fatal(err)
	}
	if err := runCleanup(c, []string{"apply", "crash"}); err != nil {
		t.Fatal(err)
	}
	var rep CleanupReport
	data, _ := os.ReadFile(c.path("cleanup.json"))
	if json.Unmarshal(data, &rep) != nil {
		t.Fatal("bad report json")
	}
	if rep.Last == nil || rep.Last.Count != 3 || rep.TrashItems != 3 || counts(rep)["crash"] != 0 {
		t.Errorf("report after apply: %+v", rep.Last)
	}
	if len(rep.Biggest) == 0 {
		t.Error("the size list from the last scan should be kept")
	}
	if runCleanup(c, []string{"apply"}) == nil || runCleanup(c, []string{"bogus"}) == nil || runCleanup(c, nil) == nil {
		t.Error("bad commands accepted")
	}
	if err := runCleanup(c, []string{"restore"}); err != nil {
		t.Fatal(err)
	}
	if err := runCleanup(c, []string{"empty"}); err != nil {
		t.Fatal(err)
	}
}
