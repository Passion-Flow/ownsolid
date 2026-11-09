#!/usr/bin/env bash
set -euo pipefail

if (( $# != 3 )) || [[ $1 != /* || ! -x $1 || ! $2 =~ ^[a-f0-9]{64}$ || ! $3 =~ ^sha256:[a-f0-9]{64}$ ]]; then
    echo "Usage: bash $0 /absolute/path/to/registry registry-sha256 sha256:postgres-image-id" >&2
    exit 1
fi
cd "$(dirname "$0")/../../../../../.."
repo=$PWD
df -h /
free -m
ss -lntp
umask 077
work=$(mktemp -d)
cleanup() {
    if [[ -n ${postgres:-} ]]; then docker stop --time 10 "$postgres" >/dev/null; fi
    for name in site-admission.test pg-dsn; do
        if [[ -f $work/$name ]]; then rm -- "$work/$name"; fi
    done
    if [[ -d $work/mount ]]; then rmdir -- "$work/mount"; fi
    rmdir -- "$work"
}
trap cleanup EXIT

go -C services/control test -p 2 -tags 'repository_integration registry_integration' -c     -o "$work/site-admission.test" ./internal/modules/imageregistry
postgres=$(docker run -d --rm --pull never --name lane-obs-registry-pg --network none --cpus 1 --memory 768m     -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=lane_obs_registry_test "$3"     -c listen_addresses=127.0.0.1 -c port=55433 -c max_connections=12)
for attempt in $(seq 1 45); do
    if docker exec "$postgres" pg_isready -h 127.0.0.1 -p 55433 -d lane_obs_registry_test >/dev/null 2>&1; then break; fi
    if (( attempt == 45 )); then exit 1; fi
    sleep 1
done
docker exec "$postgres" psql -h 127.0.0.1 -p 55433 -U postgres -d lane_obs_registry_test     -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector WITH VERSION '0.8.7'"
printf '%s\n' 'postgres://postgres@127.0.0.1:55433/lane_obs_registry_test?sslmode=disable' >"$work/pg-dsn"
mkdir "$work/mount"
pid=$(docker inspect -f '{{.State.Pid}}' "$postgres")
nsenter -t "$pid" -n unshare --mount --propagation private bash -s -- "$work" "$repo" "$1" "$2" <<'NS'
set -euo pipefail
work=$1
test "$(ip -o link show | wc -l)" -eq 1
ip -brief address
test -z "$(ip route show default)"
test -z "$(ip -6 route show default)"
mount -t tmpfs -o size=64m,mode=0700,nodev,nosuid tmpfs "$work/mount"
trap 'umount "$work/mount"' EXIT
export TMPDIR="$work/mount"
export OWNSOLID_TEST_REGISTRY_MOUNT="$TMPDIR"
export PROJECT_ONE_REPOSITORY_ROOT="$2"
export PROJECT_ONE_REPOSITORY_ENGINE=postgresql
export PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE="$work/pg-dsn"
export PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA="m20_repository_$(python3 -c 'import secrets; print(secrets.token_hex(16))')"
export PROJECT_ONE_REGISTRY_NATIVE_BINARY="$3"
export PROJECT_ONE_REGISTRY_NATIVE_SHA256="$4"
export PROJECT_ONE_REGISTRY_NATIVE_OPERATION_SECONDS=15
export PROJECT_ONE_REGISTRY_NATIVE_MEMORY_MIB=256
"$work/site-admission.test" -test.v -test.run '^TestActualSiteRegistryPinAdmissionBoundary$' -test.timeout 3m
NS
