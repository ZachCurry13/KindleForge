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

## Update KindleForge itself
There is no in-app updater (the old one used the retired server).
1. Get the new files as above.
2. Copy the new `KindleForge/` folder and `KindleForge.sh` into `documents/`, replacing the old ones.
3. Run **KindleForge** from the Library once so it re-copies itself.

Your repo list, cached catalog and installed packages are kept: they live in `/mnt/us/.KFPM/` and KPM's own folders, not in the app.

## Remove KindleForge
1. In the app, uninstall any packages you no longer want (they belong to KPM and stay installed otherwise).
2. Delete `documents/KindleForge/` and `documents/KindleForge.sh` over USB.
3. Optional cleanup: delete `/mnt/us/.KFPM/` (repo list, cache, logs). The copy in `/var/local/mesquite/KindleForge` and the app registration need a shell to remove; leaving them is harmless, and reinstalling overwrites them.

## Where things live
| Path | What |
|---|---|
| `/mnt/us/.KFPM/repos.json` | repo manifest URLs and the id-to-category map |
| `/mnt/us/.KFPM/catalog.json` | catalog the UI reads (rewritten by KFPM) |
| `/mnt/us/.KFPM/cache/` | last good copy of each manifest (offline use) |
| `/mnt/us/.KFPM/last.log` | output of the most recent operation |
| `/mnt/us/kmc/kpm/` | KPM's own database and installed packages |

## Adding a repository
Edit `/mnt/us/.KFPM/repos.json` and add another entry under `repos`:

```json
{ "id": "myrepo", "url": "https://example.github.io/Repo/manifest.json" }
```

Optionally add package ids to `categories` (`readers`, `games`, `media`, `utilities`) so they appear under those tabs. Then tap **Refresh**.
