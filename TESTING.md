# On-device test checklist (KPM Edition)

Nothing here has run on a Kindle yet. Work top to bottom; stop at the first failure and note it. Items marked **[unverified]** are the specific assumptions made while developing on a desktop.

## 0. Prerequisites
- [ ] Jailbroken, latest hotfix, and **KPM present** (ships with `hdnext` / jb.sh). Wi-Fi available.
- [ ] Optional but recommended: `upx --best --lzma KindleForge/KindleForge/binaries/KFPM` first (7 MB -> ~1.6 MB), in case `/var/local` space is tight.

## 1. Install & run the diagnostics first (no UI involved)
1. Copy `KindleForge/` and `KindleForge.sh` to `documents/` on the Kindle, **and** `KFPM-Diag.sh` next to them.
2. Eject, run **KindleForge** once from the Library (this copies the app to `/var/local/mesquite/KindleForge`). Go back Home.
3. Run **KFPM Diagnostics**. Wait for the "Done" popup, reconnect USB, open `kfpm-diag.txt`.

Check each section of that file:

| Check | Pass looks like | If it fails |
|---|---|---|
| `kpm location` / `kpm version` | a path and a version | KFPM can't find kpm; set it up or tell me the real path |
| `kpm list-repo` **[unverified]** | lines like `- id - name (https://...)` | If the URL isn't printed, KFPM re-adds the repo every run (harmless but noisy). Paste me the output |
| `sqlite3 + kpm db` **[unverified]** | `sqlite3` path, db exists, rows or empty | No `sqlite3` => installed state falls back to folder names (versions blank, no Update button) |
| `free space` | `binaries/KFPM` present, space free on `/var/local` | Pack with UPX |
| `HTTPS / CA certs` **[unverified]** | `manifest http=200` | `http=000` / cert error => Go can't verify TLS on this firmware; paste me the message |
| `KFPM -abi` | `ABI: hf (platform: kindlehf)` or `sf (...kindlepw2)` | Wrong platform => packages hidden |
| `KFPM -s -v` | `Catalog updated: N packages` | Paste the output |
| `KFPM -a` | package list, repo shown as `(network)` | |
| `data dir` | `catalog.json`, `repos.json`, `cache/kindletweaks.json`, `last-log.txt` | |

## 2. UI loads
- [ ] Launch KindleForge. Footer shows **JS Working!**, **ABI: hf/sf**, and **List: <date>**.
- [ ] Package cards appear. If you see "No package list yet. Tap Refresh." after a sync succeeded, the **iframe `file://` read failed [unverified]**: that's the top suspect, tell me and I'll switch the read method.
- [ ] Does it fit? **3 cards + Page Up/Down + footer without scrolling [unverified]**. If it overflows or leaves a big gap, tell me the screen model and I'll change `PAGE_SIZE`.
- [ ] Look for **ghosting/flashing**: tab taps and page turns should redraw once, cleanly.
- [ ] Tap targets feel large enough with a finger.

## 3. Browse features
- [ ] **Page Down / Page Up** change pages; buttons grey out at the ends.
- [ ] **Tabs**: Installed, Media, etc. filter correctly.
- [ ] **Search**: tap the box, type (e.g. `barna`), list narrows without a flurry of refreshes. Clear it to restore.
- [ ] **Details** opens, shows version/repo/category, **Back** closes it.

## 4. Package operations
Use a small, harmless package first (e.g. KUALA).
- [ ] **Install**: button -> banner "Installing KUALA... Please wait." -> "Installed KUALA." and the card flips to INSTALLED / Uninstall. The result event reaching the page **[unverified]** is what drives this; if the banner never changes, tell me (it times out after 6 min).
- [ ] Confirm the package really exists: `ls /mnt/us/kmc/kpm/packages/` (and it appears in KUAL/library as expected).
- [ ] **Uninstall** the same package -> card flips back.
- [ ] **Update**: only testable if a newer version exists; otherwise skip. (Simulate by installing an older artifact via `kpm install` if you have one.)
- [ ] **Update All** (system menu): runs, shows a result.
- [ ] **Failure path**: turn Wi-Fi off, tap Install. Expect "Failed: install ..." with a **View log** button that shows the real `kpm` error.
- [ ] Wrong-device package: any package whose artifacts don't include your platform is hidden (e.g. a `kindlehf`-only package on a soft-float Kindle).

## 5. Offline & repos
- [ ] After one successful Refresh, enable **Airplane Mode**, relaunch: the list still opens, footer says **Offline list** only after a Refresh attempt with no network.
- [ ] Decline the Wi-Fi prompt at launch: app should open on the saved list (it no longer kicks you Home).
- [ ] **Refresh** offline: banner "Could not reach the repositories. Showing the saved list." and the list stays.
- [ ] **Custom repo**: add a second manifest URL to `/mnt/us/.KFPM/repos.json` (and entries under `categories`), Refresh, confirm its packages show up and `kpm list-repo` now lists it.

