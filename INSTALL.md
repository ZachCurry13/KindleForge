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
- **Update a package:** cards with a newer version show **Update to X.Y.Z**. **Update All Packages** (system menu) updates every KindleForge-installed package that has a newer version.
- **Refresh the list:** the **Refresh** button, or *Refresh Packages* in the system menu.
- **When something fails:** tap **View log** (in the banner or under Details) to see what KPM printed.

## Sources and repositories
System menu > **Sources and Repositories** lists every repository KindleForge can read, with how much to trust it:
- **REVIEWED**: on by default. A person has looked at it.
- **COMMUNITY: NOT REVIEWED**: listed so you can find it, **off** until you turn it on (tap twice, after a warning). Their packages run other people's code on your Kindle.
- **ADDED BY YOU**: repositories you add with **Add a repository**. Type the address of the repository's `manifest.json` (it must start with `https://`); KindleForge fetches it, shows what it found (name, kind, number of packages) and only adds it after you confirm. You can turn it off or remove it later.

Two kinds of repository are understood:
- **KPM repositories** (KindleModding/KindleTweaks style): packages with install scripts, handled as described under "Two package sources".
- **ZenPM-style repositories** (for example the ZenPM plugin repository, about 250 KOReader plugins): only **KOReader plugins** are offered, and only if KOReader is on your Kindle. Each plugin's download must be a GitHub release file whose **SHA-256 matches** the one the repository publishes; it is then unpacked into `koreader/plugins/` and nothing else is touched, and no script is run. A plugin whose zip does not hold exactly one `<name>.koplugin` folder is refused. **Restart KOReader** after installing, updating or removing a plugin. A plugin already in KOReader that KindleForge did not install is never replaced.

## Apps already on your Kindle
KindleForge looks for apps it knows about, even ones you installed by hand, and lists them in the **Installed** tab with a **FOUND ON YOUR KINDLE** badge. It only **reads** files to do this.
- **KOReader:** shown with its version (from `koreader/git-rev`) and, after a Refresh, whether a newer release exists. KindleForge does **not** update KOReader: use KOReader's own updater. It only tells you.
- **KindleTweaks apps copied over by hand** (KUALA, Gargoyle, KinAMP, LARK, RAnki): their version can't be read, so the card offers **Take over**. Tap it twice: KindleForge installs the store version over your copy, exactly like an update, and from then on tracks its version, so Update and Update All work. The packages' own scripts replace the program files and leave your data (cards, listening history, settings) in place, but back up anything precious first.
- **What it recognises** is a list in `catalog/config.json` (`detect`), so more apps can be added without an app update. Anything not on the list is simply not shown.

## Finding more sources (for maintainers)
A weekly job lists repositories and apps worth a look in [DISCOVERED.md](DISCOVERED.md). To offer one in the directory, run `scripts/promote_source.py <id>`: it is added **off by default**. See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/RESOURCES.md](docs/RESOURCES.md).

## Keeping the list fresh
- **Automatically:** when you open KindleForge and the saved list is more than **24 hours old**, it refreshes by itself (if the Kindle is online). If that attempt fails (for example you are offline) it will not try again for **6 hours**, so opening the app offline is not slow.
- **By hand:** tap **Refresh** any time.
- **The footer** shows the age: "List updated 3 h ago". After 7 days it says "List is 9 days old: tap Refresh". A Refresh that could only use the saved copy says so and does **not** reset the clock.
- **Where new apps come from:** a daily job on GitHub keeps the package info current, new packages in the upstream list appear on their own, and apps added to the community repository appear on the next Refresh. None of this needs an app update.

## Suggest an app
System menu > **Suggest an App** shows the address of the suggestion form (open it on a phone or computer). The most-wanted suggestions are listed in [SUGGESTIONS.md](SUGGESTIONS.md). Accepted apps are read and checked by hand and then pinned to a checksum, so a package that changes afterwards is refused. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Two package sources, one store
- **gingrspacecadet/kpm packages** (the ones in the daily-synced list) are installed with your Kindle's `kpm -S` / `kpm -R`. That KPM tracks no versions, so these have no Update button.
- **KindleTweaks repository packages** (KUALA, Gargoyle, KinAMP, LARK, RAnki, and any repo you add to `repos.json`) are installed **by KindleForge itself**: it downloads the package, unpacks it safely, runs its install script, and remembers the version, so **Update** and **Update All Packages** work for these. Uninstalling runs the package's own uninstall script; your data (RAnki's cards, LARK's listening history, KinAMP's settings) is left in place by those scripts.
- **Community repository:** apps people suggest are added to `catalog/community/manifest.json` with a **sha256 pin**, and that repository is marked `requireHash`: KindleForge refuses any package from it that has no pin or whose download does not match.
- **Trust:** the install and uninstall scripts come from the repository and run as root on the Kindle, exactly as with any KPM. The repository publishes no checksums, so KindleForge relies on https. I statically reviewed the five current packages' scripts: they only write under `/mnt/us`. Only add repositories you trust.

## Logs
KindleForge keeps a history in **`kindleforge-log.txt`** at the top level of the Kindle's storage (size-limited, newest kept), and the latest run in `.KFPM/last-log.txt`, which **View log** shows. Pull `kindleforge-log.txt` over USB when something goes wrong. **Clean Up Kindle** can move the logs and test reports to the trash.

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

## Where package info comes from, and keeping it current
The store lists every package in the upstream KPM list (infrastructure like `musl` or `make` is hidden). A daily job on GitHub reads that list and fills in each package's name, author and description from the package's own GitHub project, so **new packages show up by themselves**. Kindles get the update on their next **Refresh**; no app update is needed. Anything written by hand in `catalog/config.json` wins and is never overwritten (`scripts/sync_packages.py`). A package with no description simply has none until its project gets one: add one by hand in `catalog/config.json` under `packages`.

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
| `/mnt/us/.KFPM/last-log.txt` | output of the most recent operation |
| `/mnt/us/kmc/kpm/` or `/mnt/us/kpm/` | KPM's own database / installed list and packages (KindleModding / gingrspacecadet) |

## Adding a repository
Edit `/mnt/us/.KFPM/repos.json` (your personal file, merged with the GitHub catalog definition) and add another entry under `repos`:

```json
{ "id": "myrepo", "url": "https://example.github.io/Repo/manifest.json" }
```

Optionally add package ids to `categories` (`readers`, `games`, `media`, `utilities`) so they appear under those tabs, or list a catalog repo id under `"disable": [...]` to hide it. Then tap **Refresh**.

To change the catalog for *everyone*, edit `catalog/config.json` in the GitHub repo instead. It also takes per-package display data (`packages`: name, description, category) and a `hidden` list; your `repos.json` can override these and use `"show": [...]` to un-hide a package.

Custom repositories apply to KindleModding/KPM only. With gingrspacecadet/kpm, packages come from the mirror list in `/etc/kpm/mirrors.conf`.
