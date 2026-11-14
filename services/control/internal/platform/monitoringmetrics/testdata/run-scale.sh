#!/usr/bin/env bash
set -euo pipefail

if (( $# != 3 )) || [[ $1 != /* || ! -x $1 ]]; then
  echo "Usage: bash $0 /absolute/path/to/victoria-metrics-prod nodes rounds" >&2
  exit 1
fi
store=$1
nodes=$2
rounds=$3
if [[ ! $nodes =~ ^[0-9]+$ || ! $rounds =~ ^[0-9]+$ ]]; then
  echo "Node and round counts must be integers" >&2
  exit 1
fi
df -h /
free -m
ss -lntp
if (( nodes >= 10000 && $(free -m | awk '/^Mem:/ {print $4}') < 2048 )); then
  echo "Free at least 2 GiB before running the full scale fixture" >&2
  exit 1
fi

cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'find "$work" -type f -delete; find "$work" -depth -type d -empty -delete' EXIT
GO111MODULE=off go test -c -o "$work/scale.test" scale_integration_test.go
unshare --net bash -s -- "$work" "$store" "$nodes" "$rounds" <<'NS'
set -euo pipefail
work=$1
store=$2
nodes=$3
rounds=$4
ip link set lo up
ip -brief address
test -z "$(ip route show default)"
test -z "$(ip -6 route show default)"

cleanup() {
  kill -TERM "$pid" 2>/dev/null || true
  wait "$pid"
  printf 'VictoriaMetrics allocated bytes after shutdown: '
  du -s -B 1 "$work/data" | cut -f 1
  echo 'VictoriaMetrics stopped'
}
GOMAXPROCS=6 "$store" -memory.allowedBytes=8GB -storageDataPath="$work/data" \
  -httpListenAddr=127.0.0.1:58428 >"$work/store.log" 2>&1 &
pid=$!
trap cleanup EXIT
for attempt in $(seq 1 30); do
  if curl --noproxy '*' -fsS --max-time 2 http://127.0.0.1:58428/health >/dev/null 2>&1; then break; fi
  if (( attempt == 30 )) || ! kill -0 "$pid"; then
    cat "$work/store.log" >&2
    exit 1
  fi
  sleep 1
done
GOMAXPROCS=6 OWNSOLID_SCALE_METRICS_ADDRESS=http://127.0.0.1:58428 \
  OWNSOLID_SCALE_NODES="$nodes" OWNSOLID_SCALE_ROUNDS="$rounds" "$work/scale.test" \
  -test.v -test.run '^TestVictoriaMetricsFleetScale$' -test.timeout 20m
NS
