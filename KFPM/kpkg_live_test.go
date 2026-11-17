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
			if err := kpkgDownload(c, u, archive); err != nil {
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
