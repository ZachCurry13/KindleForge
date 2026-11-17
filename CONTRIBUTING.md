# Contributing

## Suggest an app
Open an issue with the **Suggest an app** form: https://github.com/ZachCurry13/KindleForge/issues/new?template=suggest-app.yml
(or tap **Suggest an App** in KindleForge's system menu to see the address). Add a 👍 to suggestions you also want. The current list is in [SUGGESTIONS.md](SUGGESTIONS.md) and updates by itself.

## Report a problem
Use the **Report a problem** form and attach `kindleforge-log.txt` from the top of the Kindle's storage.

## How an app gets into the store (maintainers)
An app can arrive three ways. Only the third needs work from us.

1. **It is in the gingrspacecadet/kpm package list.** Nothing to do: the daily *Sync package info* job lists it and fills in its description.
2. **It is in a KindleModding-style repository you trust** (like KindleTweaks/Repo). Add that repository to `repos` in `catalog/config.json`; its packages appear on the next Refresh. Only add repositories you would install software from.
3. **A single package someone offers.** Add it to the community repository, which pins every download to a checksum:

   ```bash
   scripts/add_app.py --id foo --name "Foo" --author "Someone" \
       --description "One sentence on what it does." --version 1.2.3 \
       --platforms kindlehf,kindlepw2 \
       --url https://github.com/someone/foo/releases/download/v1.2.3/foo_1.2.3.kpkg \
       --category utilities
   ```

   The script downloads the package, checks it **without running anything**, prints `install.sh` / `uninstall.sh` and records the sha256 in `catalog/community/manifest.json`.

   **Read the printed scripts.** They run as root on people's Kindles. Reject anything that downloads and runs other code, writes outside `/mnt/us`, touches system partitions, phones home, or you cannot explain. Packages with dependencies are not supported yet. If the author later publishes a new version, run the script again: the old checksum stops matching, so Kindles will not install an unreviewed file.

   Then commit, label the issue `added` and close it. It moves to "Added to the store" in `SUGGESTIONS.md`.

Labels used: `app-suggestion` (added by the form), `accepted`, `added`, `needs-info`, `bug`.

## Offer another repository
The weekly *Discover sources* job writes [DISCOVERED.md](DISCOVERED.md). To offer a repository to everyone (switched **off** by default, with a warning when a user turns it on):
1. Read its packages. Check they install and uninstall cleanly and do what they say. For KPM repositories, `KFPM_LIVE=1 go test -run LiveDirectoryKPM` downloads and unpacks every package (it never runs a script).
2. `scripts/promote_source.py <id> --note "One honest line about it."`
3. Commit `catalog/config.json`. Users get it on their next Refresh.

A repository you would put on by default (trust *reviewed*) belongs in `repos`, not `sources`, and should have been read line by line. Individual people can always add any repository themselves from **Sources and Repositories**.

## Teach KindleForge to recognise an app on a Kindle
Add an entry to `detect` in `catalog/config.json`: `id`, `name`, `paths` (ALL must exist, relative to the Kindle's storage), optionally `versionFile` + `versionRegex` (first capture group is the version) and `latestGitHub` (`owner/repo`, whose newest release counts as the latest version), plus a `note` and a one-line `shortNote`. If the `id` matches a store package, the card offers "Take over"; otherwise the app is listed for information only. Detection only reads files and never leaves the Kindle's storage.

## Tests
```bash
cd KFPM && go test ./...                      # backend
cd ratings-worker && npm test                 # ratings endpoint
python3 scripts/test_sync_packages.py         # package info sync
python3 scripts/test_add_app.py               # adding reviewed apps
python3 scripts/test_pull_suggestions.py      # wishlist
python3 scripts/test_discover.py              # source discovery and promotion
KFPM_LIVE=1 go test -run Live ./...           # real downloads, unpacked only; nothing is executed (optional)
```
Never run third-party install scripts on a development machine to "see what happens": read them.
