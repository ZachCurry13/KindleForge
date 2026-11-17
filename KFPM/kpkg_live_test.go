package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Opt-in (KFPM_LIVE=1): downloads the real packages from the KindleTweaks
// repository and checks that they unpack and validate. It NEVER runs any hook.
func TestLiveKindleTweaksPackagesUnpackAndValidate(t *testing.T) {
	if os.Getenv("KFPM_LIVE") != "1" {
		t.Skip("set KFPM_LIVE=1 to download the real packages (no hooks are run)")
	}
	c := Config{DataDir: t.TempDir(), Platform: "kindlehf", UserRoot: t.TempDir()}
	rf := ReposFile{Repos: []Repo{{ID: "kindletweaks", URL: "https://kindletweaks.github.io/Repo/manifest.json"}}}
	srcs, st := kpkgSources(c, rf, true)
	if len(srcs) == 0 || st[0].Source != "network" {
		t.Fatalf("could not read the live manifest: %+v", st)
	}
	for _, plat := range []string{"kindlehf", "kindlepw2"} {
		c.Platform = plat
		srcs, _ = kpkgSources(c, rf, false)
		for id, s := range srcs {
			if s.Art == nil {
				t.Logf("%s: no artifact for %s (skipped)", id, plat)
				continue
			}
			u, err := resolveArtifactURL(s.ManifestURL, s.Art.URL)
			if err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			dir := t.TempDir()
			archive := filepath.Join(dir, id+".kpkg")
			if err := kpkgDownload(c, u, archive, ""); err != nil {
				t.Fatalf("%s/%s: %v", plat, id, err)
			}
			out := filepath.Join(dir, "x")
			if err := extractKpkg(archive, out); err != nil {
				t.Fatalf("%s/%s unpack: %v", plat, id, err)
			}
			if err := checkPackageManifest(out, id, s.Art.Version, plat); err != nil {
				t.Fatalf("%s/%s validate: %v", plat, id, err)
			}
			t.Logf("%-10s %-9s %s ok (%s)", id, plat, s.Art.Version, u)
		}
	}
}

// Opt-in (KFPM_LIVE=1): checks every KPM-format source shipped in the
// directory (catalog/config.json "sources"). Downloads each package for both
// device types, unpacks it with the strict extractor and validates its
// manifest. It NEVER runs a hook.
func TestLiveDirectoryKPMSourcesUnpack(t *testing.T) {
	if os.Getenv("KFPM_LIVE") != "1" {
		t.Skip("set KFPM_LIVE=1 (downloads real packages; nothing is executed)")
	}
	data, err := os.ReadFile(filepath.Join("..", "catalog", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range cfg.Sources {
		if src.Format != "" && src.Format != "kpm" {
			continue
		}
		c := Config{DataDir: t.TempDir(), UserRoot: t.TempDir()}
		rf := ReposFile{Repos: []Repo{src}}
		for _, plat := range []string{"kindlehf", "kindlepw2"} {
			c.Platform = plat
			srcs, st := kpkgSources(c, rf, plat == "kindlehf")
			if st[0].Source == "unavailable" {
				t.Errorf("%s: manifest unreadable", src.ID)
				break
			}
			for id, s := range srcs {
				if s.Art == nil {
					t.Logf("%-20s %-10s %-18s (no artifact for this device)", src.ID, plat, id)
					continue
				}
				u, err := resolveArtifactURL(s.ManifestURL, s.Art.URL)
				if err != nil {
					t.Errorf("%s/%s: %v", src.ID, id, err)
					continue
				}
				dir := t.TempDir()
				archive := filepath.Join(dir, "p.kpkg")
				if err := kpkgDownload(c, u, archive, s.Art.SHA256); err != nil {
					t.Errorf("%s/%s/%s download: %v", src.ID, plat, id, err)
					continue
				}
				out := filepath.Join(dir, "x")
				if err := extractKpkg(archive, out); err != nil {
					t.Errorf("%s/%s/%s unpack: %v", src.ID, plat, id, err)
					continue
				}
				if err := checkPackageManifest(out, id, s.Art.Version, plat); err != nil {
					t.Errorf("%s/%s/%s validate: %v", src.ID, plat, id, err)
					continue
				}
				t.Logf("%-20s %-10s %-18s %s ok", src.ID, plat, id, s.Art.Version)
			}
		}
	}
}
