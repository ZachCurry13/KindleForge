/*
   KFPM
   KindleForge Package Manager

   Thin wrapper around the native KPM (Kindle Package Manager) CLI. KFPM owns
   the storefront catalog (repo manifests + offline cache) and reports results
   to the frontend through lipc properties; KPM does the real package work.
*/

package main

import (
	"fmt"
	"os"
	"os/exec"
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

	case "-l":
		listInstalledCmd(cfg)

	case "-a":
		// Offline-safe: uses the cached manifests, never touches the network.
		printCatalog(buildCatalog(cfg, loadRepos(cfg), false))

	case "-abi":
		abi := detectABI()
		fmt.Printf("[KFPM] ABI: %s (platform: %s)\n", abi, cfg.Platform)
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
kfpm -s [-v]         Sync: refresh repo manifests and rewrite catalog.json
kfpm -l              List installed packages
kfpm -a              List the catalog (from cache, works offline)
kfpm -abi            Report device ABI to the frontend

Data directory: /mnt/us/.KFPM  (repos.json, catalog.json, cache/, last.log)`)
}

// change runs one install/uninstall through kpm, publishes the result and
// regenerates catalog.json so the frontend sees the new state.
func change(c Config, action, id, statusProp string, verbose bool) error {
	resetLog(c)
	ensureRepos(c, loadRepos(c).Repos, verbose)

	_, err := runKPM(c, verbose, action, id)
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
	rf := loadRepos(c)
	if sync {
		ensureRepos(c, rf.Repos, verbose)
		if _, err := runKPM(c, verbose, "update"); err != nil {
			logf("kpm update failed: %v", err)
		}
	}
	cat := buildCatalog(c, rf, sync)

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
