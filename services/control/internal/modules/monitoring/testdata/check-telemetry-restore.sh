#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )) || [[ $1 != /* ]]; then
  echo "Usage: bash $0 /absolute/path/to/verified/upstream-binaries" >&2
  exit 1
fi
tools=$1
for binary in victoria-metrics-prod victoria-logs-prod vmbackup-prod vmrestore-prod; do
  test -x "$tools/$binary"
done
test "$(ip -o link show | wc -l)" -eq 1
ip link show lo >/dev/null
test -z "$(ss -Hlnt 'sport = :58420 or sport = :58421 or sport = :58428 or sport = :59428')"

umask 077
work=$(mktemp -d)
stop_store() {
  if [[ -n ${store_pid:-} ]]; then
    kill -TERM "$store_pid" 2>/dev/null || true
    wait "$store_pid"
    unset store_pid
  fi
}
cleanup() {
  stop_store
  find "$work" -type f -delete
  find "$work" -type l -delete
  find "$work" -depth -type d -empty -delete
}
trap cleanup EXIT

get() { curl --noproxy '*' -fsS --max-time 15 "$@"; }
start_store() {
  local binary=$1 data=$2 origin=$3
  shift 3
  GOMAXPROCS=2 "$tools/$binary" -memory.allowedBytes=256MB -storageDataPath="$data" \
    -httpListenAddr="${origin#http://}" "$@" >"$work/store.log" 2>&1 &
  store_pid=$!
  local attempt
  for attempt in $(seq 1 30); do
    if get "$origin/health" >/dev/null 2>&1; then return; fi
    if ! kill -0 "$store_pid"; then break; fi
    sleep 1
  done
  cat "$work/store.log" >&2
  return 1
}

metrics=http://127.0.0.1:58428
start_store victoria-metrics-prod "$work/metrics" "$metrics" -search.latencyOffset=0s
get --data-binary 'ownsolid_backup_fixture 42' "$metrics/api/v1/import/prometheus"
get "$metrics/internal/force_flush"
GOMAXPROCS=2 timeout 60 "$tools/vmbackup-prod" -httpListenAddr=127.0.0.1:58420 -concurrency=2 \
  -storageDataPath="$work/metrics" -snapshot.createURL="$metrics/snapshot/create" -dst="fs://$work/metrics-backup"
stop_store

GOMAXPROCS=2 timeout 60 "$tools/vmrestore-prod" -httpListenAddr=127.0.0.1:58421 -concurrency=2 \
  -src="fs://$work/metrics-backup" -storageDataPath="$work/metrics-restore"
start_store victoria-metrics-prod "$work/metrics-restore" "$metrics" -search.latencyOffset=0s
get --get --data-urlencode query=ownsolid_backup_fixture "$metrics/api/v1/query" \
  | python3 -c 'import json,sys; r=json.load(sys.stdin); assert r["status"] == "success"; assert [s["value"][1] for s in r["data"]["result"]] == ["42"]'
stop_store
echo 'Restored the expected metric through the VictoriaMetrics query API.'

logs=http://127.0.0.1:59428
start_store victoria-logs-prod "$work/logs" "$logs"
now=$(date -u +%FT%TZ)
day=$(date -u --date="$now" +%Y%m%d)
printf '{"_time":"%s","_msg":"ownsolid backup fixture"}\n' "$now" \
  | get -H 'Content-Type: application/stream+json' --data-binary @- "$logs/insert/jsonline"
get --data "" "$logs/internal/force_flush"
snapshot=$(get --data-urlencode "partition_prefix=$day" "$logs/internal/partition/snapshot/create" \
  | python3 -c '
import json
from pathlib import Path
import sys
paths = json.load(sys.stdin)
assert len(paths) == 1
path = Path(paths[0]).resolve(strict=True)
assert path.is_dir() and path.is_relative_to(Path(sys.argv[1]).resolve(strict=True))
print(path)
' "$work/logs")
mkdir "$work/logs-backup"
rsync -aH "$snapshot/" "$work/logs-backup/"
get --data-urlencode "path=$snapshot" "$logs/internal/partition/snapshot/delete"
stop_store

mkdir -p "$work/logs-restore/partitions/$day"
rsync -aH "$work/logs-backup/" "$work/logs-restore/partitions/$day/"
start_store victoria-logs-prod "$work/logs-restore" "$logs"
get --get --data-urlencode 'query=_msg:"ownsolid backup fixture"' "$logs/select/logsql/query" \
  | python3 -c 'import json,sys; assert [json.loads(row)["_msg"] for row in sys.stdin] == ["ownsolid backup fixture"]'
stop_store
echo 'Restored the expected log through the VictoriaLogs query API.'