## 5a. gingrspacecadet/kpm devices (`/usr/local/bin/kpm`, `/mnt/us/kpm/packages.list`)
- [ ] Footer shows `KPM: gingr`; the store lists names with descriptions (no `musl`, `apk`, `make`... entries).
- [ ] **Install** a small one (e.g. `hello`): banner, then card shows INSTALLED and the id appears in `/mnt/us/kpm/packages.list`. Real `kpm -S` output format is [unverified]; check `last-log.txt` if it fails. (Needs `unzip`, `curl` and a writable `/usr/local`.)
- [ ] **Uninstall** it: removed from `packages.list`.
- [ ] Update All reports a clear "not supported" failure (expected).

## 5b. Updates (needs two published releases; install an older one first)
- [ ] After Refresh, if a newer release exists, the black **"KindleForge X is available"** banner shows. **What's new** shows the notes.
- [ ] System menu > **Check for KindleForge Update** reports "up to date" or "available".
- [ ] **Update KindleForge**: banner "Updating KindleForge...", then "Updated... Restarting", the app reloads and the footer shows the new `vX`. **Swap/reload on a real device is [unverified]**; if it hangs, check `/mnt/us/.KFPM/last-log.txt` and that `/var/local/mesquite/KindleForge` still has `index.html` (a `.old` folder next to it means a swap was interrupted).
- [ ] Disable Wi-Fi and tap Update: expect "update failed. Nothing was changed." and the old version still works.
- [ ] Catalog definition: edit `catalog/config.json` on GitHub (e.g. a category), Refresh on the Kindle, footer shows `(github)` and the change appears without an app update.

## 5h. Sources and repositories
- [ ] System menu > Sources and Repositories lists KindleTweaks and the community repository as REVIEWED/on; ZenPM plugin repository, Dropbear SSH, KOReader Tategumi and the mkulik fork as COMMUNITY/off.
- [ ] Turning a community source on needs two taps; after it, Refresh lists its packages (ZenPM plugins appear only if KOReader is installed).
- [ ] Add a repository: type an https address of a manifest.json; Check shows what it found; Add it (two taps); it appears as ADDED BY YOU and can be removed.
- [ ] Install a KOReader plugin from the ZenPM repository; it appears in `koreader/plugins/`; after restarting KOReader it loads. [unverified on a real device]

## 5g. Apps already on the Kindle
- [ ] After Refresh, the Installed tab lists KOReader with a FOUND ON YOUR KINDLE badge and its version. [unverified on device: `koreader/git-rev` exists in current KOReader builds, as in the release zips]
- [ ] If a newer KOReader exists the card says UPDATE x and offers no install button.
- [ ] A hand-copied KindleTweaks app (e.g. KUALA) shows "Take over"; two taps install the store version, then it shows INSTALLED with a version.

## 5f. Fresh list, suggestions
- [ ] Open the app after the list is >24h old (or delete `syncedAt` from `.KFPM/catalog.json`): it refreshes by itself once, and the footer says "List updated just now".
- [ ] Offline and stale: it tries once, fails, and does not retry on the next open within 6 hours; the footer keeps showing the true age.
- [ ] System menu > **Suggest an App** shows the address.
- [ ] After a refresh, `Community` is a repo in the catalog (it has no apps until a maintainer adds one).

## 5e. KindleTweaks packages (KUALA, Gargoyle, ...) on a gingr-only Kindle
- [ ] They appear in the store with versions. Install **KUALA** (small): card shows INSTALLED v1.1.0, `documents/kuala.sh` and `/mnt/us/kuala` exist. [unverified on device: hooks, `/bin/sh`]
- [ ] Uninstall it: both are removed.
- [ ] **Update All Packages** says it updated 0 / nothing, instead of failing.
- [ ] After a failure: **View log** shows text; `kindleforge-log.txt` exists at the top level and keeps history.

## 5c. Clean Up Kindle
- [ ] System menu > Clean Up Kindle shows a scan with free space and categories. [unverified on device: `/mnt/us` is a FUSE filesystem; renaming into `.KFPM/trash` should work but check.]
- [ ] Move one category (e.g. crash dumps) to the trash: the files leave `documents/`; **Restore from trash** puts them back.
- [ ] Your books, My Clippings and KOReader are untouched.

## 5d. Ratings (after the Worker is deployed and `ratingsURL` is set)
- [ ] Install a package: banner shows **Rate it**; Details shows 1-5. Tap a number: "Thanks! Your rating was sent." (needs internet to the Worker).
- [ ] With Wi-Fi off, rate: "Saved. It will be sent next time you are online." Then Refresh online: it is sent.
- [ ] After the Action has run and 3+ votes exist, the card shows "Rating X / 5 (N)".

## 6. Reinstall behaviour
- [ ] Re-run `KindleForge.sh` (or use a new release copy): `/mnt/us/.KFPM/` (repos, cache, catalog) survives and installed packages are unaffected.

## What to send back
For any failure: the relevant section of `kfpm-diag.txt`, plus `/mnt/us/.KFPM/last-log.txt`, your Kindle model, and firmware version. With that I can fix issues without guessing.
