package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// whereConfig makes a Kindle whose fake `kpm -S` installs differently per package:
//
//	game -> a launcher in documents/      ext  -> a KUAL extension folder
//	tool -> a terminal command            dir  -> a new top-level folder
//	none -> nothing visible at all
func whereConfig(t *testing.T) Config {
	root := t.TempDir()
	c := Config{UserRoot: root, DataDir: filepath.Join(root, ".KFPM"), Backend: BackendGingr, Platform: "kindlehf",
		GingrList: filepath.Join(root, "kpm", "packages.list"), GingrMirror: filepath.Join(root, "mirrors.conf")}
	os.MkdirAll(filepath.Dir(c.GingrList), 0o755)
	os.WriteFile(c.GingrList, nil, 0o644)
	script := fmt.Sprintf(`#!/bin/sh
ROOT='%s'
LIST='%s'
case "$1" in
 -S)
  echo "$2" >> "$LIST"
  case "$2" in
   game) mkdir -p "$ROOT/documents"; printf '#!/bin/sh\n' > "$ROOT/documents/Game.sh"; mkdir -p "$ROOT/documents/Game.sdr" "$ROOT/documents/game_data"; echo x > "$ROOT/documents/.hidden.sh";;
   ext)  mkdir -p "$ROOT/extensions/MyExt"; echo '{}' > "$ROOT/extensions/MyExt/menu.json";;
   tool) mkdir -p "$ROOT/kpm/packages/bin"; echo x > "$ROOT/kpm/packages/bin/mytool";;
   dir)  mkdir -p "$ROOT/NewApp" "$ROOT/koreader" "$ROOT/kmc";;
  esac;;
 -R) grep -vx "$2" "$LIST" > "$LIST.t"; mv "$LIST.t" "$LIST";;
esac
exit 0
`, root, c.GingrList)
	p := filepath.Join(root, "fakekpm")
	os.WriteFile(p, []byte(script), 0o755)
	c.KPMPath = p
	// things that were already there must never be reported
	os.MkdirAll(filepath.Join(root, "documents"), 0o755)
	os.WriteFile(filepath.Join(root, "documents", "Old.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.MkdirAll(filepath.Join(root, "extensions", "OldExt"), 0o755)
	return c
}

func installAndWhere(t *testing.T, c Config, id string) Where {
	if err := change(c, "install", id, "packageInstallStatus", false); err != nil {
		t.Fatalf("install %s: %v", id, err)
	}
	return loadWhere(c)[id]
}

func TestWhereIsRecordedPerKindOfInstall(t *testing.T) {
	c := whereConfig(t)
	if w := installAndWhere(t, c, "game"); strings.Join(w.Library, ",") != "Game.sh" || len(w.KUAL)+len(w.Terminal)+len(w.Folders) != 0 {
		t.Errorf("game: %+v (Game.sdr, game_data, hidden and pre-existing files must not appear)", w)
	}
	if w := installAndWhere(t, c, "ext"); strings.Join(w.KUAL, ",") != "MyExt" || len(w.Library) != 0 {
		t.Errorf("ext: %+v (OldExt was already there)", w)
	}
	if w := installAndWhere(t, c, "tool"); strings.Join(w.Terminal, ",") != "mytool" || len(w.Library)+len(w.KUAL) != 0 {
		t.Errorf("tool: %+v", w)
	}
	if w := installAndWhere(t, c, "dir"); strings.Join(w.Folders, ",") != "NewApp" {
		t.Errorf("dir: %+v (koreader and kmc are known folders, not new apps)", w)
	}
	w := installAndWhere(t, c, "none")
	if !w.Checked || !w.empty() {
		t.Errorf("an install that adds nothing must say so: %+v", w)
	}
}

func TestWhereAppearsInTheCatalogOnlyForInstalledPackages(t *testing.T) {
	c := whereConfig(t)
	installAndWhere(t, c, "game")
	installAndWhere(t, c, "none")
	cat := buildCatalog(c, ReposFile{}, false)
	applyWhere(c, &cat)
	by := map[string]CatPackage{}
	for _, p := range cat.Packages {
		by[p.ID] = p
	}
	if g := by["game"]; g.Where == nil || len(g.Where.Library) != 1 {
		t.Errorf("game: %+v", g.Where)
	}
	if n := by["none"]; n.Where == nil || !n.Where.Checked || !n.Where.empty() {
		t.Errorf("none: %+v", n.Where)
	}
	// a package that is not installed has no record to show
	cat2 := Catalog{Packages: []CatPackage{{ID: "game", Installed: false}}}
	applyWhere(c, &cat2)
	if cat2.Packages[0].Where != nil {
		t.Error("not installed: no where")
	}
}

func TestReinstallKeepsTheEarlierRecordAndUninstallForgetsIt(t *testing.T) {
	c := whereConfig(t)
	installAndWhere(t, c, "game")
	// installing again only overwrites files, adds nothing new: keep what we knew
	if w := installAndWhere(t, c, "game"); strings.Join(w.Library, ",") != "Game.sh" {
		t.Errorf("record lost on reinstall: %+v", w)
	}
	if err := change(c, "uninstall", "game", "packageUninstallStatus", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadWhere(c)["game"]; ok {
		t.Error("uninstalling must forget the record")
	}
}

func TestPluginsReportKOReader(t *testing.T) {
	c := whereConfig(t)
	saveKpkgInstalled(c, map[string]KpkgRecord{"Zen--hello": {Kind: "zen", Module: "hello", Version: "1"}})
	cat := Catalog{Packages: []CatPackage{{ID: "Zen--hello", Installed: true}}}
	applyWhere(c, &cat)
	if w := cat.Packages[0].Where; w == nil || strings.Join(w.KOReader, ",") != "hello.koplugin" {
		t.Errorf("%+v", w)
	}
}
