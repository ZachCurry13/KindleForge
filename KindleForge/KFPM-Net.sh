#!/bin/sh
# Name: KFPM Network Test
# Author: KindleForge KPM Edition
# DontUseFBInk

# Tests the Kindle's internet on EVERY Wi-Fi network it has saved, one after
# another, then switches back to the network you started on. It never joins
# new networks and never reads or prints passwords.
#
# Takes a few minutes. A popup appears when it starts and another when it is
# done. Result: /mnt/us/kfpm-net.txt (top level of the Kindle's storage).
#
# Privacy: Wi-Fi names are shortened (first 2 letters + length), device
# addresses are masked. Glance through the file before sharing it.

OUT="${KFPM_NET_OUT:-/mnt/us/kfpm-net.txt}"
RESOLV="${KFPM_RESOLV:-/etc/resolv.conf}"
IFACE=wlan0
CONNECT_WAIT=45   # seconds to wait for each network
MAX_NETS=6

alert() {
  lipc-set-prop com.lab126.pillow pillowAlert "{ \"clientParams\":{ \"alertId\":\"appAlert1\", \"show\":true, \"customStrings\":[ { \"matchStr\":\"alertTitle\", \"replaceStr\":\"KFPM Network Test\" }, { \"matchStr\":\"alertText\", \"replaceStr\":\"$1\" } ] } }" 2>/dev/null
}

hide() { sed -E 's/([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}/xx:xx:xx:xx:xx:xx/g; s/ESSID:"[^"]*"/ESSID:"(hidden)"/g'; }

# wpa_cli talks to the supplicant; try the usual control socket locations.
WPA=""
if command -v wpa_cli >/dev/null 2>&1; then
  for p in "" "-p /var/run/wpa_supplicant" "-p /var/run/wpa_supplicant/"; do
    if wpa_cli $p -i $IFACE status >/dev/null 2>&1; then WPA="wpa_cli $p -i $IFACE"; break; fi
  done
fi

short_name() { # first 2 chars + length, so you can tell networks apart without exposing the name
  n="$1"; len=$(printf %s "$n" | wc -c)
  printf '%s... (%s chars)' "$(printf %s "$n" | cut -c1-2)" "$len"
}

wpa_field() { $WPA status 2>/dev/null | sed -n "s/^$1=//p" | head -1; }

wait_connected() { # connected = supplicant COMPLETED and an IP address
  i=0
  while [ $i -lt $CONNECT_WAIT ]; do
    st=$(wpa_field wpa_state); ip=$(wpa_field ip_address)
    if [ "$st" = COMPLETED ] && [ -n "$ip" ]; then sleep 3; return 0; fi
    i=$((i+3)); sleep 3
  done
  return 1
}

# DNS helpers. busybox nslookup prints "Name:" then "Address..." on success.
dns_blocked() { nslookup "$2" "$1" 2>&1 | grep -A3 '^Name:' | grep -q -E '(^|[^0-9.])(0\.0\.0\.0|127\.0\.0\.1)([^0-9]|$)'; }

# ok only if it answered with a real address (0.0.0.0 = blocked, not ok)
dns_ok() { nslookup "$2" "$1" 2>&1 | grep -A3 '^Name:' | grep -q 'Address' && ! dns_blocked "$1" "$2"; }

BLK=0
dns_row() { # dns_row <server> <label>
  srv="$1"; label="$2"
  for d in github.com raw.githubusercontent.com api.github.com; do
    if dns_blocked "$srv" "$d"; then r="BLOCKED (answer 0.0.0.0: a blocklist)"; BLK=1
    elif dns_ok "$srv" "$d"; then r="ok"
    else r="NO ANSWER / FAILED"; fi
    echo "   $d via $label: $r"
  done
}

