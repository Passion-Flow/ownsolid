#!/usr/bin/env bash
set -euo pipefail
umask 077
inputs=$(realpath "$1")
image='pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47'
work=$(mktemp -d -t lane-del-rel108.XXXXXXXX)
project="lane-del-rel108-${work##*.}"
project=${project,,}
unit=$project
unit_dir=/run/systemd/system
units_installed=false
recorder_pid=
compose=(docker compose --env-file "$work/install/compose.env" -f "$work/install/compose.yaml")
export DOCKER_HOST=unix:///var/run/docker.sock
unset DOCKER_CONTEXT
cleanup() {
    local status=$?
    trap - EXIT
    set +e
    if "$units_installed"; then
        journalctl --quiet --no-pager -u "$unit.service" -n 40
        systemctl stop "$unit.timer" "$unit.service"
        systemctl reset-failed "$unit.service" 2>/dev/null || true
        rm -f -- "$unit_dir/$unit.service" "$unit_dir/$unit.timer" "$unit_dir/$unit.service.d/fixture.conf"
        rm -f -- "$unit_dir/$unit.timer.d/fixture.conf"
        rmdir -- "$unit_dir/$unit.service.d" "$unit_dir/$unit.timer.d"
        systemctl daemon-reload
    fi
    if [[ -f "$work/install/compose.env" ]]; then
        timeout 15 "${compose[@]}" unpause postgres >/dev/null 2>&1 || true
        timeout 25 "${compose[@]}" down --volumes --timeout 5 || status=1
    fi
    if [[ -n $recorder_pid ]]; then
        kill "$recorder_pid" 2>/dev/null || true
        wait "$recorder_pid" 2>/dev/null || true
    fi
    if [[ -f $work/egress.jsonl ]]; then cat "$work/egress.jsonl"; fi
    rm -r -- "$work"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$work/repository"
printf 'nameserver 192.0.2.53\n' > "$work/resolv.conf"
public_key=$(openssl genpkey -algorithm ED25519 | openssl pkey -pubout -outform DER | tail -c 32 | base64 | tr -d '=\n' | tr '+/' '-_')
printf '{"format_version":1,"algorithm":"Ed25519ctx","key_id":"scheduled-fixture","public_key":"%s"}\n' "$public_key" > "$work/trust.json"
cat > "$work/site.yaml" <<YAML
format_version: 1
project: $project
install_root: $work/install
compose_file: $work/compose.yaml
runtime: {uid: 10001, gid: 10001, postgres_uid: 999}
images:
  postgres: $image
  control: $image
  metrics: $image
  logs: $image
origins:
  portal: https://portal.backup.invalid:8443
  admin: https://admin.backup.invalid:8443
license: {trust_file: $work/trust.json}
smtp: {host: 127.0.0.1, from_address: platform@example.invalid}
administrator:
  email: admin@example.invalid
  display_name: Backup Fixture Admin
  installation_name: Scheduled backup fixture
  workspace_name: Default
backup_seconds: 60
backup_schedule: {directory: $work/repository, retain: 1}
YAML
cat > "$work/compose.yaml" <<YAML
name: $project
services:
  postgres:
    image: $image
    pull_policy: never
    network_mode: none
    dns: ["192.0.2.53"]
    mem_limit: 640m
    cpus: 1
    pids_limit: 128
    environment:
      POSTGRES_DB: ownsolid
      POSTGRES_USER: ownsolid_migrator
      POSTGRES_PASSWORD_FILE: /run/fixture-password
    volumes:
      - "$inputs:/inputs:ro"
      - "$work:/fixture:ro"
      - "$work/install/config:/run/projectone:ro"
      - "$work/install/operator:/run/projectone-operator:ro"
    tmpfs:
      - "/var/lib/postgresql:rw,size=384m"
    entrypoint:
      - bash
      - -ec
      - |
        while [[ ! -f /fixture/start ]]; do sleep 0.1; done
        install -o postgres -m 0400 /fixture/install/secrets/postgres-password /run/fixture-password
        exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1,172.31.254.2"
YAML
test -z "$(docker ps -aq --filter "label=com.docker.compose.project=$project")"
OWNSOLID_TIMER_FIXTURE_CONFIG="$work/site.yaml" "$inputs/materialize.test" -test.run '^TestMaterializeScheduledBackupFixture$' -test.v
"${compose[@]}" up --detach --no-build --pull never postgres
container=$("${compose[@]}" ps -q postgres)
[[ -n $container && $(docker inspect --format '{{.HostConfig.NetworkMode}}' "$container") == none ]]
[[ $(docker inspect --format '{{len .HostConfig.PortBindings}}' "$container") == 0 ]]
pid=$(docker inspect --format '{{.State.Pid}}' "$container")
in_namespace=(nsenter --target "$pid" --net)
"${in_namespace[@]}" ip link set lo up
"${in_namespace[@]}" ip addr add 172.31.254.2/32 dev lo
"${in_namespace[@]}" "$inputs/egress-recorder" -interface egress0 -log "$work/egress.jsonl" &
recorder_pid=$!
for _ in {1..50}; do
    if "${in_namespace[@]}" ip link show egress0 >/dev/null 2>&1; then break; fi
    sleep 0.1
done
"${in_namespace[@]}" ip link set egress0 up
"${in_namespace[@]}" ip addr add 198.18.0.1/32 dev egress0
"${in_namespace[@]}" ip -6 addr add 2001:db8:e9e5::1/128 dev egress0
sleep 2
"${in_namespace[@]}" ip route add default dev egress0
"${in_namespace[@]}" ip -6 route add default dev egress0
[[ $("${in_namespace[@]}" ip -o link show | wc -l) == 2 ]]
"${in_namespace[@]}" ip route show default
"${in_namespace[@]}" ip -6 route show default
"${in_namespace[@]}" "$inputs/probe"
sleep 0.2
python3 - "$work/egress.jsonl" <<'PYPROBE'
import json
from pathlib import Path
import sys
rows = [json.loads(line) for line in Path(sys.argv[1]).read_bytes().splitlines()]
assert len(rows) == 3, rows
assert [row["destination"] for row in rows] == ["192.0.2.10:9", "[2001:db8::10]:9", "192.0.2.53:53"], rows
assert rows[-1].get("dns_question") == "probe.invalid. A", rows
print("Recorder observed and blocked the IPv4, IPv6 and DNS probes")
PYPROBE
probe_hash=$(sha256sum "$work/egress.jsonl")
touch "$work/start"
for _ in {1..100}; do
    if "${compose[@]}" exec --no-TTY postgres bash -ec '[[ $(cat /proc/1/comm) == postgres ]] && pg_isready -q -U ownsolid_migrator -d ownsolid'; then break; fi
    sleep 0.2
done
query() {
    local db=$1
    shift
    "${compose[@]}" exec --no-TTY postgres psql -XAtq -v ON_ERROR_STOP=1 -U ownsolid_migrator -d "$db" "$@"
}
query ownsolid -c "CREATE EXTENSION IF NOT EXISTS vector VERSION '0.8.7'"
"${compose[@]}" exec --no-TTY --user 10001:10001 postgres /inputs/control migrate --config /run/projectone-operator/operator.yaml
"${compose[@]}" exec --no-TTY --user 10001:10001 postgres /inputs/control check-schema --config /run/projectone-operator/operator.yaml
original=$(query ownsolid -c 'SELECT oid FROM pg_database WHERE datname=current_database()')
deployment=$(query ownsolid -c 'SELECT deployment_id FROM platform_license_state WHERE id=1')
[[ $original =~ ^[0-9]+$ && $deployment =~ ^[0-9a-f-]{36}$ ]]
schema=$(query ownsolid -c 'SELECT max(version) FROM schema_migrations')
[[ $schema =~ ^[0-9]+$ && $schema -ge 58 ]]
tables_sql="SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p') AND NOT c.relispartition"
tables=$(query ownsolid -c "$tables_sql")
[[ $tables =~ ^[0-9]+$ && $tables -ge 100 ]]
printf "Observed migrated schema=%s tables=%s\n" "$schema" "$tables"

test ! -e "$unit_dir/$unit.service"
test ! -e "$unit_dir/$unit.timer"
mkdir "$unit_dir/$unit.service.d"
units_installed=true
cp "$inputs/ownsolid-backup.service" "$unit_dir/$unit.service"
cp "$inputs/ownsolid-backup.timer" "$unit_dir/$unit.timer"
cat > "$unit_dir/$unit.service.d/fixture.conf" <<UNIT
[Service]
ExecSearchPath=$inputs:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
Environment=OWNSOLID_INSTALL_CONFIG=$work/site.yaml
Environment=DOCKER_HOST=unix:///var/run/docker.sock
NetworkNamespacePath=/proc/$pid/ns/net
BindReadOnlyPaths=$work/resolv.conf:/etc/resolv.conf
UNIT
mkdir "$unit_dir/$unit.timer.d"
cat > "$unit_dir/$unit.timer.d/fixture.conf" <<'UNIT'
[Timer]
OnCalendar=
OnActiveSec=2s
AccuracySec=100ms
Persistent=no
UNIT
systemctl daemon-reload
[[ $(systemctl show -p Unit --value "$unit.timer") == "$unit.service" ]]
[[ $(systemctl show -p NetworkNamespacePath --value "$unit.service") == "/proc/$pid/ns/net" ]]
history() { "$inputs/ownsolid-install" backup-history --config "$work/site.yaml"; }
run_timer() {
    local sequence=$1 expected_result=$2 expected_expired=$3
    systemctl stop "$unit.timer"
    systemctl reset-failed "$unit.service" 2>/dev/null || true
    systemctl start "$unit.timer"
    for _ in {1..260}; do
        if history > "$work/history.json" && python3 - "$work/history.json" "$sequence" <<'PY'
import json
from pathlib import Path
import sys
runs = json.loads(Path(sys.argv[1]).read_bytes())["runs"]
sys.exit(not (len(runs) == int(sys.argv[2]) and runs[-1].get("finished_at")))
PY
        then break; fi
        sleep 0.25
    done
    "$inputs/ownsolid-install" backup-history --config "$work/site.yaml" \
        --expected-deployment-id "$deployment" > "$work/history.json"
    python3 - "$work/history.json" "$sequence" "$expected_result" "$expected_expired" "$deployment" <<'PY'
import json
from pathlib import Path
import sys
data = json.loads(Path(sys.argv[1]).read_bytes())
assert data["deployment_id"] == sys.argv[5] and data["format_version"] == 1
runs = data["runs"]
assert [row["sequence"] for row in runs] == list(range(1, int(sys.argv[2]) + 1))
row = runs[-1]
assert row["outcome"] == sys.argv[3] and row.get("finished_at"), row
assert row["backup_verified"] is True, row
if sys.argv[4] == "-":
    assert "expired_backups" not in row, row
    assert row.get("error_code") == "INSTALL_BACKUP_RETENTION_FAILED", row
else:
    assert row.get("expired_backups") == int(sys.argv[4]), row
    assert "error_code" not in row, row
print("Timer attempt", row["sequence"], row["outcome"], "expired=", row.get("expired_backups"),
      "error=", row.get("error_code"))
PY
    for _ in {1..40}; do
        state=$(systemctl show -p ActiveState --value "$unit.service")
        if [[ $state == inactive || $state == failed ]]; then break; fi
        sleep 0.1
    done
    systemctl show -p Result -p ExecMainStatus "$unit.service"
    if [[ $expected_result == succeeded ]]; then
        [[ $(systemctl show -p Result --value "$unit.service") == success ]]
    else
        [[ $(systemctl show -p Result --value "$unit.service") == exit-code ]]
    fi
}

shopt -s nullglob
scratch=rel108_fixture_readback
verify_dump() {
    local archive=$1
    test -s "$archive/database.dump"
    python3 - "$archive/manifest.json" "$archive/database.dump" "$schema" "$tables" <<'PYDUMP'
import hashlib
import json
from pathlib import Path
import sys
manifest = json.loads(Path(sys.argv[1]).read_bytes())
path = Path(sys.argv[2])
with path.open("rb") as dump:
    assert dump.read(5) == b"PGDMP"
    dump.seek(0)
    assert hashlib.file_digest(dump, "sha256").hexdigest() == manifest["database"]["sha256"]
assert path.stat().st_size == manifest["database"]["bytes"]
assert manifest["schema_version"] == int(sys.argv[3]) and len(manifest["tables"]) == int(sys.argv[4])
print(f"Verified archived dump bytes and manifest for schema {manifest['schema_version']}, "
      f"{len(manifest['tables'])} tables")
PYDUMP
    query postgres -c "CREATE DATABASE $scratch TEMPLATE template0"
    "${compose[@]}" exec --no-TTY postgres pg_restore -U ownsolid_migrator -d "$scratch" --exit-on-error < "$archive/database.dump"
    [[ $(query "$scratch" -c 'SELECT deployment_id FROM platform_license_state WHERE id=1') == "$deployment" ]]
    [[ $(query "$scratch" -c 'SELECT max(version) FROM schema_migrations') == "$schema" ]]
    [[ $(query "$scratch" -c "$tables_sql") == "$tables" ]]
    query postgres -c "DROP DATABASE $scratch"
}

run_timer 1 succeeded 0
first=("$work/repository"/ownsolid-scheduled-*)
[[ ${#first[@]} == 1 ]]
verify_dump "${first[0]}"
foreign="$work/repository/ownsolid-backup-20261108T000000Z"
mkdir "$foreign"
printf 'operator backup fixture\n' > "$foreign/keep"

run_timer 2 succeeded 1
second=("$work/repository"/ownsolid-scheduled-*)
[[ ${#second[@]} == 1 && ${second[0]} != "${first[0]}" && ! -e ${first[0]} && -f "$foreign/keep" ]]
verify_dump "${second[0]}"
second_hash=$(sha256sum "${second[0]}/database.dump")
echo "Expired archive was removed and its successful history entry remained"

# The exact canonical timestamp is synthetic. The malformed owner is created
# only under this fixture's repository and triggers the real retention reader.
bad="$work/repository/ownsolid-scheduled-$deployment-20000101T000000Z"
mkdir "$bad"
ln -s /dev/null "$bad/schedule-owner.json"
run_timer 3 failed -
third=("$work/repository"/ownsolid-scheduled-*)
[[ ${#third[@]} == 3 && $(sha256sum "${second[0]}/database.dump") == "$second_hash" ]]
published=
for item in "${third[@]}"; do
    if [[ $item != "$bad" && $item != "${second[0]}" ]]; then published=$item; fi
done
test -n "$published"
verify_dump "$published"
echo "Retention refused the fixture owner link after publishing a readable new archive"

test "$(readlink "$bad/schedule-owner.json")" = /dev/null
rm -- "$bad/schedule-owner.json"
rmdir -- "$bad"
run_timer 4 succeeded 2
final=("$work/repository"/ownsolid-scheduled-*)
[[ ${#final[@]} == 1 && ! -e ${second[0]} && ! -e $published && -f "$foreign/keep" ]]
verify_dump "${final[0]}"
[[ $(query ownsolid -c 'SELECT oid FROM pg_database WHERE datname=current_database()') == "$original" ]]
python3 - "$work/history.json" <<'PYHISTORY'
import json
from pathlib import Path
import sys
runs = json.loads(Path(sys.argv[1]).read_bytes())["runs"]
assert [r["outcome"] for r in runs] == ["succeeded", "succeeded", "failed", "succeeded"]
assert [r.get("expired_backups") for r in runs] == [0, 1, None, 2]
assert all(r["backup_verified"] for r in runs)
assert runs[2].get("error_code") == "INSTALL_BACKUP_RETENTION_FAILED"
print("History retained all four verified publications, including the earlier retention failure")
PYHISTORY
systemctl stop "$unit.timer" "$unit.service"
"${compose[@]}" stop --timeout 5 postgres
kill -0 "$recorder_pid"
[[ $(sha256sum "$work/egress.jsonl") == "$probe_hash" ]]
echo "Only the recorder's IPv4, IPv6 and DNS probes"
