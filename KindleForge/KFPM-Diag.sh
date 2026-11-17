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

echo "== which KPM? (KindleModding = /var/local/kmc/bin/kpm + kpm.db; gingrspacecadet = /usr/local/bin/kpm + /mnt/us/kpm/*.list)"
for p in /var/local/kmc/bin/kpm /mnt/us/kmc/bin/kpm /usr/local/bin/kpm; do ls -l "$p" 2>&1; done
command -v kpm 2>&1
echo "-- /mnt/us/kpm"; ls -la /mnt/us/kpm 2>&1; ls -la /mnt/us/kpm/packages 2>&1
echo "-- /mnt/us/kmc"; ls -la /mnt/us/kmc /mnt/us/kmc/kpm 2>&1
echo "-- /etc/kpm"; cat /etc/kpm/kpm.conf /etc/kpm/mirrors.conf 2>&1
echo "-- installed (gingrspacecadet style)"; cat /mnt/us/kpm/packages.list 2>&1
echo "-- first lines of remote list"; head -5 /mnt/us/kpm/remote_packages.list 2>&1

echo "== KindleModding KPM (if present)"
KPM=/var/local/kmc/bin/kpm; [ -x "$KPM" ] || KPM=/mnt/us/kmc/bin/kpm
if [ -x "$KPM" ]; then "$KPM" version 2>&1; "$KPM" list-repo 2>&1; else echo "not found"; fi
echo "== gingrspacecadet KPM (if present)"
if [ -x /usr/local/bin/kpm ]; then /usr/local/bin/kpm --version 2>&1; /usr/local/bin/kpm -Ql 2>&1; else echo "not found"; fi

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
echo "== last-log.txt"; tail -30 $DATA/last-log.txt 2>&1
echo "== done"
} > "$OUT" 2>&1

lipc-set-prop com.lab126.pillow pillowAlert '{ "clientParams":{ "alertId":"appAlert1", "show":true, "customStrings":[ { "matchStr":"alertTitle", "replaceStr":"KFPM Diagnostics" }, { "matchStr":"alertText", "replaceStr":"Done. Read kfpm-diag.txt over USB." } ] } }' 2>/dev/null