net_test() {
  echo "-- connection details"
  echo "cmState: $(lipc-get-prop com.lab126.wifid cmState 2>&1)"
  if [ -n "$WPA" ]; then
    $WPA status 2>&1 | grep -E '^(wpa_state|freq|key_mgmt|pairwise_cipher|group_cipher|ip_address)='
  fi
  iwconfig $IFACE 2>&1 | grep -E "Frequency|Link Quality|Signal|Bit Rate|Tx-Power" | hide
  GW=$(route -n 2>/dev/null | awk '$1=="0.0.0.0"{print $2; exit}')
  NS=$(awk '/^nameserver/{print $2}' "$RESOLV" 2>/dev/null | head -2)
  MYIP=$(wpa_field ip_address)
  echo "-- addresses: Kindle=${MYIP:-?}  gateway=${GW:-none}  dns=$(echo $NS)"

  echo "-- A) router (gateway) reachable?"
  GWOK=0; [ -n "$GW" ] && ping -c 2 -W 3 "$GW" >/dev/null 2>&1 && GWOK=1
  echo "   ${GW:-none}: $([ $GWOK = 1 ] && echo yes || echo NO)"
  echo "-- B) internet by NUMBER, no DNS needed (1.1.1.1)"
  NUMOK=0; ping -c 2 -W 3 1.1.1.1 >/dev/null 2>&1 && NUMOK=1
  echo "   $([ $NUMOK = 1 ] && echo yes || echo NO)"
  echo "-- C) is each DNS server this network gave us reachable? (the Pi-hole?)"
  NSOK=0
  for n in $NS; do
    if ping -c 2 -W 3 "$n" >/dev/null 2>&1; then echo "   $n: reachable"; NSOK=1; else echo "   $n: NOT reachable (ping)"; fi
  done
  echo "-- D) names through THIS network's DNS (what the Kindle really uses)"
  NETOK=0; BLK=0
  for n in $NS; do dns_row "$n" "$n"; dns_ok "$n" github.com && dns_ok "$n" raw.githubusercontent.com && NETOK=1; done
  echo "-- E) same names through PUBLIC DNS (bypasses the Pi-hole / router)"
  PUBOK=0
  dns_row 1.1.1.1 "1.1.1.1"; dns_ok 1.1.1.1 github.com && dns_ok 1.1.1.1 raw.githubusercontent.com && PUBOK=1
  echo "-- F) https by number / by name / what KindleForge needs"
  curl -sS -m 10 -o /dev/null -w "   by number http=%{http_code}\n" https://1.1.1.1 2>&1
  curl -sS -m 12 -o /dev/null -w "   github.com http=%{http_code}\n" https://github.com 2>&1
  curl -sS -m 12 -o /dev/null -w "   raw.githubusercontent.com http=%{http_code}\n" https://raw.githubusercontent.com/gingrspacecadet/kpm/main/packages.list 2>&1

  KM=$(wpa_field key_mgmt); FQ=$(wpa_field freq)
  echo "-- VERDICT"
  if [ -z "$GW" ] && [ -z "$NS" ] && [ "$(wpa_field wpa_state)" = COMPLETED ]; then
    echo "   Wi-Fi ASSOCIATED (security: ${KM:-?}, ${FQ:-?} MHz) but the Kindle got NO gateway and NO DNS server."
    echo "   => Connection setup (DHCP/route) never finished. Not a Pi-hole problem: it never got far enough to ask DNS."
    case "$KM" in *SAE*) echo "   => This network uses WPA3 (SAE). Kindles are unreliable with WPA3: try WPA2-PSK (AES) only.";; esac
    [ -n "$FQ" ] && [ "$FQ" -ge 4900 ] 2>/dev/null && echo "   => It is on 5 GHz. Try a 2.4 GHz network name too."
  elif [ "$NUMOK" = 0 ] && [ "$GWOK" = 0 ]; then
    echo "   Cannot even reach the router: Wi-Fi link or router/AP problem (or client isolation / guest network)."
  elif [ "$NUMOK" = 0 ]; then
    echo "   Router reachable but no internet by number: router/modem/WAN or a firewall rule blocking this device."
  elif [ "$BLK" = 1 ] && [ "$PUBOK" = 1 ]; then
    echo "   This network's DNS answers 0.0.0.0 for GitHub names: the Pi-hole (or its blocklist/group) is BLOCKING them."
    echo "   => Pi-hole: whitelist github.com, raw.githubusercontent.com and api.github.com, or put the Kindle in a group without blocklists."
  elif [ "$NETOK" = 1 ]; then
    echo "   All good: internet and this network's DNS both work."
  elif [ "$PUBOK" = 1 ] && [ "$NSOK" = 0 ]; then
    echo "   Internet works, public DNS works, but this network's DNS server is UNREACHABLE from the Kindle."
    echo "   => Pi-hole/DNS server is down, on another VLAN/subnet the Kindle cannot reach, or firewalled from it."
  elif [ "$PUBOK" = 1 ]; then
    echo "   Internet works, public DNS works, but this network's DNS server is reachable and NOT ANSWERING (or blocking) for the Kindle."
    echo "   => Pi-hole: enable 'Permit all origins' (Settings > DNS) or add the Kindle's subnet; check the Query Log for the Kindle's IP."
  else
    echo "   Internet by number works but DNS fails even via public DNS: UDP port 53 to the outside is blocked (a firewall or forced-DNS rule)."
  fi
}

