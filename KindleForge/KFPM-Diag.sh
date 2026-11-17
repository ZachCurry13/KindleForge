#!/bin/sh
# Name: KFPM Diagnostics
# Author: KindleForge KPM Edition
# DontUseFBInk

# Writes /mnt/us/kfpm-diag.txt. Copy this next to KindleForge.sh in documents/,
# run it from the Home Library, then read the file over USB.

OUT=/mnt/us/kfpm-diag.txt
KFPM=/var/local/mesquite/KindleForge/binaries/KFPM
DATA=/mnt/us/.KFPM

{
echo "== date"; date
echo "== firmware"; cat /etc/version.txt 2>/dev/null; grep -i version /etc/prettyversion.txt 2>/dev/null
echo "== ABI loader (hf if present)"; ls -l /lib/ld-linux-armhf.so.3 2>&1

echo "== kpm location"
for p in /var/local/kmc/bin/kpm /mnt/us/kmc/bin/kpm; do ls -l "$p" 2>&1; done
command -v kpm 2>&1
KPM=/var/local/kmc/bin/kpm; [ -x "$KPM" ] || KPM=$(command -v kpm)
echo "== kpm version"; "$KPM" version 2>&1
echo "== kpm list-repo"; "$KPM" list-repo 2>&1

echo "== sqlite3 + kpm db"
command -v sqlite3 2>&1
ls -l /mnt/us/kmc/kpm/kpm.db 2>&1
sqlite3 /mnt/us/kmc/kpm/kpm.db "SELECT id,version_major,version_minor,version_patch FROM installed_packages;" 2>&1
ls /mnt/us/kmc/kpm/packages 2>&1

echo "== free space (need room for the KFPM binary)"
df -h /var/local /mnt/us 2>&1
ls -l "$KFPM" 2>&1

echo "== HTTPS / CA certs"
ls /etc/ssl/certs 2>&1 | head -3
curl -sS -m 20 -o /dev/null -w "manifest http=%{http_code}\n" https://kindletweaks.github.io/Repo/manifest.json 2>&1

echo "== KFPM -abi"; "$KFPM" -abi 2>&1
echo "== KFPM -s (sync; needs Wi-Fi)"; "$KFPM" -s -v 2>&1
echo "== KFPM -a"; "$KFPM" -a 2>&1
echo "== KFPM -l"; "$KFPM" -l 2>&1
echo "== data dir"; ls -l $DATA $DATA/cache 2>&1
echo "== last.log"; tail -30 $DATA/last.log 2>&1
echo "== done"
} > "$OUT" 2>&1

lipc-set-prop com.lab126.pillow pillowAlert '{ "clientParams":{ "alertId":"appAlert1", "show":true, "customStrings":[ { "matchStr":"alertTitle", "replaceStr":"KFPM Diagnostics" }, { "matchStr":"alertText", "replaceStr":"Done. Read kfpm-diag.txt over USB." } ] } }' 2>/dev/null
