#!/usr/bin/env bash
# Exercise an extracted, pinned model with a cold cache and the repository egress recorder.
set -euo pipefail
if [[ $# != 6 || $EUID != 0 || $4 != *@sha256:* ]]; then
    echo "usage (Linux root): $0 model-dir catalog.json egress-recorder image@sha256:digest cpu-limit memory-limit" >&2
    exit 2
fi
model=$(realpath "$1")
catalog=$(realpath "$2")
recorder=$(realpath "$3")
image=$4
work=$(mktemp -d)
container=
recorder_pid=
runtime_pid=
cleanup() {
    if [[ -n $container ]]; then docker rm -f "$container" >/dev/null || true; fi
    if [[ -n $runtime_pid ]]; then wait "$runtime_pid" || true; fi
    if [[ -n $recorder_pid ]]; then
        kill "$recorder_pid" 2>/dev/null || true
        wait "$recorder_pid" || true
    fi
    if [[ -f $work/egress.jsonl ]]; then cat "$work/egress.jsonl"; fi
    if [[ -f $work/vllm.log ]]; then tail -40 "$work/vllm.log"; fi
    rm -f -- "$work/egress.jsonl" "$work/vllm.log"
    rmdir "$work"
}
trap cleanup EXIT

python3 - "$model" "$catalog" <<'PY'
import hashlib
import json
from pathlib import Path
import sys

directory, catalog = map(Path, sys.argv[1:])
manifest = json.loads(catalog.read_text())
expected = {entry["path"]: entry for entry in manifest["files"]}
if {p.name for p in directory.iterdir()} != set(expected):
    sys.exit("Model directory differs from the pinned catalogue")
for name, entry in expected.items():
    p = directory / name
    if p.is_symlink() or not p.is_file() or p.stat().st_size != entry["size_bytes"]:
        sys.exit(f"Model size or file type differs for {name}")
    with p.open("rb") as file:
        if hashlib.file_digest(file, "sha256").hexdigest() != entry["sha256"]:
            sys.exit(f"Model checksum differs for {name}")
print(f"Verified {len(expected)} catalogue files at {manifest['repository']}@{manifest['revision']}")
PY

# The worker resolves its own hostname while it starts its local process group.
container=$(docker run -d --pull never --network none --cap-drop ALL \
    --hostname ownsolid-offline-model --add-host ownsolid-offline-model:127.0.0.1 \
    --cpus "$5" --memory "$6" --shm-size 1g \
    --mount "type=bind,source=$model,target=/model,readonly" \
    --tmpfs /cold:exec,mode=1777 --env HF_HOME=/cold/huggingface --env XDG_CACHE_HOME=/cold \
    --env HF_HUB_OFFLINE=1 --env TRANSFORMERS_OFFLINE=1 --env VLLM_NO_USAGE_STATS=1 \
    --env DO_NOT_TRACK=1 --env VLLM_CPU_KVCACHE_SPACE=1 --env OMP_NUM_THREADS=2 \
    --entrypoint sleep "$image" infinity)
pid=$(docker inspect --format '{{.State.Pid}}' "$container")
in_namespace=(nsenter --target "$pid" --net)
[[ $("${in_namespace[@]}" ip -o link show | wc -l) -eq 1 ]]
"${in_namespace[@]}" "$recorder" -interface egress0 -log "$work/egress.jsonl" &
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
docker exec -i "$container" python3 - <<'PY'
import socket
import struct

for family, address in ((socket.AF_INET, "192.0.2.10"), (socket.AF_INET6, "2001:db8::10")):
    with socket.socket(family) as s:
        s.settimeout(2)
        try:
            s.connect((address, 9))
        except OSError:
            continue
        raise SystemExit("Outbound probe unexpectedly connected")
with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
    query = struct.pack("!6H", 1, 0x0100, 1, 0, 0, 0) + b"\x05probe\x07invalid\x00\x00\x01\x00\x01"
    s.sendto(query, ("192.0.2.53", 53))
PY
python3 - "$work/egress.jsonl" <<'PY'
import json
import sys
from pathlib import Path

rows = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
expected = {("tcp", "192.0.2.10:9"), ("tcp", "[2001:db8::10]:9"), ("udp", "192.0.2.53:53")}
if len(rows) != len(expected) or {(r["protocol"].lower(), r["destination"]) for r in rows} != expected:
    sys.exit(f"Unexpected probe set: {rows}")
if not any("probe.invalid" in r.get("dns_question", "") for r in rows):
    sys.exit("DNS question missing from recorder")
print("Recorder saw and blocked the IPv4, IPv6 and DNS probes")
PY
probe_hash=$(sha256sum "$work/egress.jsonl")

docker exec "$container" vllm serve /model --tokenizer /model --host 127.0.0.1 \
    --served-model-name offline-smoke --dtype bfloat16 --max-model-len 256 \
    --max-num-seqs 1 --enforce-eager >"$work/vllm.log" 2>&1 &
runtime_pid=$!
docker exec -i "$container" python3 - <<'PY'
import json
import time
import urllib.error
import urllib.request

endpoint = "http://127.0.0.1:8000"
for _ in range(150):
    try:
        with urllib.request.urlopen(endpoint + "/health", timeout=1) as response:
            if response.status == 200:
                break
    except urllib.error.URLError:
        time.sleep(2)
else:
    raise SystemExit("vLLM did not become ready")
body = json.dumps({"model": "offline-smoke", "prompt": "The capital of France is", "max_tokens": 8, "temperature": 0}).encode()
request = urllib.request.Request(endpoint + "/v1/completions", body, {"Content-Type": "application/json"})
with urllib.request.urlopen(request, timeout=120) as response:
    result = json.load(response)
if not result["choices"][0]["text"].strip() or result["usage"]["completion_tokens"] < 1:
    raise SystemExit("Model did not produce text")
print(f"Local model generated {result['usage']['completion_tokens']} completion tokens")
PY
docker stop --timeout 20 "$container" >/dev/null
kill -0 "$recorder_pid"
if [[ $(sha256sum "$work/egress.jsonl") != "$probe_hash" ]]; then
    echo "Runtime attempted outbound traffic" >&2
    exit 1
fi
echo "Zero runtime egress after the three positive probes"