alert "Started. This takes a few minutes. Wait for the Done popup."

{
echo "== date"; date
echo "== kindle"; head -2 /etc/version.txt 2>/dev/null
echo "== wpa_cli: ${WPA:-NOT AVAILABLE (only the current connection can be tested)}"

echo "== turning Wi-Fi on (scriptlets do not) and waiting for a connection"
lipc-set-prop com.lab126.cmd wirelessEnable 1 2>&1
if [ -n "$WPA" ]; then wait_connected && echo "connected" || echo "not connected after ${CONNECT_WAIT}s"; else sleep 15; fi

if [ -z "$WPA" ]; then
  echo; echo "######## current connection ########"
  net_test
  echo "== done"
else
  ORIG=$(wpa_field id)
  echo "starting network id: ${ORIG:-none}"
  echo "== saved networks (names shortened)"
  NETS=$($WPA list_networks 2>/dev/null | awk -F'\t' 'NR>1 && $1 ~ /^[0-9]+$/ {print $1 "\t" $2 "\t" $4}')
  echo "$NETS" | while IFS="$(printf '\t')" read -r id name flags; do
    [ -n "$id" ] && echo "  id=$id  $(short_name "$name")  $flags"
  done

  echo
  echo "######## STARTING network (id ${ORIG:-?}) ########"
  net_test

  COUNT=0
  echo "$NETS" | while IFS="$(printf '\t')" read -r id name flags; do
    [ -z "$id" ] && continue
    [ "$id" = "$ORIG" ] && continue
    COUNT=$((COUNT+1)); [ $COUNT -gt $MAX_NETS ] && break
    echo; echo "######## saved network id=$id  $(short_name "$name") ########"
    $WPA enable_network "$id" >/dev/null 2>&1
    $WPA select_network "$id" >/dev/null 2>&1
    sleep 5
    if wait_connected && [ "$(wpa_field id)" = "$id" ]; then net_test; else
      echo "COULD NOT SWITCH to this network (the Kindle is on id $(wpa_field id), wanted $id)."
      echo "It may be out of range, or the Kindle's Wi-Fi manager refused the switch. Connect to it in Settings > Wi-Fi, then run this test again."
      $WPA status 2>&1 | grep -E '^(wpa_state|key_mgmt)='
    fi
  done

  echo; echo "== switching back to the starting network"
  if [ -n "$ORIG" ]; then $WPA select_network "$ORIG" >/dev/null 2>&1; fi
  $WPA enable_network all >/dev/null 2>&1
  wait_connected && echo "reconnected" || echo "could not confirm reconnection; reconnect in Settings > Wi-Fi if needed"
  echo "== done"
fi
} > "$OUT" 2>&1

alert "Done. Read kfpm-net.txt over USB."
