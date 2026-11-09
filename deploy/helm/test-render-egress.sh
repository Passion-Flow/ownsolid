#!/usr/bin/env bash
# Render and check the real control file boundary in isolated Linux namespaces.
set -euo pipefail
umask 077

if [[ ${OWNSOLID_HELM_TEST_ISOLATED:-} != 1 ]]; then
    if [[ $# != 5 || $EUID != 0 || $(uname -sm) != 'Linux x86_64' ]]; then
        echo "usage (Linux amd64 root): $0 empty-work-directory helm control installer-test egress-recorder" >&2
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
    export OWNSOLID_HELM_TEST_ISOLATED=1
    exec unshare --mount --net --pid --fork --mount-proc --propagation private \
        bash "$(realpath "$0")" "$work" "${inputs[@]}"
fi

work=$1
helm=$2
control=$3
installer_test=$4
recorder=$5
chart_root=$(dirname "$(realpath "$0")")
export KUBECONFIG=/dev/null HELM_NO_PLUGINS=1
export HELM_CACHE_HOME="$work/helm-cache" HELM_CONFIG_HOME="$work/helm-config" HELM_DATA_HOME="$work/helm-data"
export OWNSOLID_TEST_HELM="$helm" OWNSOLID_TEST_HELM_CHART="$chart_root/ownsolid"

ip link set lo up
"$recorder" -interface egress0 -log "$work/egress.jsonl" &
recorder_pid=$!
trap 'kill "$recorder_pid" 2>/dev/null || true; wait "$recorder_pid" 2>/dev/null || true' EXIT
for _ in {1..50}; do
    if ip link show egress0 >/dev/null 2>&1; then break; fi
    sleep 0.1
done
ip link set egress0 up
ip addr add 198.18.0.1/32 dev egress0
ip -6 addr add 2001:db8:e9e5::1/128 dev egress0 nodad
ip route add default dev egress0
ip -6 route add default dev egress0
printf 'nameserver 192.0.2.53\noptions timeout:1 attempts:1\n' >"$work/resolv.conf"
mount --bind "$work/resolv.conf" /etc/resolv.conf
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
    header = struct.pack("!6H", 1, 0x0100, 1, 0, 0, 0)
    sock.sendto(header + b"\x05probe\x07invalid\x00\x00\x01\x00\x01", ("192.0.2.53", 53))
PY

"$helm" version --short
"$installer_test" -test.v -test.run '^TestHelm' -test.timeout 60s
"$helm" package "$chart_root/ownsolid" --destination "$work"
package="$work/ownsolid-0.1.0.tgz"
sha256sum "$package"
"$helm" template site "$package" --kube-version 1.36.6 --namespace site-del107 \
    --values "$chart_root/testdata/site.yaml" --show-only templates/configmap.yaml >"$work/configmap.yaml"

# Use the exact mounted paths in the rendered control configuration.
mount -t tmpfs -o mode=0755 tmpfs /run
install -m 0755 "$control" /run/control
tar -xOzf "$package" ownsolid/files/snapshot.sh >/run/snapshot
chmod 0755 /run/snapshot
uid=10001
gid=10001
install -d -m 0750 -g "$gid" /run/projection /run/projection/revision
install -d -m 2770 -g "$gid" /run/ownsolid
chmod 2770 /run/ownsolid
install -d -m 0700 -o "$uid" -g "$gid" /run/models
ln -s revision /run/projection/..data
projection=/run/projection/revision
awk '/^  control.yaml: \|$/ { copy=1; next } copy && /^    / { print substr($0, 5) }' \
    "$work/configmap.yaml" >"$projection/control.yaml"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=admin.site.test' \
    -addext 'subjectAltName=DNS:admin.site.test,DNS:portal.site.test' \
    -keyout "$projection/tls.key" -out "$projection/tls.crt" 2>"$work/openssl.log"
cp "$projection/tls.crt" "$projection/browser-ca.pem"
cp "$projection/tls.crt" "$projection/smtp-ca.pem"
cp "$projection/tls.crt" "$projection/postgres-ca.pem"
# The transport checker does not connect to SQL or open the runtime credential files.
for key in runtime-dsn auth-hmac mail-keyring.json license-trust.json; do
    printf 'Transport-only fixture\n' >"$projection/$key"
done
chgrp "$gid" "$projection/"*
chmod 0440 "$projection/"*
as_runtime=(setpriv --reuid "$uid" --regid "$gid" --clear-groups)
snapshot=("${as_runtime[@]}" /bin/sh /run/snapshot /run/projection /run/ownsolid /run/models
    control.yaml tls.crt tls.key runtime-dsn auth-hmac mail-keyring.json browser-ca.pem postgres-ca.pem smtp-ca.pem license-trust.json)
"${snapshot[@]}"
[[ $(stat -c '%u:%g:%a' /run/ownsolid/private) == "$uid:$gid:700" ]]
for path in /run/ownsolid/private/*; do
    [[ ! -L $path && $(stat -c '%u:%g:%a' "$path") == "$uid:$gid:600" ]]
done
expected='{"format_version":1,"transport_mode":"direct_https","listen_address":"0.0.0.0:8443","admin_origin":"https://admin.site.test","portal_origin":"https://portal.site.test","tls_certificate_file":"/run/ownsolid/private/tls.crt","tls_private_key_file":"/run/ownsolid/private/tls.key","trusted_proxy_cidrs":[]}'
verify=("${as_runtime[@]}" /run/control verify-serving-transport
    --config /run/ownsolid/private/control.yaml --expected-server-json "$expected")
"${verify[@]}" | grep -Fx 'CONTROL_SERVING_TRANSPORT_OK'

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=admin.site.test' \
    -addext 'subjectAltName=DNS:admin.site.test' -keyout "$projection/tls.key" \
    -out "$projection/tls.crt" 2>>"$work/openssl.log"
chgrp "$gid" "$projection/tls.crt" "$projection/tls.key"
chmod 0440 "$projection/tls.crt" "$projection/tls.key"
"${snapshot[@]}"
if "${verify[@]}" >"$work/wrong-certificate.log" 2>&1; then
    echo "Control accepted a certificate without the Portal SAN" >&2
    exit 1
fi
grep -Fx 'SERVING_TRANSPORT_CONFIGURATION_INVALID' "$work/wrong-certificate.log"
mv "$projection/runtime-dsn" "$projection/withheld-dsn"
if "${snapshot[@]}" >"$work/missing-key.log" 2>&1; then
    echo "Snapshot accepted a missing runtime DSN projection" >&2
    exit 1
fi
grep -Fx 'HELM_RUNTIME_FILE_REQUIRED: runtime-dsn' "$work/missing-key.log"
chmod 0770 /run/models
if "${snapshot[@]}" >"$work/model-mode.log" 2>&1; then
    echo "Snapshot accepted a group-writable model store" >&2
    exit 1
fi
grep -F 'HELM_MODEL_STORE_INVALID' "$work/model-mode.log"

kill -0 "$recorder_pid"
python3 - "$work/egress.jsonl" <<'PY'
import json
import sys
from pathlib import Path

rows = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
expected = {("tcp", "192.0.2.10:9"), ("tcp", "[2001:db8::10]:9"), ("udp", "192.0.2.53:53")}
if len(rows) != len(expected) or {(r["protocol"].lower(), r["destination"]) for r in rows} != expected:
    sys.exit(f"Unexpected egress: {rows}")
if not any("probe.invalid" in r.get("dns_question", "") for r in rows):
    sys.exit("DNS question missing from recorder")
print("Helm and native Control checks attempted zero external traffic; IPv4, IPv6 and DNS probes blocked")
PY
