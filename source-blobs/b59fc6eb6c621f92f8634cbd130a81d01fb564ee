#!/bin/bash
set -euo pipefail
umask 077
cd "$1"
network=$2 worker_id=$3 ip=$4
server="$network-server"
worker="$network-worker"
authority="$ip:5443"
repo=ownsolid/release/metrics-image
manifest=$(jq -er '.assets[] | select(.id=="metrics-image" and .architecture=="amd64") | .image_id' capture/registry-signed-manifest.json)
image_id=$(jq -er '.assets[] | select(.id=="metrics-image" and .architecture=="amd64") | .image_config_digest' capture/registry-signed-manifest.json)
[[ $manifest =~ ^sha256:[0-9a-f]{64}$ && $image_id =~ ^sha256:[0-9a-f]{64}$ ]]
ref=$(jq -er --arg ref "$authority/$repo@$manifest" '.images[] | select(.==$ref)' capture/registry-install.json)
absent=$(printf '%064d' 0)
site=site-inputs/worker
conf=/etc/rancher/rke2
native=registries.yaml
dropin=config.yaml.d/99-site-offline.yaml
w() { docker exec "$worker" "$@"; }
k() { docker exec "$server" /var/lib/rancher/rke2/bin/kubectl --kubeconfig /etc/rancher/rke2/rke2.yaml --request-timeout=25s "$@"; }
cri() { timeout 45 docker exec "$worker" /var/lib/rancher/rke2/bin/crictl --runtime-endpoint unix:///run/k3s/containerd/containerd.sock "$@"; }
copy_site() {
    local source=$1 target=$2
    docker cp "$source" "$worker:$target.arc104-tmp"
    w install -m 0600 "$target.arc104-tmp" "$target"
    w rm "$target.arc104-tmp"
}
proof_config() {
    local point=$1 path expected actual mode
    for path in "$native" "$dropin" registry-ca.pem; do
        expected=$(sha256sum "$site/$path" | cut -d ' ' -f 1)
        actual=$(w sha256sum "$conf/$path" | cut -d ' ' -f 1)
        mode=$(w stat -c '%u:%a' "$conf/$path")
        [[ $actual == "$expected" && $mode == 0:600 ]]
        printf '%s %s %s %s\n' "$point" "$path" "$actual" "$mode" >> capture/site-policy-proof.txt
    done
    w test ! -e "$conf/ownsolid-registries.yaml"
}
assert_cold() {
    local label=$1
    cri images -o json > "capture/$label-worker-images.json"
    jq -e --arg id "$image_id" --arg ref "$ref" 'all(.images[]; .id != $id and ([(.repoDigests // [])[] | select(. == $ref)] | length)==0)' "capture/$label-worker-images.json" >/dev/null
}
reload() {
    local before after
    before=$(w pgrep -xo containerd)
    timeout 120 docker exec "$worker" systemctl restart rke2-agent
    for _ in {1..65}; do
        test ! -e exchange/resource-refused
        after=$(w pgrep -xo containerd || true)
        if [[ -n $after && $after != "$before" ]] && cri info >/dev/null 2>&1; then return; fi
        sleep 2
    done
    echo "RKE2 worker did not start a new CRI instance" >&2
    return 1
}
refusal() {
    local label=$1 image=$2 reason=$3 status=0
    test ! -e exchange/resource-refused
    printf "%s\n" "$label" > capture/current-stage
    cri pull "$image" >"capture/$label.log" 2>&1 || status=$?
    printf '%s\n' "$status" > "capture/$label.exit"
    if [[ $status == 0 || $status == 124 ]] || ! grep -Eqi "$reason" "capture/$label.log" ||
       ! grep -Fq "$authority" "capture/$label.log"; then
        echo "$label did not return the expected in-site refusal (status=$status)" >&2
        return 1
    fi
    assert_cold "$label"
    printf '%s refused by the configured site path (status=%s)\n' "$label" "$status"
}
cleanup_site() {
    copy_site "$site/$native" "$conf/$native"
    copy_site "$site/registry-ca.pem" "$conf/registry-ca.pem"
}
trap 'cleanup_site >/dev/null 2>&1 || true' EXIT
proof_config after-agent-join
assert_cold before-pulls
copy_site site-inputs/wrong-ca.pem "$conf/registry-ca.pem"
reload
refusal wrong-ca "$ref" 'x509:|certificate signed by unknown authority'
copy_site "$site/registry-ca.pem" "$conf/registry-ca.pem"
copy_site site-inputs/anonymous.yaml "$conf/$native"
reload
refusal anonymous "$ref" '401|unauthorized|authorization failed|no basic auth credentials'
copy_site "$site/$native" "$conf/$native"
reload
refusal unknown-digest "$authority/$repo@sha256:$absent" '404|not found|manifest unknown'
refusal no-public-fallback "docker.io/$repo@sha256:$absent" '404|not found|manifest unknown'
proof_config restored-site-policy
assert_cold before-pod
printf "%s\n" pod-ready > capture/current-stage
python3 - "$ref" "$worker_id" <<'PY' | docker exec -i "$server" /var/lib/rancher/rke2/bin/kubectl --kubeconfig /etc/rancher/rke2/rke2.yaml --request-timeout=25s create -f -
import json,sys
ref,node=sys.argv[1:]
print(json.dumps({"apiVersion":"v1","kind":"Pod","metadata":{"name":"arc104-signed-registry-workload"},
    "spec":{"nodeSelector":{"kubernetes.io/hostname":node},"restartPolicy":"Never",
    "containers":[{"name":"workload","image":ref,"imagePullPolicy":"Always",
                   "command":["/victoria-metrics-prod"],
                   "args":["-httpListenAddr=:8428","-storageDataPath=/tmp/arc104-metrics"],
                   "readinessProbe":{"httpGet":{"path":"/-/ready","port":8428},"periodSeconds":2}}]}}))
PY
pod=arc104-signed-registry-workload
if ! k wait --for=condition=Ready --timeout=180s "pod/$pod"; then
    k get "pod/$pod" -o json > capture/workload-pod.json || true
    k describe "pod/$pod" > capture/workload-events.txt || true
    exit 1
fi
k get "pod/$pod" -o json > capture/workload-pod.json
k logs "pod/$pod" > capture/workload.log
grep -qi 'victoria-metrics' capture/workload.log
cri inspecti "$ref" > capture/workload-cri.json
jq -e --arg id "$image_id" --arg ref "$ref" '.status.id == $id and (.status.repoDigests | index($ref) != null)' capture/workload-cri.json >/dev/null
jq -e --arg node "$worker_id" --arg ref "$ref" --arg digest "$manifest" --arg id "$image_id" '
    .spec.nodeName == $node and (.status.conditions | any(.type=="PodScheduled" and .status=="True")) and
    .spec.containers[0].image == $ref and .status.phase=="Running" and
    (.status.conditions | any(.type=="Ready" and .status=="True")) and
    .status.containerStatuses[0].ready and .status.containerStatuses[0].state.running != null and
    (.status.containerStatuses[0].imageID | endswith($digest) or endswith($id))' capture/workload-pod.json >/dev/null
k wait --for=condition=Ready --timeout=45s "node/$worker_id"
proof_config after-pod
trap - EXIT
printf 'Worker pulled and ran the previously absent signed digest %s\n' "$manifest"
