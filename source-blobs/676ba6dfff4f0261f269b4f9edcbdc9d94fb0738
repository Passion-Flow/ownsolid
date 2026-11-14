#!/usr/bin/env bash
set -euo pipefail

image=${OWNSOLID_TEST_POSTGRES_IMAGE:-}
alertmanager=${OWNSOLID_TEST_ALERTMANAGER:-}
if [[ ! $image =~ ^sha256:[a-f0-9]{64}$ || $alertmanager != /* || ! -x $alertmanager ]]; then
    echo "Set OWNSOLID_TEST_POSTGRES_IMAGE to a verified local image ID and OWNSOLID_TEST_ALERTMANAGER to its verified absolute binary." >&2
    exit 2
fi
cd "$(dirname "$0")/../../../../../../.."
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
    fi
    if [[ -f $work/alerting.test ]]; then rm -- "$work/alerting.test"; fi
    rmdir -- "$work"
}
trap cleanup EXIT
export GOMAXPROCS=2
go -C services/control test -p 2 -tags alerting_integration,alerting_candidate -c \
    -o "$work/alerting.test" ./internal/modules/monitoring/alerting
container=$(docker run --detach --pull never --network none --memory 384m \
    --name "lane-obs-escalation-${work##*.}" \
    --tmpfs /var/lib/postgresql:rw,nosuid,nodev,size=256m \
    -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_USER=fixture \
    -e POSTGRES_DB=ownsolid_escalation_test "$image" \
    -c listen_addresses=127.0.0.1 -c shared_buffers=16MB -c max_connections=16)
printf "PostgreSQL fixture %s\n" "$container"
pid=$(docker inspect --format '{{.State.Pid}}' "$container")
cd services/control/internal/modules/monitoring/alerting
nsenter --target "$pid" --net bash -ec '
    ip link set lo up
    test "$(ip -o link show | wc -l)" -eq 1
    ip -brief address
    test -z "$(ip route show default)"
    test -z "$(ip -6 route show default)"
    export OWNSOLID_TEST_ESCALATION_DSN="postgresql://fixture@127.0.0.1:5432/ownsolid_escalation_test?sslmode=disable"
    exec "$1" -test.v -test.run "^TestEscalationCandidateWithAlertmanager$" -test.timeout 95s
' bash "$work/alerting.test"
