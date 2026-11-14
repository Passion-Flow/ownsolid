set -euo pipefail
umask 077
test "$(uname -sm)" = 'Linux x86_64'
test -f /run/lock/ownsolid-heavy.lock
test -f /root/lane-sci-tools/heavy-test.lock
exec 8<>/run/lock/ownsolid-heavy.lock
exec 9<>/root/lane-sci-tools/heavy-test.lock
deadline=$((SECONDS + 90))
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

work=$(mktemp -d /root/lane-arc107-linux.XXXXXXXX)
printf 'Fixture root %s\n' "$work"
cp -a /root/lane-arc-audit58.QQe1KRCn/repo "$work/repo"
git -C "$work/repo" apply --check /root/lane-arc107-current-43f5a6778079/update.patch
git -C "$work/repo" apply /root/lane-arc107-current-43f5a6778079/update.patch
python3 /root/lane-arc107-current-43f5a6778079/verify-source.py "$work/repo" /root/lane-arc107-current-43f5a6778079/manifest.json
test -z "$(find "$work/repo" -name '._*' -print -quit)"
scratch=$(mktemp -d /tmp/osage.XXXXXXXX)
cleanup() { result=$?; printf "Native test/build exit: %s\n" "$result"; if [ -f "$work/test.log" ]; then grep -E "^ok[[:space:]]|^FAIL|^--- FAIL:|Error|error:" "$work/test.log" | tail -8 || true; fi; rm -rf -- "$scratch"; return "$result"; }
trap cleanup EXIT
export ARC107_WORK="$work" ARC107_SOURCE="$work/repo" TMPDIR="$scratch/"
export PATH=/root/lane-sci-tools/go/bin:/usr/sbin:/usr/bin:/sbin:/bin
export GOENV=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMAXPROCS=2
export GOCACHE=/root/lane-sci-tools/go-cache GOMODCACHE=/root/lane-sci-tools/go-mod
export GOFLAGS='-p=1 -buildvcs=false' CGO_ENABLED=1 CC=gcc
unset PROJECT_ONE_TEST_DSN PROJECT_ONE_TEST_DSN_FILE PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA
test "$(go version)" = 'go version go1.27.3 linux/amd64'
timeout 12m unshare --net bash -euc '
  ip link set lo up
  test -z "$(ip route show)"
  printf "Native transport test namespace:\n"
  ip -o addr show
  go -C "$ARC107_SOURCE/services/control" test -race -v ./internal/platform/httpserver ./internal/platform/agentgateway ./internal/platform/config ./cmd/server -count=1 -timeout=4m
  CGO_ENABLED=0 make -C "$ARC107_SOURCE" e2e-binaries
  CGO_ENABLED=0 go -C "$ARC107_SOURCE/services/control" build -trimpath -o "$ARC107_WORK/control" ./cmd/server
' > "$work/test.log" 2>&1
