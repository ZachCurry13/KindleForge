/*
   KFPM
   KindleForge Package Manager

   Thin wrapper around the native KPM (Kindle Package Manager) CLI. KFPM owns
   the storefront catalog (repo manifests + offline cache) and reports results
   to the frontend through lipc properties; KPM does the real package work.
*/

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[KFPM] "+format+"\n", a...)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		help()
		return
	}

	cfg := loadConfig()
	_ = os.MkdirAll(cfg.DataDir, 0o755)

	verbose := false
	for _, a := range args[1:] {
		if a == "-v" {
			verbose = true
		}
	}
	// Package id is the first non-flag argument after the command.
	pkgArg := func() (string, bool) {
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fmt.Printf("[KFPM] %s requires a package id\n", args[0])
			return "", false
		}
		if !validID.MatchString(args[1]) {
			fmt.Printf("[KFPM] invalid package id %q\n", args[1])
			return "", false
		}
		return args[1], true
	}

	switch args[0] {
	case "-i", "-u":
		id, ok := pkgArg()
		if !ok {
			return
		}
		// `kpm install` (re)installs/upgrades, so install and update share a path.
		os.Exit(exitCode(change(cfg, "install", id, "packageInstallStatus", verbose)))

	case "-r":
		id, ok := pkgArg()
		if !ok {
			return
		}
		os.Exit(exitCode(change(cfg, "uninstall", id, "packageUninstallStatus", verbose)))

	case "-U":
		resetLog(cfg)
		if isGingr(cfg) {
			// gingrspacecadet/kpm tracks no versions, so there is nothing to upgrade.
			fmt.Println("[KFPM] Update All is not supported by this KPM (it does not track package versions).")
			appendLog(cfg, "update all: not supported by gingrspacecadet/kpm")
			setStatus("packageInstallStatus", "failure")
			os.Exit(1)
		}
		if _, err := runKPM(cfg, verbose, "upgrade"); err != nil {
			fmt.Printf("[KFPM] Upgrade failed: %v (see %s)\n", err, cfg.path("last.log"))
			refreshCatalog(cfg, false, verbose) // catalog first: the status event triggers a reload
			setStatus("packageInstallStatus", "failure")
			os.Exit(1)
		}
		fmt.Println("[KFPM] Upgrade complete!")
		refreshCatalog(cfg, false, verbose)
		setStatus("packageInstallStatus", "success")

	case "-s":
		resetLog(cfg)
		os.Exit(exitCode(refreshCatalog(cfg, true, verbose)))

	case "-R":
		id, ok := pkgArg()
		if !ok {
			return
		}
		if len(args) < 3 {
			fmt.Println("[KFPM] -R needs a package id and 1-5 stars")
			return
		}
		stars, _ := strconv.Atoi(args[2])
		resetLog(cfg)
		status, err := rateCmd(cfg, loadRepos(cfg, false).RatingsURL, id, stars)
		if err != nil {
			fmt.Printf("[KFPM] Rating failed: %v\n", err)
			appendLog(cfg, "rating failed: "+err.Error())
		} else {
			fmt.Printf("[KFPM] Rating %s for '%s': %d stars\n", status, id, stars)
		}
		refreshCatalog(cfg, false, verbose) // catalog first: the status event triggers a reload
		setStatus("rateStatus", status)
		if err != nil {
			os.Exit(1)
		}

	case "-k":
		resetLog(cfg)
		err := runCleanup(cfg, args[1:])
		if err != nil {
			fmt.Printf("[KFPM] Cleanup failed: %v\n", err)
			appendLog(cfg, "cleanup failed: "+err.Error())
			setStatus("cleanupStatus", "failure")
			os.Exit(1)
		}
		fmt.Println("[KFPM] Cleanup step done.")
		setStatus("cleanupStatus", "success")

	case "-c":
		resetLog(cfg)
		os.Exit(exitCode(checkForUpdate(cfg)))

	case "-A":
		resetLog(cfg)
		if err := applyUpdate(cfg); err != nil {
			fmt.Printf("[KFPM] App update failed: %v\n", err)
			appendLog(cfg, "update failed: "+err.Error())
			setStatus("appUpdateStatus", "failure")
			os.Exit(1)
		}
		fmt.Printf("[KFPM] KindleForge updated to %s\n", readAppVersion(cfg))
		setStatus("appUpdateStatus", "success")

	case "-l":
		listInstalledCmd(cfg)

	case "-a":
		// Offline-safe: uses the cached manifests, never touches the network.
		printCatalog(buildCatalog(cfg, loadRepos(cfg, false), false))

	case "-abi":
		abi := detectABI()
		writeLocalVersion(cfg)
		fmt.Printf("[KFPM] ABI: %s (platform: %s), KPM: %s at %s, app %s\n", abi, cfg.Platform, backendName(cfg), cfg.KPMPath, readAppVersion(cfg))
		setStatus("deviceABI", abi)

	default:
		fmt.Println("Unknown Option:", args[0])
		help()
	}
}

func exitCode(err error) int {
	if err != nil {
		return 1
	}
	return 0
}

func help() {
	fmt.Println(`KindleForge Package Manager (KPM backend)
=========================================
kfpm -i <ID> [-v]    Install a package (and its dependencies)
kfpm -u <ID> [-v]    Update a package to the newest version
kfpm -r <ID> [-v]    Uninstall a package
kfpm -U [-v]         Upgrade every installed package
kfpm -s [-v]         Sync: refresh catalog definition + repo manifests, check for app update
kfpm -R <ID> <1-5>   Rate an installed package (sent anonymously; queued if offline)
kfpm -k scan         Cleanup: scan the Kindle for removable files (read-only)
kfpm -k apply a,b    Cleanup: move the chosen categories to the trash (restorable)
kfpm -k restore      Cleanup: put everything in the trash back
kfpm -k empty        Cleanup: permanently delete the trash
kfpm -c              Check GitHub for a newer KindleForge release
kfpm -A              Download, verify and install the newer KindleForge release
kfpm -l              List installed packages
kfpm -a              List the catalog (from cache, works offline)
kfpm -abi            Report device ABI to the frontend

Data directory: /mnt/us/.KFPM  (repos.json, catalog.json, cache/, last.log)`)
}

