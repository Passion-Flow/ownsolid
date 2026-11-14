#!/usr/bin/env bash
set -euo pipefail
umask 077
work=$(cd "$(dirname "$0")" && pwd -P)
trap 'result=$?; printf "%s\n" "$result" > "$work/exit"; date -u > "$work/finish"' EXIT
test "$(uname -sm)" = 'Linux x86_64'
test -f /run/lock/ownsolid-heavy.lock
test -f /root/lane-sci-tools/heavy-test.lock
exec 8<>/run/lock/ownsolid-heavy.lock
exec 9<>/root/lane-sci-tools/heavy-test.lock
deadline=$((SECONDS+90)); last=
while :; do
 reason=
 if ! flock -n 8 || ! flock -n 9; then reason='shared heavy-test locks occupied'
 elif pgrep -x 'make|cargo|rustc|compile|link' >/dev/null; then reason='another compiler active'
 elif ! awk '/^MemFree:/{f=$2}/^MemAvailable:/{a=$2}END{exit !(f>=2097152 && a>=8388608)}' /proc/meminfo; then reason='memory below admission'
 elif ! awk '/^full /{sub(/^avg10=/,"",$2);exit !($2<=5.0)}' /proc/pressure/io; then reason='full I/O PSI above 5%'
 elif [[ $(df --output=avail -BG / | tail -1 | tr -dc '0-9') -lt 30 ]]; then reason='disk below 30 GiB'
 fi
 if [[ -z $reason ]]; then break; fi
 flock -u 9; flock -u 8
 if [[ $reason != "$last" ]]; then printf 'Admission pending: %s\n' "$reason"; last=$reason; fi
 if ((SECONDS>=deadline)); then exit 75; fi
 sleep 15
done
date -u > "$work/start"
awk '/^MemFree:|^MemAvailable:/' /proc/meminfo
cat /proc/pressure/io
python3 "$work/prepare-source.py"
for version in 55 56 57 58; do
 source=/root/lane-arc-audit58.W1wf4RN2/schema${version}.dump
 if [[ $version == 55 || $version == 58 ]]; then source=/root/lane-arc-audit58.QQe1KRCn/schema${version}.dump; fi
 cp "$source" "$work/schema${version}.dump"
done
cp /root/lane-arc-audit58.QQe1KRCn/prefix.jsonl "$work/prefix.jsonl"
sha256sum "$work"/*.dump "$work/prefix.jsonl" > "$work/predecessor-checksums"
cat "$work/predecessor-checksums"
timeout 8m unshare --net bash "$work/build-offline.sh" 2>&1 | tee "$work/build.log"
bash "$work/replay.sh" 2>&1 | tee "$work/replay.log"
