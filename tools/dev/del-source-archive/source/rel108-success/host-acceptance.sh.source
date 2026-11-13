#!/usr/bin/env bash
set -euo pipefail
inputs=$(realpath "$1")
case "$inputs" in /dev/shm/lane-del-rel108-success.????????) ;; *) exit 1 ;; esac
trap 'rm -r -- "$inputs"' EXIT
cd "$inputs"
sha256sum -c SHA256SUMS
cat build-inputs.txt
[[ $(uname -sm) == "Linux x86_64" && -c /dev/net/tun ]]
df -h /root /dev/shm
free -m
cat /proc/pressure/io
ss -lntp | wc -l
ready() {
    awk '/^MemAvailable:/ {a=$2} /^MemFree:/ {f=$2} END {exit !(a>=4194304 && f>=2097152)}' /proc/meminfo &&
        awk '/^full / { split($2, score, "="); exit ! (score[2] <= 20) }' /proc/pressure/io &&
        df -Pk /root | awk 'END { exit ! ($4 >= 20 * 1024 * 1024) }'
}
if ! ready; then echo 'Resource admission pending; no workload started'; fi
exec 9>/run/lock/ownsolid-heavy.lock
for attempt in $(seq 1 12); do
    if flock -w 45 -E 75 9; then break; fi
    echo 'Shared Linux lock held; timer fixture has not started'
    if [[ $attempt == 12 ]]; then exit 75; fi
done
exec 8>/root/lane-sci-tools/heavy-test.lock
flock -n -E 75 8
ready || { echo 'Shared host preflight refused under both locks'; exit 75; }
docker image inspect pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47 >/dev/null
timeout 360 bash "$inputs/timer-acceptance.sh" "$inputs"
echo 'Combined timer acceptance finished and cleaned up'
