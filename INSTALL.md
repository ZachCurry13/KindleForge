# Installing, updating and removing KindleForge (KPM Edition)

> Not yet tested on hardware. See [TESTING.md](TESTING.md) for the first-run checklist.

## Requirements
- A jailbroken Kindle on the latest hotfix.
- **A KPM installed.** KindleForge supports two, and detects which one you have:
  - **KindleModding/KPM** (ships with `hdnext` / jb.sh): `/var/local/kmc/bin/kpm`, data in `/mnt/us/kmc/kpm/`. Full features: versions, dependencies, per-device filtering, Update buttons and Update All. Preferred if both are present.
  - **gingrspacecadet/kpm** (`kpm -S` / `-R`): `/usr/local/bin/kpm`, data in `/mnt/us/kpm/` (`packages.list`). Its repository is only a list of package names and it tracks no versions, so there are no Update buttons and **Update All is not available**. Descriptions, categories and the list of hidden infrastructure packages (`musl`, `apk`, `make`, ...) come from the GitHub catalog definition (`catalog/config.json`). The footer shows `KPM: gingr`.
- Wi-Fi for refreshing the package list and installing. Browsing the saved list works offline.

## Get the files
Download the zip from the [latest release](https://github.com/ZachCurry13/KindleForge/releases/latest) and unzip it. It contains the `KindleForge/` folder, `KindleForge.sh`, `KFPM-Diag.sh` and these docs.

To build from source instead:

```bash
git clone https://github.com/ZachCurry13/KindleForge.git
```

then take the `KindleForge/KindleForge/` folder and `KindleForge/KindleForge.sh`, after building KFPM as described in the README.

Optional, if you build it yourself: shrink the backend binary first (about 7 MB down to 1.6 MB):

```bash
upx --best --lzma KindleForge/KindleForge/binaries/KFPM
```

## Install
1. Connect the Kindle over USB.
2. Copy the `KindleForge/` folder **and** `KindleForge.sh` into the Kindle's `documents/` folder. Both must be there.
3. Eject, then open **KindleForge** from the Home Library.
4. On first launch it copies itself to `/var/local/mesquite/KindleForge` and registers the app. Go back Home and launch it again if it doesn't open straight away.
5. In the app, tap **Refresh** (needs Wi-Fi) to load the package list.

Optional: copy `KindleForge/KFPM-Diag.sh` into `documents/` too. Running it writes `kfpm-diag.txt` to the Kindle's root for troubleshooting.

## Using it
- **Install / Uninstall:** tap the button on a package card.
- **Update a package:** cards with a newer version show **Update to X.Y.Z**. **Update All Packages** is in the system menu.
- **Refresh the list:** the **Refresh** button, or *Refresh Packages* in the system menu.
- **When something fails:** tap **View log** (in the banner or under Details) to see what KPM printed.

## Updates: two separate things

### 1. The package catalog (updates without a new app version)
The list of repositories and the category tabs come from `catalog/config.json` in this GitHub repo. **Refresh** (button or system menu) downloads it, so catalog changes reach every Kindle without a new release. The last good copy is cached, so it still works offline, and built-in defaults are used if there is no cache. The footer shows where the list came from: `(github)`, `(cache)` or `(built-in)`. Your own `repos.json` is merged on top and is never overwritten.

### 2. KindleForge itself (in-app, from GitHub releases)
- **Refresh** also checks GitHub for a newer release. You can check on demand from the system menu: **Check for KindleForge Update**.
- When one exists, a black banner appears at the top: **KindleForge X is available**. **What's new** shows the release notes; **Update KindleForge** installs it.
- The update downloads the release zip, **verifies its SHA-256 checksum against the one GitHub publishes for that file**, checks the package is complete and matches the release version, then swaps the app folder (keeping the old one until the swap succeeds, and restoring it if it fails). The app then reloads itself.
- It only replaces the app folder. **`KindleForge.sh` (the launcher) is not updated automatically.** If a release note says the launcher changed, copy the new `KindleForge.sh` over USB.
- Releases from before v5.0.0-alpha.2 have no updater: install alpha.2 or later by hand once, and it can update itself from there.
- Manual route (always works): copy the new `KindleForge/` folder and `KindleForge.sh` into `documents/` and run it once.

Your repo list, cached catalog and installed packages are kept: they live in `/mnt/us/.KFPM/` and KPM's own folders, not in the app.

## Remove KindleForge
1. In the app, uninstall any packages you no longer want (they belong to KPM and stay installed otherwise).
2. Delete `documents/KindleForge/` and `documents/KindleForge.sh` over USB.
3. Optional cleanup: delete `/mnt/us/.KFPM/` (repo list, cache, logs). The copy in `/var/local/mesquite/KindleForge` and the app registration need a shell to remove; leaving them is harmless, and reinstalling overwrites them.

## Rating packages
Once ratings are switched on (the catalog says so), an installed package shows **Rate it** after installing, and **1 to 5** buttons in its **Details** view. Averages appear on the cards once a package has at least 3 votes. Ratings are **anonymous**: the app sends only the package id, your 1-5 vote and a random install id (not your Kindle's serial number). Offline? The rating is saved and sent next time you Refresh. Change your vote any time by tapping another number. Details: [ratings-worker/README.md](ratings-worker/README.md).

## Clean up your Kindle
System menu > **Clean Up Kindle**. KindleForge scans (read-only) and lists what it found, with sizes:
- **Crash dumps** (`pillowd_*` / `mesquite_*` ... `_crash`), **old KindleForge leftovers** (`KF-Update-Temp`, old `installed.txt`), **Indexer dumps**, **KindleForge test reports** and **download leftovers**: on by default.
- **Orphaned book data (.sdr)**: reading progress and highlights for books no longer on the Kindle. **Off by default**; removing them loses that data if you re-add the book.

Tap a row to switch it ON/OFF, then **Move selected to trash**. Nothing is deleted yet: items go to `/mnt/us/.KFPM/trash` and **Restore from trash** puts them back (an item whose original place is taken stays in the trash). **Empty trash** (tap twice) is the only permanent delete. **Biggest items** is information only.

It only ever considers its own fixed list of categories; it never takes a path from the screen, never follows links, and never touches your books, `My Clippings`, KOReader, `extensions`, `system` or other folders.

## Where things live
| Path | What |
|---|---|
| `/mnt/us/.KFPM/repos.json` | **your own** extra repos, category overrides and `disable` list (never overwritten) |
| `/mnt/us/.KFPM/trash/` | files moved by Clean Up, restorable until you empty the trash |
| `/mnt/us/.KFPM/update.json` | installed version and any pending KindleForge update |
| `/mnt/us/.KFPM/cache/config.json` | last good copy of the GitHub catalog definition |
| `/mnt/us/.KFPM/catalog.json` | catalog the UI reads (rewritten by KFPM) |
| `/mnt/us/.KFPM/cache/` | last good copy of each manifest (offline use) |
| `/mnt/us/.KFPM/last.log` | output of the most recent operation |
| `/mnt/us/kmc/kpm/` or `/mnt/us/kpm/` | KPM's own database / installed list and packages (KindleModding / gingrspacecadet) |

## Adding a repository
Edit `/mnt/us/.KFPM/repos.json` (your personal file, merged with the GitHub catalog definition) and add another entry under `repos`:

```json
{ "id": "myrepo", "url": "https://example.github.io/Repo/manifest.json" }
```

Optionally add package ids to `categories` (`readers`, `games`, `media`, `utilities`) so they appear under those tabs, or list a catalog repo id under `"disable": [...]` to hide it. Then tap **Refresh**.

To change the catalog for *everyone*, edit `catalog/config.json` in the GitHub repo instead. It also takes per-package display data (`packages`: name, description, category) and a `hidden` list; your `repos.json` can override these and use `"show": [...]` to un-hide a package.

Custom repositories apply to KindleModding/KPM only. With gingrspacecadet/kpm, packages come from the mirror list in `/etc/kpm/mirrors.conf`.
