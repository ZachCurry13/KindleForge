# Installing, updating and removing KindleForge (KPM Edition)

> Not yet tested on hardware. See [TESTING.md](TESTING.md) for the first-run checklist.

## Requirements
- A jailbroken Kindle on the latest hotfix.
- **KPM installed** (it ships with `hdnext` / jb.sh). KindleForge looks for it at `/var/local/kmc/bin/kpm`, then `/mnt/us/kmc/bin/kpm`, then on `PATH`.
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

## Where things live
| Path | What |
|---|---|
| `/mnt/us/.KFPM/repos.json` | **your own** extra repos, category overrides and `disable` list (never overwritten) |
| `/mnt/us/.KFPM/update.json` | installed version and any pending KindleForge update |
| `/mnt/us/.KFPM/cache/config.json` | last good copy of the GitHub catalog definition |
| `/mnt/us/.KFPM/catalog.json` | catalog the UI reads (rewritten by KFPM) |
| `/mnt/us/.KFPM/cache/` | last good copy of each manifest (offline use) |
| `/mnt/us/.KFPM/last.log` | output of the most recent operation |
| `/mnt/us/kmc/kpm/` | KPM's own database and installed packages |

## Adding a repository
Edit `/mnt/us/.KFPM/repos.json` (your personal file, merged with the GitHub catalog definition) and add another entry under `repos`:

```json
{ "id": "myrepo", "url": "https://example.github.io/Repo/manifest.json" }
```

Optionally add package ids to `categories` (`readers`, `games`, `media`, `utilities`) so they appear under those tabs, or list a catalog repo id under `"disable": [...]` to hide it. Then tap **Refresh**.

To change the catalog for *everyone*, edit `catalog/config.json` in the GitHub repo instead.
