set -euo pipefail
umask 077
test "$(uname -sm)" = 'Linux x86_64'
test -f /run/lock/ownsolid-heavy.lock
test -f /root/lane-sci-tools/heavy-test.lock
exec 8<>/run/lock/ownsolid-heavy.lock
exec 9<>/root/lane-sci-tools/heavy-test.lock
deadline=$((SECONDS + 600))
last=
while :; do
  reason=
  if ! flock -n 8 || ! flock -n 9; then
    reason='shared heavy-test lock is occupied'
  elif pgrep -x 'make|cargo|rustc|compile|link' >/dev/null; then
    reason='another repository gate or compiler is active'
  elif ! awk '
    /^MemFree:/ {free=$2} /^MemAvailable:/ {available=$2}
    END {exit !(free>=2097152 && available>=8388608)}
  ' /proc/meminfo; then
    reason='memory admission is below threshold'
  elif ! awk '/^full / {sub(/^avg10=/,"",$2); exit !($2 <= 5.0)}' /proc/pressure/io; then
    reason='full I/O PSI avg10 is above 5%'
  elif [[ $(df --output=avail -BG / | tail -1 | tr -dc '0-9') -lt 30 ]]; then
    reason='available disk is below 30 GiB'
  fi
  if [[ -z $reason ]]; then break; fi
  flock -u 9
  flock -u 8
  if [[ $reason != "$last" ]]; then printf 'Waiting for admission. %s\n' "$reason"; last=$reason; fi
  if (( SECONDS >= deadline )); then exit 75; fi
  sleep 15
done
printf 'Exclusive Linux admission acquired\n'
date -u
df -h /
awk '/^MemFree:|^MemAvailable:/' /proc/meminfo
cat /proc/pressure/io
ss -lnt '( sport = :5432 )'
work=$(mktemp -d /root/lane-arc-audit58.XXXXXXXX)
printf 'Fixture root %s\n' "$work"
tar -xzf - -C "$work"
printf 'Candidate %s\n' "$(cat "$work/revision")"
up="$work/repo/services/control/migrations/postgresql/000058_audit_chain_head_scope.up.sql"
down="$work/repo/services/control/migrations/postgresql/000058_audit_chain_head_scope.down.sql"
printf '%s  %s\n' c4d228212ca9a4107dc5e4a78468f9f7a35dfba818564d406a0a8861cdca1413 "$up" | sha256sum -c
printf '%s  %s\n' f9e79fe6b7ddaa15b400e8b53283cac5b67286075112c25a96bdcdf12aef0ebc "$down" | sha256sum -c
bash "$work/replay-active.sh" "$work/repo" 2>&1 | tee "$work/active-replay.log"
