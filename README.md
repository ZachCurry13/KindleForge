# KindleForge 📦

> [!NOTE]
> **KPM Edition (personal fork).** The backend now delegates all package work to the native [KPM](https://github.com/KindleModding/KPM) CLI and reads live KPM repositories (default: [KindleTweaks/Repo](https://kindletweaks.github.io/Repo/manifest.json)). **Requires KPM on the device** (ships with `hdnext` / jb.sh). Not yet tested on hardware.

<a href='https://ko-fi.com/W7W31J9IS0' target='_blank'><img height='36' style='border:0px;height:36px;' src='https://storage.ko-fi.com/cdn/kofi5.png?v=6' border='0' alt='Buy Me a Coffee at ko-fi.com' /></a>

*Like [my](https://penguins184.xyz/) work? Consider donating or just starring my repo! :)*

KindleForge is a GUI Appstore for Kindles, allowing you to download & install tweaks & applications such as KOReader directly on-device, without having to keep copying files locally.

## Installation

> [!CAUTION]
> Ensure you are on the latest hotfix prior to installation! Older hotfixes could potentially cause bricks! As always, jailbreak at your own risk ;) **Hotfixes 2.3.1 and above should work fine.**

1. Grab the latest file from releases
2. Unzip it, paste both `KindleForge/` and `KindleForge.sh` into the `documents/` folder on your Kindle.
3. Run the scriptlet, and have fun!

## Features

- Installs through the native KPM package manager
- Search, category tabs and Install / Update / Uninstall buttons
- Paginated, high-contrast e-ink UI (no scrolling, no animation)
- Multiple repositories via `repos.json`, with an offline cache
- Hard-float and soft-float support
- Active community & support

<img width="536" height="724" alt="UI" src="https://github.com/user-attachments/assets/a619e79d-b436-49e2-a52e-85bcd085f7c8" />

## Minimum Requirements

- AdBreak, WinterBreak, or LanguageBreak
- Latest Universal Hotfix
- Above or on the 5.12.2.2 firmware (4.1.0 adds Chromebar fixes; Application Errors fixed with Hotfix 2.3.7+)
- Both **hard-float and soft-float** kindles should work, but not all packages may support both architectures.

## Troubleshooting

Try reloading multiple times, rebooting, or re-copying assets from the latest release and trying again.

KindleTweaks project suggestions? Tweak suggestions? Feature requests? Check out our [discord](https://discord.gg/aSG3eMsAsW)

### Hotfix

Reinstallation steps:

1. Turn on aeroplane mode
2. Restore OTAs
3. Download latest hotfix from wiki, copy to kindle root
4. Update your kindle 
5. Run the hotfix
6. Rename OTAs
7. You can use WiFi once more

### KindleForge

**Q: The KindleForge scriptlet doesn't appear!**

A: Ensure you have copied `KindleForge.sh` into your documents folder. If this is true, and the scriptlet still isn't showing, then reinstall the latest hotfix using the steps above.

**Q: When I click on the scriptlet, it says "Application Error!"**

A: Chances are, you've installed KindleForge incorrectly. **BOTH the folder AND scriptlet MUST be in the `documents` folder on your kindle!**. If you are on an elder firmware this is a usually harmless popup and the app will open regardless - this is being investigated.

**Q: I keep getting core dumps & crashlogs generated when using KindleForge/Scriptlets!**

A: You must be on the latest hotfix, 2.3.1+. Use the steps mentioned above to reinstall hotfix

**Q: No apps load!**

A: Tap *Refresh* (needs Wi-Fi once). Check that KPM exists at `/var/local/kmc/bin/kpm`, and look at `/mnt/us/.KFPM/last.log` (also viewable in-app via *Details > View log*).

**Q: App is installing/uninstalling forever!**

A: Could be caused by a multitude of issues such as beginning the install without wifi, DNS stuff. Press *Refresh* to clear the lock and retry, or reboot. Operations time out after 5 minutes.

## Repositories & data

KFPM keeps its data in `/mnt/us/.KFPM/`:

- `repos.json` - list of KPM manifest URLs (seeded with KindleTweaks) plus an optional package-id to category map for the store tabs
- `catalog.json` - merged catalog the UI reads (rewritten by KFPM)
- `cache/` - last good copy of each manifest, used when offline
- `last.log` - output of the most recent operation

## Building & Contribution

```
cd KFPM
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../KindleForge/KindleForge/binaries/KFPM .
upx --best --lzma ../KindleForge/KindleForge/binaries/KFPM   # optional: ~7MB -> ~1.6MB
```

Desktop testing: set `KFPM_DATA_DIR`, `KFPM_KPM`, `KFPM_DB` and `KFPM_PLATFORM` to point KFPM at a fake `kpm` and a test database. The frontend loads data from `./dev/` when not served from `file://`.

KindleForge is licensed under `GPLv3`, with all other KindleTweaks projects (including the KindleForge Repository) being licensed under `CC BY-NC-SA 4.0`.

## Special Thanks

- [Kurizu](https://ko-fi.com/kurizu), Illusion help & app image
- Gingrspacecadet, KFPM, inspiration & repository help
- Clint, event & KFPM troubleshooting, scroll help
- Dammit Jeff, repository icons
- ThatPotatoDev, ABI differentiation, dependencies, & tweak support

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=KindleTweaks/KindleForge&type=date&legend=top-left)](https://www.star-history.com/#KindleTweaks/KindleForge&type=date&legend=top-left)
