# KindleForge (KPM Edition)

GUI app store for jailbroken Kindles. Fork of KindleTweaks/KindleForge, rewritten to use the native KPM CLI.

## Layout
- `KFPM/` - Go backend (`main.go` commands, `config.go` paths/repos.json, `kpm.go` kpm + sqlite, `catalog.go` manifests/cache/catalog.json)
- `KindleForge/KindleForge/` - frontend (ES5 only, old Mesquite WebKit): `index.html`, `style.css`, `script.js`, `binaries/` (KFPM + Utild)
- `KindleForge/KindleForge.sh` - launcher scriptlet (copies app to /var/local/mesquite, registers in appreg.db)

## How it fits together
- Page -> backend: `kindle.messaging.sendStringMessage("com.kindlemodding.utild","runCMD","<KFPM> -i <id>")`
- Backend -> page: `lipc-set-prop xyz.penguins184.kindleforge <prop> -s <value>` (`packageInstallStatus`, `packageUninstallStatus`, `catalogStatus`, `deviceABI`). catalog.json is written BEFORE the status is set.
- Page reads `/mnt/us/.KFPM/catalog.json` and `last.log` through a hidden iframe (file:// XHR is unreliable).

## KPM facts (verified from KindleModding/KPM source)
- CLI: `kpm [-y] {add-repo|remove-repo|list-repo|update|search|install|uninstall|upgrade|launch}`; no list-installed command.
- DB `/mnt/us/kmc/kpm/kpm.db` (table `installed_packages`), packages in `/mnt/us/kmc/kpm/packages/`. KFPM reads the DB via the `sqlite3` binary.
- Manifest v2: packages keyed by id; artifacts have `version:[a,b,c]`, `supported_platforms` (`kindlehf`/`kindlepw2`), `dependencies`.

## Two KPMs (auto-detected, `detectBackend` in config.go; force with `KFPM_BACKEND=modding|gingr`)
- `modding` = KindleModding/KPM (see above), preferred when both exist.
- `gingr` = gingrspacecadet/kpm, `gingr.go`: `/usr/local/bin/kpm`, `kpm -S/-R/-Ql/-Qr`, installed ids in `/mnt/us/kpm/packages.list`, mirrors in `/etc/kpm/mirrors.conf` (first field = list URL, list = names only). Exit codes are unreliable (it can crash after a good install, and reads `kpm -Ql kpmgui` unchecked), so success is decided by re-reading packages.list and kpm gets an explicit PATH. No versions, so no Update/Update All. Display data comes from `packages`/`hidden`/`categories` in `catalog/config.json`.

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
