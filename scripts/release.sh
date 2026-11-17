#!/bin/sh
# Builds the release zip from the checked-in tree.
#   scripts/release.sh            -> dist/KindleForge-KPM-v<VERSION>.zip
# The version comes from KindleForge/KindleForge/VERSION. Publish with:
#   gh release create v<VERSION> dist/KindleForge-KPM-v<VERSION>.zip --prerelease --repo ZachCurry13/KindleForge
# The in-app updater only installs a zip whose VERSION file matches the tag.
set -eu
cd "$(dirname "$0")/.."
VERSION=$(tr -d '[:space:]' < KindleForge/KindleForge/VERSION)
OUT="dist/KindleForge-KPM-v$VERSION.zip"
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

echo "Building KFPM $VERSION (linux/arm)..."
(cd KFPM && go test ./... >/dev/null && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o ../KindleForge/KindleForge/binaries/KFPM .)
if command -v upx >/dev/null 2>&1; then upx --best --lzma -q KindleForge/KindleForge/binaries/KFPM >/dev/null; fi

cp -r KindleForge/KindleForge "$STAGE/KindleForge"
cp KindleForge/KindleForge.sh KindleForge/KFPM-Diag.sh INSTALL.md TESTING.md LICENSE "$STAGE/"
mkdir -p dist && rm -f "$OUT"
(cd "$STAGE" && python3 -m zipfile -c "$OLDPWD/$OUT" KindleForge KindleForge.sh KFPM-Diag.sh INSTALL.md TESTING.md LICENSE)
echo "$OUT"
sha256sum "$OUT"
