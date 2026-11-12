#!/usr/bin/env bash
set -euo pipefail

fixture_timeout=110s
case ${1:-history} in
    history) fixture='^TestScopedFleetHistoryOffline$'; auxiliary=${OWNSOLID_TEST_VMALERT:-} ;;
    history-http) fixture='^TestFleetHistoryAuthenticatedHTTPOffline$'; auxiliary=${OWNSOLID_TEST_VMALERT:-} ;;
    history-retention) fixture='^TestFleetHistoryNinetyDayAggregates$'; auxiliary=${OWNSOLID_TEST_VICTORIA_METRICS:-}; fixture_timeout=180s ;;
    history-tls) fixture='^TestFleetHistoryPrivateTLS$'; auxiliary=${OWNSOLID_TEST_VMALERT:-} ;;
    cardinality) fixture='^TestScopedSeriesCardinalityOffline$'; auxiliary=${OWNSOLID_TEST_VICTORIA_METRICS:-} ;;
    scrape-inventory) fixture='^TestNodeScrapeInventoryThroughVmagent$'; auxiliary=${OWNSOLID_TEST_VMAGENT:-} ;;
    *) echo "Choose history, history-http, history-retention, history-tls, cardinality or scrape-inventory." >&2; exit 2 ;;
esac
image=${OWNSOLID_TEST_POSTGRES_IMAGE:-}
metrics=${OWNSOLID_TEST_VICTORIA_METRICS:-}
if [[ ! $image =~ ^sha256:[a-f0-9]{64}$ || $metrics != /* || ! -x $metrics || $auxiliary != /* || ! -x $auxiliary ]]; then
    echo "Set OWNSOLID_TEST_POSTGRES_IMAGE to the verified local image ID and the required test binary paths to verified absolute executables." >&2
    exit 2
fi
cd "$(dirname "$0")/../../../../../.."
df -h /
free -m
ss -lntp
if [[ $(free -m | awk '$1=="Mem:" {print $4}') -lt 2048 ]]; then
    echo "The shared host is below the free-memory floor." >&2
    exit 75
fi
umask 077
work=$(mktemp -d)
container=
cleanup() {
    if [[ -n $container ]]; then
        docker stop --time 10 "$container" >/dev/null
        docker rm "$container" >/dev/null
        printf 'Stopped and removed PostgreSQL fixture %s\n' "$container"
    fi
    rm -f -- "$work/monitoring.test" "$work/dsn"
    rmdir -- "$work"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
export GOMAXPROCS=2
go -C services/control test -p 2 -tags repository_integration,monitoring_integration -c \
    -o "$work/monitoring.test" ./internal/modules/monitoring
container=$(docker run --detach --pull never --network none --memory 384m \
    --name "lane-obs-history-${work##*.}" \
    --tmpfs /var/lib/postgresql:rw,nosuid,nodev,size=256m \
    -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_USER=fixture \
    -e POSTGRES_DB=ownsolid_history_test "$image" \
    -c listen_addresses=127.0.0.1 -c shared_buffers=16MB -c max_connections=16)
printf 'Started PostgreSQL fixture %s on isolated port 5432\n' "$container"
pid=$(docker inspect --format '{{.State.Pid}}' "$container")
printf 'postgresql://fixture@127.0.0.1:5432/ownsolid_history_test?sslmode=disable\n' >"$work/dsn"
export PROJECT_ONE_REPOSITORY_ENGINE=postgresql
export PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE="$work/dsn"
export PROJECT_ONE_TEST_DSN_FILE="$work/dsn"
export PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA="m20_repository_$(tr -d '-' </proc/sys/kernel/random/uuid)"
cd services/control/internal/modules/monitoring
nsenter --target "$pid" --net unshare --pid --fork --kill-child --mount-proc bash -ec '
    ip link set lo up
    test "$(ip -o link show | wc -l)" -eq 1
    ip -brief address
    test -z "$(ip route show default)"
    test -z "$(ip -6 route show default)"
    ss -lntp
    for attempt in $(seq 1 80); do
        if (exec 3<>/dev/tcp/127.0.0.1/5432) 2>/dev/null; then
            docker exec "$2" psql -X -U fixture -d ownsolid_history_test -v ON_ERROR_STOP=1 -c "$3"
            exec "$1" -test.v -test.run "$4" -test.timeout "$5"
        fi
        sleep 0.25
    done
    echo "The isolated PostgreSQL listener did not become ready." >&2
    exit 1
' bash "$work/monitoring.test" "$container" \
    "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC;" "$fixture" "$fixture_timeout"
