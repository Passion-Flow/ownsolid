#!/bin/bash
set -euo pipefail
umask 077
cd "$1"
root=$PWD
net=$2
plane="$net-plane"
dns="$net-dns"
server="$net-server"
worker="$net-worker"
python3 "$root/guard.py" "$root" start | tee capture/host-run-admission.json
if docker ps --format '{{.Names}}' | grep -Ei '(^|[-_])lane[-_]?del|^del[-_]'; then
    echo 'DEL has a running fixture'
    exit 75
fi
test "$(free -m | awk '/^Mem:/ {print $4}')" -ge 2048
mountpoint -q "$root/media-mount"
test -s "$root/media-mount/stage/ownsolid.tar.gz"
install -d -m 0700 "$root/control-inputs" "$root/node-inputs" "$root/exchange" "$root/exchange/server" "$root/exchange/worker" "$root/capture" "$root/docker-data" "$root/site-runtime"
install -m 0700 "$root/managed.test" "$root/ownsolid-install" "$root/egress-recorder" "$root/control-inside.sh" "$root/registry-install" "$root/control-inputs/"
install -m 0700 "$root/observe-containers.sh" "$root/configure-postgres-logs.sh" "$root/control-inputs/"
install -m 0755 "$root/diagnostic-control" "$root/control-inputs/"
install -m 0700 "$root/agent" "$root/egress-recorder" "$root/node-inputs/"
docker network create --internal "$net" >/dev/null
test "$(docker network inspect -f '{{.Internal}}' "$net")" = true
subnet=$(docker network inspect -f '{{(index .IPAM.Config 0).Subnet}}' "$net")
read -r ARC_SITE_DNS ARC_CONTROL_IP ARC_SERVER_IP ARC_WORKER_IP < <(python3 - "$subnet" <<'PY'
from ipaddress import ip_network
import sys
network=ip_network(sys.argv[1])
print(*(network[x] for x in (2,10,11,12)))
PY
)
export ARC_SITE_DNS ARC_CONTROL_IP ARC_SERVER_IP ARC_WORKER_IP
export ARC_FIXTURE_ROOT="$root" ARC_PROJECT="$net"
python3 "$root/site-files.py" "$root/site-inputs" "$ARC_CONTROL_IP"
base=$(docker image ls --no-trunc --format '{{.ID}}' ownsolid-proto/lane-a-node:24.04)
dind=$(docker image ls --no-trunc --format '{{.ID}}' docker:29.1.3-dind)
docker run -d --name "$dns" --network "$net" --ip "$ARC_SITE_DNS" --read-only \
    --mount "type=bind,src=$root/site-dns,dst=/site-dns,readonly" --entrypoint /site-dns "$base" >/dev/null
for role in server worker; do
    ip=$ARC_SERVER_IP
    if test "$role" = worker; then ip=$ARC_WORKER_IP; fi
    ARC_NODE_IP="$ip" bash setup-node.sh "$role"
    docker exec "$net-$role" sh -ceu '
        test ! -e /usr/local/bin/rke2
        test ! -e /etc/rancher/rke2/config.yaml
        test ! -e /var/lib/rancher/rke2/server/manifests/rke2-canal.yaml.skip
        test ! -e /fixture-state/kubernetes.json
    '
    if [[ $role == worker ]]; then
        docker exec "$net-$role" install -d -m 0700 /etc/rancher/rke2
        docker cp "$root/site-inputs/worker/." "$net-$role:/etc/rancher/rke2/"
        docker exec "$net-$role" chmod 0700 /etc/rancher/rke2
        docker exec "$net-$role" chmod 0600 /etc/rancher/rke2/registries.yaml /etc/rancher/rke2/config.yaml.d/99-site-offline.yaml
    fi
    for q in arc104-probe.invalid; do
        docker exec "$net-$role" getent ahostsv4 "$q" || true
        docker logs "$dns" | tail -n 1 | grep -F "\"name\":\"$q.\""
    done
done
first=$(( $(docker logs "$dns" | wc -l) + 1 ))
docker logs "$dns" > capture/site-dns-positive-controls.jsonl
save_captures() {
    docker logs "$dns" > capture/site-dns.raw.jsonl
    tail -n "+$first" capture/site-dns.raw.jsonl > capture/site-dns.jsonl
    for role in server worker; do
        cp "$root/$role-capture/egress.jsonl" "capture/$role-egress.jsonl"
    done
}
trap save_captures EXIT
docker run -d --name "$plane" --privileged --network "$net" --ip "$ARC_CONTROL_IP" \
    --cgroupns private --memory 6g --cpus 3 \
    --env ARC_SITE_DNS --env ARC_CONTROL_IP --env ARC_SERVER_IP --env ARC_WORKER_IP --env "ARC_PROJECT=$net" \
    --mount "type=bind,src=$root/docker-data,dst=/var/lib/docker" \
    --mount "type=bind,src=$root/media-mount,dst=/release" \
    --mount "type=bind,src=$root/site-inputs,dst=/site-inputs,readonly" \
    --mount "type=bind,src=$root/site-runtime,dst=/site-runtime" \
    --mount "type=bind,src=$root/exchange,dst=/exchange" \
    --mount "type=bind,src=$root/capture,dst=/capture" \
    --mount "type=bind,src=$root/control-inputs,dst=/bundle,readonly" \
    --entrypoint /bin/sh "$dind" /bundle/control-inside.sh >/dev/null
