#!/usr/bin/env bash
# Run the signed Registry installer and a cold Docker client in two isolated Linux namespaces.
set -euo pipefail
umask 077

site_ip=10.77.108.1
node_ip=10.77.108.2
authority=$site_ip:5443
self=$(realpath "$0")
role=${OWNSOLID_REGISTRY_TEST_ROLE:-}
if [[ -z $role ]]; then
    if [[ $# != 6 || $EUID != 0 || $(uname -sm) != 'Linux x86_64' ]]; then
        echo "usage (Linux amd64 root): $0 empty-work-directory installer egress-recorder signed-release legacy-release public-trust" >&2
        exit 2
    fi
    work=$(realpath "$1")
    [[ -d $work && -z $(ls -A -- "$work") && $(stat -c '%u:%a' "$work") == 0:700 ]]
    df -h "$work"
    free -m
    ss -lntp
    shift
    inputs=()
    for input in "$@"; do inputs+=("$(realpath "$input")"); done
    export OWNSOLID_REGISTRY_TEST_ROLE=site
    exec unshare --mount --net --pid --fork --mount-proc --propagation private bash "$self" "$work" "${inputs[@]}"
fi

work=$1
installer=$2
recorder=$3
signed=$4
legacy=$5
trust=$6
private=$work/$role
mkdir -p "$private"
export DOCKER_HOST="unix://$private/docker.sock" DOCKER_CONFIG="$private/docker-client"
mkdir "$DOCKER_CONFIG"
daemon=
ctd=
recording=
node=
mounted=
cleanup() {
    code=$?
    if ((code)); then
        tail -n 30 "$private/dockerd.log" "$private/containerd.log" "$work/node.log" 2>/dev/null || true
    fi
    for pid in "$node" "$daemon" "$ctd" "$recording"; do
        if [[ -n $pid ]]; then kill -TERM "$pid" 2>/dev/null || true; fi
    done
    for pid in "$node" "$daemon" "$ctd" "$recording"; do
        if [[ -n $pid ]]; then wait "$pid" 2>/dev/null || true; fi
    done
    if [[ -n $mounted ]]; then umount "$mounted"; fi
}
trap cleanup EXIT
trap 'exit 143' TERM

await_path() {
    local path=$1
    for _ in {1..180}; do
        if [[ -e $path ]]; then return; fi
        sleep 1
    done
    echo "Timed out waiting for $path" >&2
    return 1
}

start_network() {
    ip link set lo up
    "$recorder" -interface egress0 -log "$private/egress.jsonl" &
    recording=$!
    for _ in {1..50}; do
        if ip link show egress0 >/dev/null 2>&1; then break; fi
        sleep 0.1
    done
    ip link set egress0 up
    ip addr add 198.18.0.1/32 dev egress0
    ip -6 addr add 2001:db8:e9e5::1/128 dev egress0 nodad
    ip route add default dev egress0
    ip -6 route add default dev egress0
    printf 'nameserver 192.0.2.53\noptions timeout:1 attempts:1\n' >"$private/resolv.conf"
    mount --bind "$private/resolv.conf" /etc/resolv.conf
    echo "$role namespace routes"
    ip -brief link
    ip route
    ip -6 route
    python3 - <<'PY'
import socket
import struct

for family, address in ((socket.AF_INET, "192.0.2.10"), (socket.AF_INET6, "2001:db8::10")):
    with socket.socket(family) as sock:
        sock.settimeout(2)
        try:
            sock.connect((address, 9))
        except OSError:
            continue
        raise SystemExit("Outbound probe connected")
with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
    query = struct.pack("!6H", 1, 0x0100, 1, 0, 0, 0) + b"\x05probe\x07invalid\x00\x00\x01\x00\x01"
    sock.sendto(query, ("192.0.2.53", 53))
PY
}

assert_egress() {
    kill -0 "$recording"
    python3 - "$role" "$private/egress.jsonl" <<'PY'
import json
import sys
from pathlib import Path

rows = [json.loads(line) for line in Path(sys.argv[2]).read_text().splitlines()]
expected = {("tcp", "192.0.2.10:9"), ("tcp", "[2001:db8::10]:9"), ("udp", "192.0.2.53:53")}
if len(rows) != len(expected) or {(r["protocol"].lower(), r["destination"]) for r in rows} != expected:
    sys.exit(f"Unexpected egress on {sys.argv[1]}: {rows}")
if not any("probe.invalid" in r.get("dns_question", "") for r in rows):
    sys.exit("DNS question missing from recorder")
print(f"{sys.argv[1]}: IPv4, IPv6 and DNS probes blocked, zero unexpected outbound attempts")
PY
}

start_docker() {
    printf '%s\n' 'version = 3' 'disabled_plugins = ["io.containerd.cri.v1.runtime","io.containerd.cri.v1.images","io.containerd.grpc.v1.cri"]' >"$private/containerd.toml"
    (umask 022; exec containerd --config "$private/containerd.toml" --address "$private/containerd.sock" \
        --root "$private/docker-data/containerd/daemon" --state "$private/containerd-state") >"$private/containerd.log" 2>&1 &
    ctd=$!
    await_path "$private/containerd.sock"
    printf '{"features":{"containerd-snapshotter":true}}\n' >"$private/docker.json"
    (umask 022; exec dockerd --config-file "$private/docker.json" --host "$DOCKER_HOST" --data-root "$private/docker-data" \
        --exec-root "$private/docker-run" --pidfile "$private/docker.pid" --exec-opt native.cgroupdriver=cgroupfs \
        --containerd "$private/containerd.sock" --containerd-namespace lane-del-registry --containerd-plugins-namespace lane-del-registry-plugins \
        --bridge none --iptables=false --ip6tables=false --ip-forward=false) >"$private/dockerd.log" 2>&1 &
    daemon=$!
    for _ in {1..90}; do
        if timeout 4 docker info >/dev/null 2>&1; then break; fi
        kill -0 "$daemon"
        sleep 1
    done
    docker info --format "$role Docker {{.ServerVersion}} {{json .DriverStatus}}"
    [[ -z $(docker image ls -q) ]]
}

if [[ $role == node ]]; then
    touch "$private/waiting"
    await_path "$private/link-ready"
    ip link set worker0 up
    ip addr add "$node_ip/30" dev worker0
    start_network
    mount --bind "$work/docker-etc" /etc/docker
    start_docker
    await_path "$work/published"
    pull=$(jq -er '.images[] | select(contains("/control-image@"))' "$work/registry.json")
    url="https://$authority/v2/"
    curl=(curl --noproxy '*' --max-time 10 --silent --show-error --output /dev/null)
    if "${curl[@]}" "$url" 2>"$private/untrusted.log"; then
        echo "Registry accepted without the site CA" >&2
        exit 1
    fi
    grep -qi 'certificate' "$private/untrusted.log"
    trusted=("${curl[@]}" --cacert "$work/ca.crt" --write-out '%{http_code}')
    [[ $("${trusted[@]}" "$url") == 401 ]]
    if docker pull "$pull" >"$private/unauthenticated.log" 2>&1; then
        echo "Registry allowed a pull without authentication" >&2
        exit 1
    fi
    grep -qiE 'unauthorized|authentication required|no basic auth credentials|401' "$private/unauthenticated.log"
    jq -er .password "$work/credential.json" | docker login --username registry-test --password-stdin "$authority"
    while IFS= read -r reference; do
        docker pull "$reference"
        expected=${reference##*@}
        [[ $(docker image inspect --format '{{.Id}} {{.Os}} {{.Architecture}}' "$reference") == "$expected linux amd64" ]]
    done < <(jq -er '.images[]' "$work/registry.json")
    unknown="$authority/ownsolid/release/control-image@sha256:$(printf '%064d' 0)"
    if docker pull "$unknown" >"$private/missing.log" 2>&1; then
        echo "Registry allowed a pull of an unknown digest" >&2
        exit 1
    fi
    grep -qiE 'not found|manifest unknown' "$private/missing.log"
    authenticated=("${trusted[@]}" --netrc-file "$work/netrc")
    [[ $("${authenticated[@]}" "$url") == 200 ]]
    code=$("${authenticated[@]}" -X POST "${url}ownsolid/release/control-image/blobs/uploads/")
    [[ $code == 404 || $code == 405 ]]
    echo "Cold node verified all signed Linux amd64 digests; rejected untrusted TLS, anonymous pull, unknown digest and authenticated upload (HTTP $code)"
    assert_egress
    exit
fi

mkdir -p "$work/docker-etc/certs.d/$authority" "$work/media"
openssl req -new -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Registry acceptance test CA' \
    -addext 'basicConstraints=critical,CA:TRUE' -keyout "$work/ca.key" -out "$work/ca.crt" 2>/dev/null
openssl req -new -newkey rsa:2048 -nodes -subj "/CN=$site_ip" -keyout "$work/site.key" -out "$work/site.csr" 2>/dev/null
printf 'subjectAltName=IP:%s\nextendedKeyUsage=serverAuth\n' "$site_ip" >"$work/site.ext"
openssl x509 -req -in "$work/site.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" -CAcreateserial \
    -days 1 -extfile "$work/site.ext" -out "$work/site.crt" 2>/dev/null
chmod 600 "$work/ca.crt" "$work/site.crt"
cp "$work/ca.crt" "$work/docker-etc/certs.d/$authority/ca.crt"
python3 - "$work" "$site_ip" <<'PY'
import json
import secrets
import sys
from pathlib import Path

root = Path(sys.argv[1])
name = "registry-test"
password = secrets.token_urlsafe(32)
(root / "credential.json").write_text(json.dumps({"username": name, "password": password}))
(root / "netrc").write_text(f"machine {sys.argv[2]} login {name} password {password}\n")
PY
truncate -s 4G "$work/staging.ext4"
mkfs.ext4 -q -F "$work/staging.ext4"
mount -o loop "$work/staging.ext4" "$work/media"
mounted=$work/media
cp "$signed" "$work/media/signed.tar.gz"
cp "$legacy" "$work/media/legacy.tar.gz"
head -c 1048576 "$signed" >"$work/media/truncated.tar.gz"
cp "$trust" "$work/public-trust.json"
printf '{"mount_root":"%s","trust_file":"%s","minimum_free_bytes":1048576}\n' "$work/media" "$work/public-trust.json" >"$work/verification.json"
cat >"$work/site.yaml" <<YAML
root: $work/registry
name: lane-del-registry
listen: $authority
uid: 18080
gid: 18080
request_seconds: 120
tls_ca_file: $work/ca.crt
tls_certificate_file: $work/site.crt
tls_key_file: $work/site.key
credential_file: $work/credential.json
YAML
OWNSOLID_REGISTRY_TEST_ROLE=node unshare --mount --net --propagation private bash "$self" "$@" >"$work/node.log" 2>&1 &
node=$!
await_path "$work/node/waiting"
ip link add site0 type veth peer name worker0
ip link set worker0 netns "$node"
ip link set site0 up
ip addr add "$site_ip/30" dev site0
touch "$work/node/link-ready"
start_network
start_docker

apply=("$installer" registry-bundle --verification "$work/verification.json" --config "$work/site.yaml")
for entry in 'legacy INSTALL_REGISTRY_BUNDLE_REQUIRED' 'truncated INSTALL_BUNDLE_VERIFICATION_FAILED'; do
    read -r input error <<<"$entry"
    if "${apply[@]}" --bundle "$work/media/$input.tar.gz" >"$private/$input.log" 2>&1; then
        echo "Installer accepted $input media" >&2
        exit 1
    fi
    grep -F "$error" "$private/$input.log"
    [[ ! -e $work/registry && -z $(docker container ls -aq) && -z $(docker image ls -q) ]]
done
"${apply[@]}" --bundle "$work/media/signed.tar.gz" | tee "$work/registry.json"
jq -e '.images | length == 4 and all(test("@sha256:[0-9a-f]{64}$"))' "$work/registry.json" >/dev/null
id=$(docker inspect --format '{{.Id}}' lane-del-registry)
[[ $(docker inspect --format '{{.HostConfig.ReadonlyRootfs}} {{range .Mounts}}{{if eq .Destination "/var/lib/registry"}}{{.RW}}{{end}}{{end}}' "$id") == 'true false' ]]
[[ $(docker container ls -aq) == "${id:0:12}" ]]
"${apply[@]}" --bundle "$work/media/signed.tar.gz" >"$work/repeated.json"
cmp "$work/registry.json" "$work/repeated.json"
[[ $(docker inspect --format '{{.Id}}' lane-del-registry) == "$id" ]]
echo "Repeat verified the same container and digest references"
docker container stop --time 10 "$id" >/dev/null
"${apply[@]}" --bundle "$work/media/signed.tar.gz" >"$work/restarted.json"
cmp "$work/registry.json" "$work/restarted.json"
[[ $(docker inspect --format '{{.Id}} {{.State.Running}}' lane-del-registry) == "$id true" ]]
echo "Retry restarted the stopped reader and verified the persisted content"
touch "$work/published"
wait "$node"
node=
cat "$work/node.log"
docker container stop --time 10 "$id" >/dev/null
docker container rm --volumes "$id" >/dev/null
assert_egress
echo "Stopped and removed the test Registry"
