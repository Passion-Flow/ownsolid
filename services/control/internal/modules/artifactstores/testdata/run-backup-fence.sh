#!/usr/bin/env bash
set -euo pipefail

if (( $# != 2 )) || [[ $1 != /* || ! -x $1 || ! $2 =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "Usage: bash $0 /absolute/path/to/weed sha256:postgres-image-id" >&2
  exit 1
fi
weed=$1
image=$2
cd "$(dirname "$0")/../../../../../.."
df -h /
free -m
ss -lntp
test -z "$(ss -Hlnt 'sport = :55432 or sport = :38333 or sport = :39333 or sport = :38080 or sport = :38888')"
docker image inspect "$image" >/dev/null
umask 077
work=$(mktemp -d)
cleanup() {
  if [[ -n ${postgres:-} ]]; then
    docker stop --time 15 "$postgres" >/dev/null
  fi
  find "$work" -type f -delete
  find "$work" -type l -delete
  find "$work" -depth -type d -empty -delete
}
trap cleanup EXIT

go -C services/control test -p 2 -tags 'repository_integration object_storage_integration' -c \
  -o "$work/backup-fence.test" ./internal/modules/artifactstores
postgres=$(docker run -d --rm --pull never --name lane-obs-backup-pg --network none --cpus 2 --memory 1g \
  -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=lane_obs_backup_test "$image" \
  -c listen_addresses=127.0.0.1 -c port=55432 -c max_connections=12)
for attempt in $(seq 1 45); do
  if docker exec "$postgres" pg_isready -h 127.0.0.1 -p 55432 -d lane_obs_backup_test >/dev/null 2>&1; then break; fi
  if (( attempt == 45 )); then
    docker logs "$postgres" >&2
    exit 1
  fi
  sleep 1
done
docker exec "$postgres" psql -h 127.0.0.1 -p 55432 -U postgres -d lane_obs_backup_test \
  -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector WITH VERSION '0.8.7'"
pid=$(docker inspect -f '{{.State.Pid}}' "$postgres")
nsenter -t "$pid" -n bash -s -- "$work" "$weed" <<'NS'
set -euo pipefail
work=$1
weed=$2
test "$(ip -o link show | wc -l)" -eq 1
ip -brief address
test -z "$(ip route show default)"
test -z "$(ip -6 route show default)"
mkdir "$work/weed" "$work/keys"
python3 - "$work" <<'PY'
import json
from pathlib import Path
import secrets
import sys

root = Path(sys.argv[1])
credentials = []
for prefix, name, actions in [
    ("", "fixture-owner", ["Admin", "Read", "Write", "List", "Tagging"]),
    ("refusal-", "fixture-refusal", ["Read:fixture-refusal"]),
]:
    access, secret = secrets.token_hex(16), secrets.token_hex(32)
    (root / "keys" / (prefix + "access")).write_text(access + "\n")
    (root / "keys" / (prefix + "secret")).write_text(secret + "\n")
    credentials.append({
        "name": name,
        "credentials": [{"accessKey": access, "secretKey": secret}],
        "actions": actions,
    })
(root / "iam.json").write_text(json.dumps({"identities": credentials}))
(root / "schema").write_text("m20_repository_" + secrets.token_hex(16))
PY
GOMAXPROCS=2 "$weed" server -ip=127.0.0.1 -ip.bind=127.0.0.1 \
  -dir="$work/weed" -master.port=39333 -master.volumeSizeLimitMB=32 \
  -volume.port=38080 -volume.max=2 -filer.port=38888 \
  -s3 -s3.port=38333 -s3.config="$work/iam.json" >"$work/weed.log" 2>&1 &
weed_pid=$!
stop_weed() {
  kill -TERM "$weed_pid" 2>/dev/null || true
  for attempt in $(seq 1 10); do
    if ! kill -0 "$weed_pid" 2>/dev/null; then break; fi
    sleep 1
  done
  if kill -0 "$weed_pid" 2>/dev/null; then kill -KILL "$weed_pid"; fi
  wait "$weed_pid" 2>/dev/null || true
}
trap stop_weed EXIT
for attempt in $(seq 1 45); do
  status=$(curl --noproxy '*' --max-time 2 -s -o /dev/null -w '%{http_code}' http://127.0.0.1:38333/ || true)
  if [[ $status = 403 ]]; then break; fi
  if (( attempt == 45 )) || ! kill -0 "$weed_pid"; then
    tail -30 "$work/weed.log" >&2
    exit 1
  fi
  sleep 1
done
printf '%s\n' 'postgres://postgres@127.0.0.1:55432/lane_obs_backup_test?sslmode=disable' >"$work/pg-dsn"
export PROJECT_ONE_REPOSITORY_ENGINE=postgresql
export PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE="$work/pg-dsn"
export PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA=$(cat "$work/schema")
export OWNSOLID_TEST_BACKUP_S3=http://127.0.0.1:38333
export OWNSOLID_TEST_BACKUP_KEYS="$work/keys"
export TMPDIR="$work"
"$work/backup-fence.test" -test.v -test.run '^TestActualObjectBackupFence$' -test.timeout 4m
NS