docker logs -f "$plane" &
observer=$!
monitor=
trap 'if [[ -n $monitor ]]; then kill "$monitor" 2>/dev/null || true; wait "$monitor" 2>/dev/null || true; fi; kill "$observer" 2>/dev/null || true; save_captures' EXIT
python3 "$root/guard.py" "$root" arm "$plane" "$server" "$worker" | tee capture/live-admission.json
touch exchange/cgroup-admitted
(
    while docker inspect -f '{{.State.Running}}' "$plane" | grep -qx true; do
        if ! python3 "$root/guard.py" "$root" live "$plane" "$server" "$worker" >> capture/host-live.jsonl; then
            touch exchange/resource-refused
            docker stop --time 10 "$plane" >/dev/null
            exit 75
        fi
        sleep 5
    done
) &
monitor=$!
still_running() {
    if test -e exchange/resource-refused; then return 75; fi
    test "$(docker inspect -f '{{.State.Running}}' "$plane")" = true
}
for role in server worker; do
    for i in $(seq 1 240); do
        if test -f "exchange/$role/enroll"; then break; fi
        still_running
        sleep 2
    done
    test -f "exchange/$role/enroll"
    docker exec "$net-$role" /fixture-artifacts/agent enroll --config /fixture-enrollment/agent.json
    docker exec -d "$net-$role" sh -c 'exec /fixture-artifacts/agent run --config /fixture-enrollment/agent.json > /fixture-capture/agent.log 2>&1'
done
for i in $(seq 1 480); do
    if test -s exchange/api-ready.json; then break; fi
    still_running
    sleep 2
done
test -s exchange/api-ready.json
read -r server_id worker_id version ca < <(python3 - <<'PY'
import json
p=json.load(open("exchange/api-ready.json"))
print(p["server"],p["worker"],p["version"],p["ca_sha256"])
PY
)
k() { docker exec "$server" /var/lib/rancher/rke2/bin/kubectl --kubeconfig /etc/rancher/rke2/rke2.yaml --request-timeout=25s "$@"; }
test "$version" = v1.36.6+rke2r1
actual_ca=$(docker exec "$server" sha256sum /var/lib/rancher/rke2/server/tls/server-ca.crt | cut -d' ' -f1)
test "$actual_ca" = "$ca"
for pair in "server:$server_id" "worker:$worker_id"; do
    role=${pair%%:*}
    id=${pair#*:}
    k wait --for=condition=Ready --timeout=45s "node/$id"
    test "$(k get "node/$id" -o jsonpath='{.status.nodeInfo.kubeletVersion}')" = "$version"
    test "$(k get "node/$id" -o jsonpath='{.status.nodeInfo.architecture}')" = amd64
    test "$(docker exec "$net-$role" /usr/local/bin/rke2 --version | head -1 | awk '{print $3}')" = "$version"
    image_dir="$root/$role-data/agent/images"
    (
      cd "$image_dir"
      sed -n '/  rke2-images-core.linux-amd64.tar.zst$/p; /  rke2-images-canal.linux-amd64.tar.zst$/p' "$root/sha256sum-amd64.txt" > "$root/capture/$role-image-checksums.txt"
      test "$(wc -l < "$root/capture/$role-image-checksums.txt")" = 2
      sha256sum -c "$root/capture/$role-image-checksums.txt"
      test "$(find . -maxdepth 1 -type f -name '*.tar.zst' | wc -l)" = 2
    )
done
k get nodes -o wide
k -n kube-system rollout status ds/rke2-canal --timeout=90s
printf '%s\n' worker-registry-pull > capture/current-stage
if bash "$root/pull-proof.sh" "$root" "$net" "$worker_id" "$ARC_CONTROL_IP"; then
    printf '0\n' > capture/worker-proof.exit
else
    rc=$?
    printf '%s\n' "$rc" > capture/worker-proof.exit
    exit "$rc"
fi
still_running
touch exchange/node-proof
plane_exit=$(docker wait "$plane")
printf '%s\n' "$plane_exit" > capture/test-process-exit
test "$plane_exit" = 0
test ! -e exchange/resource-refused
docker stop --time 12 "$server" "$worker" "$dns" >/dev/null
save_captures
python3 "$root/verify-captures.py" capture
