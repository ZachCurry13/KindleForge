# KindleForge (KPM Edition)

GUI app store for jailbroken Kindles. Fork of KindleTweaks/KindleForge, rewritten to use the native KPM CLI.

## Layout
- `KFPM/` - Go backend (`main.go` commands, `config.go` paths/repos.json, `kpm.go` kpm + sqlite, `catalog.go` manifests/cache/catalog.json)
- `KindleForge/KindleForge/` - frontend (ES5 only, old Mesquite WebKit): `index.html`, `style.css`, `script.js`, `binaries/` (KFPM + Utild)
- `KindleForge/KindleForge.sh` - launcher scriptlet (copies app to /var/local/mesquite, registers in appreg.db)

## How it fits together
- Page -> backend: `kindle.messaging.sendStringMessage("com.kindlemodding.utild","runCMD","<KFPM> -i <id>")`
- Backend -> page: `lipc-set-prop xyz.penguins184.kindleforge <prop> -s <value>` (`packageInstallStatus`, `packageUninstallStatus`, `catalogStatus`, `deviceABI`). catalog.json is written BEFORE the status is set.
- Page reads `/mnt/us/.KFPM/catalog.json` and `last-log.txt` through a hidden iframe (file:// XHR is unreliable).

## KPM facts (verified from KindleModding/KPM source)
- CLI: `kpm [-y] {add-repo|remove-repo|list-repo|update|search|install|uninstall|upgrade|launch}`; no list-installed command.
- DB `/mnt/us/kmc/kpm/kpm.db` (table `installed_packages`), packages in `/mnt/us/kmc/kpm/packages/`. KFPM reads the DB via the `sqlite3` binary.
- Manifest v2: packages keyed by id; artifacts have `version:[a,b,c]`, `supported_platforms` (`kindlehf`/`kindlepw2`), `dependencies`.

## Two KPMs (auto-detected, `detectBackend` in config.go; force with `KFPM_BACKEND=modding|gingr`)
- `modding` = KindleModding/KPM (see above), preferred when both exist.
- `gingr` = gingrspacecadet/kpm, `gingr.go`: `/usr/local/bin/kpm`, `kpm -S/-R/-Ql/-Qr`, installed ids in `/mnt/us/kpm/packages.list`, mirrors in `/etc/kpm/mirrors.conf` (first field = list URL, list = names only). Exit codes are unreliable (it can crash after a good install, and reads `kpm -Ql kpmgui` unchecked), so success is decided by re-reading packages.list and kpm gets an explicit PATH. No versions, so no Update/Update All. Display data comes from `packages`/`hidden`/`categories` in `catalog/config.json`.

## KindleModding-style packages without KindleModding's KPM (`kpkg.go`)
Manifest repos (`repos` in catalog config, e.g. KindleTweaks/Repo) work on the gingr backend too: `kpkgSources` reads the manifests, `kpkgInstall` downloads the .kpkg (gzip tar: manifest.json, install.sh, uninstall.sh, payload/), extracts safely (no `..`, links, devices; size/file caps; https only), validates manifest id/version/platform against the catalog, runs `sh install.sh` from the package folder with `KPM_PLATFORM` etc., and records the version in `.KFPM/kpkg/installed.json` (packages live in `.KFPM/kpkg/pkg/<id>`). A manifest package wins over a same-named gingr package. `change()` routes: manifest id -> kpkg, else gingr `kpm -S`. `-U` updates kpkg packages with newer versions. No checksums exist in manifests; dependencies are refused. Live check without running hooks: `KFPM_LIVE=1 go test -run Live`. Never run third-party hooks on the dev machine.
Logs: `last-log.txt` (latest run, shown by the app) + `<root>/kindleforge-log.txt` (history, trimmed); both belong to the Clean Up `diag` category.

## Detecting apps installed by hand (`detect.go`)
Catalog definition has `detect` entries (paths all-present, optional versionFile/Regex, latestGitHub, note/shortNote). `scanDetected` only reads (symlinks refused, version files must resolve inside the user root); `loadLatest` asks GitHub for the newest release at sync (cached in `cache/latest.json`); `applyDetected` merges into the catalog: ids that exist in the store (not installed by KFPM) get `detected` -> the page offers "Take over" (two taps; = `kpkgInstall` over the manual copy); other ids are appended `readOnly` (information only, e.g. KOReader, whose own updater does updates). Versions compare numerically (`compareVersions`); a nightly ahead of the release is not "outdated".

## Freshness, community apps, suggestions
- `catalog.json` has `syncedAt` (last time real data came from the network) and `attemptedAt`. A sync that falls back to saved data FAILS (catalogStatus failure) and keeps `syncedAt`. The page auto-syncs at launch when `syncedAt` is >24h old and `attemptedAt` is >6h old (`shouldAutoSync` in script.js).
- Community repo = `catalog/community/manifest.json` (KindleModding manifest v2 + optional per-artifact `sha256`), listed in `catalog/config.json` repos with `requireHash:true`; `kpkgDownload` verifies the pin, `kpkgInstall` refuses unpinned packages from requireHash repos. Add apps with `scripts/add_app.py` (inspects without executing, prints hooks to read, pins sha256).
- Suggestions: issue form `.github/ISSUE_TEMPLATE/suggest-app.yml` (label `app-suggestion`) -> `scripts/pull_suggestions.py` -> `SUGGESTIONS.md` via `.github/workflows/suggestions.yml` (never interpolates issue text into shell; titles sanitised). `suggestURL` in config.json feeds the app's "Suggest an App" screen. See CONTRIBUTING.md. Issues must be enabled on the repo (forks have them off by default).
- Tests: `cd KFPM && go test ./...`, `cd ratings-worker && npm test`, `python3 scripts/test_{sync_packages,add_app,pull_suggestions}.py`.

## Package info sync (`scripts/sync_packages.py`, `.github/workflows/sync-packages.yml`)
Daily job: reads gingrspacecadet/kpm `packages.list`, derives name/author/description/category per package from the upstream project (GitHub repo named in the package's `install.kpm`, a same-named repo by the KPM author, or the KPM wiki page) and writes them into `catalog/config.json` `packages`. Fields it wrote are listed in the entry's `auto`; human-written fields are never overwritten. Infrastructure (`lib*`, kpm, musl, ...) is added to `hidden`. Tests: `python3 scripts/test_sync_packages.py`. Kindles read the result on Refresh.

## Ratings (`ratings.go`, `ratings-worker/`, `scripts/update_ratings.py`, `.github/workflows/ratings.yml`)
KFPM `-R <id> <1-5>` records the vote in `.KFPM/my-ratings.json`, POSTs `{pkg,install,stars}` to `ratingsURL` (https only, from `catalog/config.json`), queues it if offline and flushes on `-s`. Install id = random, `.KFPM/install-id`. Averages come from `catalog/ratings.json` (committed by the Action from the Worker's `/aggregate`), cached, shown only at >= minVotes. Worker = Cloudflare Worker + D1 (`ratings-worker/`, `npm test`; deploy steps in its README). Status prop `rateStatus` = sent|queued|failure. Deploying the Worker needs the owner's `wrangler login`; never ask for tokens.

## Cleanup (`cleanup.go`)
`-k scan|apply <cats>|restore|empty`. Fixed allowlist of categories (crash, oldforge, dumps, diag, kfupdate, orphansdr); apply re-scans and derives paths itself (UI sends only category ids); moves into `<data>/trash` with a manifest; `allowedPath` blocks protected top-level folders, symlinks and anything outside the roots; restore validates manifest paths; only `empty` really deletes. Writes `cleanup.json` (report + `last` result) after every command; status prop `cleanupStatus`. Tests: `cleanup_test.go`.

## Updates
- Catalog definition: `catalog/config.json` in the GitHub repo (repos + categories). KFPM fetches it on Refresh (`-s`), caches it in `cache/config.json`, falls back to built-ins. User `repos.json` merges on top. Code: `config.go`.
- App self-update (`selfupdate.go`): `-c` checks GitHub releases (pre-releases included), `-A` downloads the zip, requires GitHub's `sha256:` digest, extracts only `KindleForge/` (zip-slip/symlink/size checks), validates `VERSION == tag`, swaps `<app>` <-> `<app>.new` with rollback. Version comes from `KindleForge/KindleForge/VERSION`. Launcher is never updated in-app.
- Releasing: bump VERSION, run `scripts/release.sh`, then `gh release create v<VERSION> dist/*.zip --prerelease`. The tag, VERSION and zip must agree or the updater refuses it.
- Tests: `cd KFPM && go test ./...` (fake GitHub server end-to-end).

## Build / test
- Build: see README. Always `CGO_ENABLED=0`.
- Desktop test: env vars `KFPM_DATA_DIR`, `KFPM_KPM`, `KFPM_DB`, `KFPM_PKG_DIR`, `KFPM_PLATFORM`; frontend uses `./dev/` data when not on file://.
- UI rules: black on white only, no transitions/animations, paginated (PAGE_SIZE=3), ES5, no scrolling lists.

## Not yet verified on hardware
self-update swap on a real device (only tested on desktop temp dirs), gingr backend against the real gingrspacecadet/kpm (only tested with a fake script), iframe file:// catalog read (incl. `?t=` cache-buster), real `kpm list-repo` output format, page size on-device, HTTPS CA certs for Go.
