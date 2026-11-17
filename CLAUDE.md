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

## Build / test
- Build: see README. Always `CGO_ENABLED=0`.
- Desktop test: env vars `KFPM_DATA_DIR`, `KFPM_KPM`, `KFPM_DB`, `KFPM_PKG_DIR`, `KFPM_PLATFORM`; frontend uses `./dev/` data when not on file://.
- UI rules: black on white only, no transitions/animations, paginated (PAGE_SIZE=3), ES5, no scrolling lists.

## Not yet verified on hardware
iframe file:// catalog read (incl. `?t=` cache-buster), real `kpm list-repo` output format, page size on-device, HTTPS CA certs for Go.