// change runs one install/uninstall through kpm, publishes the result and
// regenerates catalog.json so the frontend sees the new state.
func change(c Config, action, id, statusProp string, verbose bool) error {
	resetLog(c)
	var err error
	if isGingr(c) {
		err = gingrPackageOp(c, action, id, verbose)
	} else {
		ensureRepos(c, loadRepos(c, false).Repos, verbose)
		_, err = runKPM(c, verbose, action, id)
	}
	status := "success"
	if err != nil {
		fmt.Printf("[KFPM] %s of '%s' failed: %v (see %s)\n", action, id, err, c.path("last.log"))
		status = "failure"
	} else {
		fmt.Printf("[KFPM] %s of '%s' succeeded!\n", action, id)
	}
	// Rewrite catalog.json BEFORE signalling: the frontend reloads it on the event.
	refreshCatalog(c, false, verbose)
	setStatus(statusProp, status)
	return err
}

// refreshCatalog rewrites catalog.json. With sync it first updates KPM's index
// and re-downloads the manifests; otherwise it only re-reads local state.
func refreshCatalog(c Config, sync, verbose bool) error {
	rf := loadRepos(c, sync)
	if sync && !isGingr(c) { // gingrspacecadet/kpm has no repo registry or index to update
		ensureRepos(c, rf.Repos, verbose)
		if _, err := runKPM(c, verbose, "update"); err != nil {
			logf("kpm update failed: %v", err)
		}
	}
	cat := buildCatalog(c, rf, sync)
	if !sync {
		keepSyncSources(c, &cat)
	}
	if sync {
		flushPending(c, rf.RatingsURL) // send ratings made while offline
	}
	applyRatings(c, rf, loadRatingsFile(c, sync), &cat)

	reachable := false
	for _, r := range cat.Repos {
		if r.Source != "unavailable" {
			reachable = true
		}
	}
	if err := writeCatalog(c, cat); err != nil {
		logf("could not write catalog: %v", err)
		if sync {
			setStatus("catalogStatus", "failure")
		}
		return err
	}
	if sync {
		// Also look for a newer KindleForge release; failure here is not fatal.
		if info, err := checkUpdate(c); err != nil {
			logf("update check failed: %v", err)
		} else if info.Available {
			fmt.Printf("[KFPM] KindleForge %s is available (installed: %s)\n", info.Latest, info.Current)
		}
		if !reachable {
			setStatus("catalogStatus", "failure")
			return fmt.Errorf("no repository reachable and no cache available")
		}
		setStatus("catalogStatus", "success")
		fmt.Printf("[KFPM] Catalog updated: %d packages\n", len(cat.Packages))
	}
	return nil
}

func listInstalledCmd(c Config) {
	inst := listInstalled(c)
	if len(inst) == 0 {
		fmt.Println("[KFPM] No Installed Packages Found!")
		return
	}
	fmt.Println("Installed Packages:")
	i := 0
	for _, p := range sortedInstalled(inst) {
		i++
		fmt.Printf("%d. %s %s\n", i, p.ID, p.Version)
	}
}

// setStatus publishes a value to the frontend through lipc. No shell is
// involved, so values are never interpreted.
func setStatus(prop, status string) {
	if _, err := exec.LookPath("lipc-set-prop"); err != nil {
		return // not on a Kindle
	}
	_ = exec.Command("lipc-set-prop", lipcService, prop, "-s", status).Run()
}

// checkForUpdate asks GitHub whether a newer KindleForge release exists and
// reports through the updateCheckStatus property.
func checkForUpdate(c Config) error {
	info, err := checkUpdate(c)
	if err != nil {
		fmt.Printf("[KFPM] Update check failed: %v\n", err)
		appendLog(c, "update check failed: "+err.Error())
		setStatus("updateCheckStatus", "failure")
		return err
	}
	if info.Available {
		fmt.Printf("[KFPM] KindleForge %s is available (installed: %s)\n", info.Latest, info.Current)
		setStatus("updateCheckStatus", "available")
	} else {
		fmt.Printf("[KFPM] KindleForge is up to date (%s)\n", info.Current)
		setStatus("updateCheckStatus", "current")
	}
	return nil
}

func backendName(c Config) string {
	if isGingr(c) {
		return "gingrspacecadet/kpm"
	}
	return "KindleModding/KPM"
}

// keepSyncSources makes a local rebuild (after an install/uninstall) report
// where the data came from at the last real sync, not "cache" just because
// the rebuild itself did not touch the network.
func keepSyncSources(c Config, cat *Catalog) {
	data, err := os.ReadFile(c.path("catalog.json"))
	if err != nil {
		return
	}
	var prev Catalog
	if json.Unmarshal(data, &prev) != nil {
		return
	}
	if prev.ConfigSource != "" {
		cat.ConfigSource = prev.ConfigSource
	}
	if prev.GeneratedAt != "" {
		cat.GeneratedAt = prev.GeneratedAt
	}
	old := map[string]string{}
	for _, r := range prev.Repos {
		old[r.ID] = r.Source
	}
	for i, r := range cat.Repos {
		if src, ok := old[r.ID]; ok && r.Source != "unavailable" && src != "" {
			cat.Repos[i].Source = src
		}
	}
}
