# Resources and credits

KindleForge stands on other people's work. This page says what it uses, what it only links to, and why.

## Used by the code
- **[KindleTweaks/Repo](https://github.com/KindleTweaks/Repo)** and **[KindleModding/KPM](https://github.com/KindleModding/KPM)**: the package format and the main package repository KindleForge installs from.
- **[gingrspacecadet/kpm](https://github.com/gingrspacecadet/kpm)**: the other KPM that many Kindles have; KindleForge works with it too.
- **[ZenPM (xZenLabs/zen-pm)](https://github.com/xZenLabs/zen-pm)**: another package manager for Kindles and KOReader, GPL-3.0 like KindleForge. KindleForge can read ZenPM-style repositories (the documented *Repository Manifest Contract*), so people using either tool draw on the same pool of KOReader plugins. KindleForge's reader is an independent implementation of that data format; no ZenPM code is used. ZenPM credits KindleForge's frontend as its starting point, and KindleForge credits [Penguins184's original KindleForge](https://github.com/KindleTweaks/KindleForge).
- **[Awesome-Kindle](https://github.com/KindleTweaks/Awesome-Kindle)** (CC0): its list of apps feeds the weekly discovery job (`DISCOVERED.md`).

## Only linked, never scraped
These are written by and for people. KindleForge does not copy or ingest their content; they are here to be read.
- **[KindleModShelf](https://kindlemodshelf.me/)**: a catalog of mods, plugins and guides with download links.
- **[KindleModding wiki](https://kindlemodding.org/)**: jailbreaking, KPM and how Kindle modding works.
- **[MIP Wiki](https://mip-wiki.pages.dev/database/)**: a question-and-answer troubleshooting database by KMC helpers. Its authors state that no AI is used on it, and KindleForge respects that by linking to it only.
- **[Penguins' Kindle Wiki](https://github.com/penguins184/Penguins-Kindle-Wiki)**.

## How this project uses automation
Plain scripts run on a schedule: they refresh package descriptions, rebuild the suggestion wishlist, and look for repositories to consider. **None of them decides what you can install**:
- Packages from sources marked *reviewed* were looked at by a person.
- Community sources are **off** until you switch them on, and you are warned first.
- Anything suggested by the public is read by a maintainer and pinned to a checksum before it is added.
- Downloads from ZenPM-style repositories must match a published SHA-256 before anything is unpacked.

KindleForge is developed with the help of an AI assistant (Claude, by Anthropic); you can see the co-author line in the commit history. The code, tests and every scheduled job are open for anyone to read, and the safety rules above are enforced by tests, not by trust.
