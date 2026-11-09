#!/usr/bin/env bash
# Compare native and logically restored PostgreSQL migrations without an uplink.
set -euo pipefail
if [[ $# != 3 || $EUID != 0 || $(uname -sm) != "Linux x86_64" ||
      ! $2 =~ @sha256:[a-f0-9]{64}$ || ! $3 =~ ^(blocked|current)$ ]]; then
    echo "usage (Linux amd64 root): $0 inputs-dir postgres-image@sha256:digest blocked|current" >&2
    exit 2
fi
inputs=$(realpath "$1")
image=$2
expected=$3
unset DOCKER_CONTEXT
export DOCKER_HOST=unix:///var/run/docker.sock
for file in control-schema55 control-schema56 control-candidate egress-recorder install.test; do
    test -x "$inputs/$file"
done
test -f "$inputs/000056.up.sql"
[[ $(docker image inspect "$image" --format '{{.Os}}/{{.Architecture}}') == linux/amd64 ]]
umask 077
work=$(mktemp -d -t lane-del-schema.XXXXXXXX)
container=
recorder_pid=
cleanup() {
    local status=$?
    if [[ -n $container ]] && ! timeout 15s docker rm -fv "$container" >/dev/null; then
        echo "Could not remove fixture $container" >&2
        status=1
    fi
    if [[ -n $recorder_pid ]]; then
        kill "$recorder_pid" 2>/dev/null || true
        wait "$recorder_pid" || true
    fi
    if [[ -f $work/egress.jsonl ]]; then cat "$work/egress.jsonl"; fi
    rm -r -- "$work"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# The image entrypoint reads a password from a protected file as the postgres user.
mkdir -m 0700 "$work/config"
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$work/postgres-password"

# Start PostgreSQL only after the recorder is in place. The fixture uses a Unix
# socket with the image's local authentication and publishes no host port.
container=${work##*/}
timeout 45s docker run -d --name "$container" --pull never --network none --dns 192.0.2.53 \
    --label ownsolid.test=restored-schema-upgrade \
    --cpus 1 --memory 512m --pids-limit 128 \
    --mount "type=bind,source=$inputs,target=/inputs,readonly" \
    --mount "type=bind,source=$work/config,target=/fixture,readonly" \
    --mount "type=bind,source=$work/postgres-password,target=/run/bootstrap-password,readonly" \
    --tmpfs /var/lib/postgresql:rw,size=256m \
    --env POSTGRES_DB=ownsolid --env POSTGRES_USER=ownsolid_migrator \
    --env POSTGRES_PASSWORD_FILE=/run/fixture-password \
    --entrypoint bash "$image" -ec '
        while [[ ! -f /fixture/start ]]; do sleep 0.1; done
        install -o postgres -m 0400 /run/bootstrap-password /run/fixture-password
        exec docker-entrypoint.sh postgres -c "listen_addresses="
    ' >/dev/null
pid=$(docker inspect --format '{{.State.Pid}}' "$container")
in_namespace=(nsenter --target "$pid" --net)
[[ $("${in_namespace[@]}" ip -o link show | wc -l) -eq 1 ]]
"${in_namespace[@]}" "$inputs/egress-recorder" -interface egress0 -log "$work/egress.jsonl" &
recorder_pid=$!
for _ in {1..50}; do
    if "${in_namespace[@]}" ip link show egress0 >/dev/null 2>&1; then break; fi
    sleep 0.1
done
"${in_namespace[@]}" ip link set egress0 up
"${in_namespace[@]}" ip addr add 198.18.0.1/32 dev egress0
"${in_namespace[@]}" ip -6 addr add 2001:db8:e9e5::1/128 dev egress0 nodad
"${in_namespace[@]}" ip route add default dev egress0
"${in_namespace[@]}" ip -6 route add default dev egress0
"${in_namespace[@]}" ip route show default
"${in_namespace[@]}" ip -6 route show default
grep -qx 'nameserver 192.0.2.53' < <(docker exec "$container" cat /etc/resolv.conf)

"${in_namespace[@]}" python3 - <<'PY'
import socket
import struct

for family, address in ((socket.AF_INET, "192.0.2.10"), (socket.AF_INET6, "2001:db8::10")):
    with socket.socket(family) as client:
        client.settimeout(0.5)
        try:
            client.connect((address, 9))
        except OSError:
            continue
        raise SystemExit("Outbound probe unexpectedly connected")
with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
    query = struct.pack("!6H", 1, 0x0100, 1, 0, 0, 0) + b"\x05probe\x07invalid\x00\x00\x01\x00\x01"
    client.sendto(query, ("192.0.2.53", 53))
PY
python3 - "$work/egress.jsonl" <<'PY'
import json
import sys
import time
from pathlib import Path

log = Path(sys.argv[1])
expected = {("tcp", "192.0.2.10:9"), ("tcp", "[2001:db8::10]:9"), ("udp", "192.0.2.53:53")}
for _ in range(50):
    rows = [json.loads(line) for line in log.read_text().splitlines()]
    if len(rows) >= len(expected):
        break
    time.sleep(0.1)
if len(rows) != len(expected) or {(r["protocol"].lower(), r["destination"]) for r in rows} != expected:
    sys.exit(f"Unexpected egress probe set: {rows}")
if not any("probe.invalid" in r.get("dns_question", "") for r in rows):
    sys.exit("DNS question missing from recorder")
print("Recorder blocked IPv4, IPv6 and DNS probes")
PY
probe_hash=$(sha256sum "$work/egress.jsonl")
touch "$work/config/start"
for _ in {1..100}; do
    if [[ $(docker exec "$container" cat /proc/1/comm) == postgres ]] &&
       docker exec "$container" pg_isready -q -U ownsolid_migrator -d ownsolid; then break; fi
    sleep 0.1
done
docker exec "$container" psql -XAt -v ON_ERROR_STOP=1 -U ownsolid_migrator -d ownsolid -c 'SELECT version()'

status=0
OWNSOLID_SCHEMA_FIXTURE_CONTAINER="$container" \
    OWNSOLID_SCHEMA_FIXTURE_DIR="$work/config" \
    OWNSOLID_SCHEMA_FIXTURE_SQL56="$inputs/000056.up.sql" \
    OWNSOLID_SCHEMA_FIXTURE_EXPECT="$expected" \
    "$inputs/install.test" -test.v -test.run '^TestRestoredSchemaUpgrade$' -test.timeout 2m || status=$?
timeout 20s docker stop --timeout 10 "$container" >/dev/null
kill -0 "$recorder_pid"
if [[ $(sha256sum "$work/egress.jsonl") != "$probe_hash" ]]; then
    echo "PostgreSQL or control attempted outbound traffic" >&2
    exit 1
fi
echo "Zero PostgreSQL or control egress after the three probes"
exit "$status"
