package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in (KFPM_LIVE=1). Downloads a sample of REAL plugins from the ZenLabs
// repository via GitHub, verifies each checksum and unpacks it with the strict
// extractor. It only unpacks files: no plugin code is ever run.
func TestLiveZenLabsPluginsUnpack(t *testing.T) {
	if os.Getenv("KFPM_LIVE") != "1" {
		t.Skip("set KFPM_LIVE=1 to download real plugins (nothing is executed)")
	}
	c := Config{DataDir: t.TempDir(), UserRoot: t.TempDir()}
	os.MkdirAll(filepath.Join(c.UserRoot, "koreader", "plugins"), 0o755)
	rf := ReposFile{Repos: []Repo{{ID: "ZenLabs", URL: "https://repo.zen-labs.org/manifest.json", Format: "zen"}}}
	srcs, st := zenSources(c, rf, true)
	if st[0].Source != "network" || len(srcs) < 50 {
		t.Fatalf("could not read the live repository: %+v (%d eligible)", st, len(srcs))
	}
	t.Logf("%d of %d packages are offered as installable plugins", len(srcs), st[0].Packages)

	ids := sortedKeys(srcs)
	step := len(ids) / 30
	if step < 1 {
		step = 1
	}
	ok, unsupported, failed := 0, map[string]string{}, map[string]string{}
	for i := 0; i < len(ids); i += step {
		id := ids[i]
		s := srcs[id]
		asset, err := chooseZenAsset(c, s)
		if err != nil {
			failed[id] = err.Error()
			continue
		}
		if !zenAssetOK(asset.URL) || !sha256DigestRe.MatchString(asset.Digest) {
			failed[id] = "url/digest rejected: " + asset.URL
			continue
		}
		dir := t.TempDir()
		archive := filepath.Join(dir, "p.zip")
		if err := kpkgDownload(c, asset.URL, archive, strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")); err != nil {
			failed[id] = err.Error()
			continue
		}
		if _, err := extractKoPlugin(archive, filepath.Join(dir, "out")); err != nil {
			unsupported[id] = err.Error()
			continue
		}
		ok++
	}
	t.Logf("sampled %d: %d unpack cleanly, %d refused by the strict rules, %d could not be fetched", ok+len(unsupported)+len(failed), ok, len(unsupported), len(failed))
	for id, why := range unsupported {
		t.Logf("  refused  %-40s %s", id, shortName(why))
	}
	for id, why := range failed {
		t.Logf("  fetch    %-40s %s", id, shortName(why))
	}
	if ok == 0 {
		t.Fatal(fmt.Sprintf("nothing unpacked; failures: %v %v", unsupported, failed))
	}
}
