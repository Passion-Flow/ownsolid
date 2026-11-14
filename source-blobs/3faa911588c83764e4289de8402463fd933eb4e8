"""Verify capture retention with native Linux TUN probes, without product services."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid

import acceptance_guard as guard

INSIDE = r'''
set -eu
umask 077
recorder=$1
probe=$2
evidence=$3
run_id=$4
mode=$5
capture=
cleanup(){
 if [ -n "$capture" ]; then kill "$capture" 2>/dev/null || true; wait "$capture" 2>/dev/null || true; fi
}
trap cleanup EXIT
ip link set lo up
test "$(ip -o link show | wc -l)" = 1
test -z "$(ip route show default)"
test -z "$(ip -6 route show default)"
"$recorder" -interface egress0 -log "$evidence/egress-live.jsonl" &
capture=$!
for n in $(seq 1 100); do
 if ip link show egress0 >/dev/null 2>&1; then break; fi
 sleep .05
done
ip link set egress0 up
ip addr add 198.18.0.1/32 dev egress0
ip -6 addr add 2001:db8:e9e5::1/128 dev egress0 nodad
ip route add default dev egress0
ip -6 route add default dev egress0
"$probe"
# The second batch exercises the IPv4-only probe shape. This is not a Docker workload test.
ip -6 route del default dev egress0
"$probe"
for n in $(seq 1 100); do
 if [ "$(wc -l < "$evidence/egress-live.jsonl")" = 5 ]; then break; fi
 sleep .05
done
test "$(wc -l < "$evidence/egress-live.jsonl")" = 5
cp "$evidence/egress-live.jsonl" "$evidence/egress-baseline.jsonl"
chmod 600 "$evidence/egress-live.jsonl" "$evidence/egress-baseline.jsonl"
printf ready > "$evidence/native-ready"
if [ "$mode" = deadline ]; then
 while :; do sleep 1; done
fi
kill "$capture"
wait "$capture"
capture=
bytes=$(wc -c < "$evidence/egress-live.jsonl")
digest=$(sha256sum "$evidence/egress-live.jsonl" | cut -d ' ' -f 1)
printf '{"run_id":"%s","recorder_exit":0,"workloads_stopped":true,"bytes":%s,"sha256":"%s"}\n' "$run_id" "$bytes" "$digest" > "$evidence/capture-ended.json"
'''


def run(recorder, probe, output):
    """Use two private namespaces and retain both complete and forced-exit evidence."""
    guard.require(sys.platform == 'linux' and os.uname().machine == 'x86_64' and os.geteuid() == 0,
                  'Run the native recorder self-test as root on Linux amd64')
    guard.regular(recorder)
    guard.regular(probe)
    output.mkdir(mode=0o700, exist_ok=False)
    results = {}
    for mode in ['normal', 'deadline']:
        evidence = output / mode
        evidence.mkdir(mode=0o700)
        run_id = str(uuid.uuid4())
        with (evidence / 'process.log').open('w') as log:
            process = subprocess.Popen(['unshare', '--mount', '--net', '--pid', '--fork',
                '--kill-child=KILL', '--mount-proc', '/bin/sh', '-c', INSIDE, 'capture-selftest',
                str(recorder), str(probe), str(evidence), run_id, mode], stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 20
                while not (evidence / 'native-ready').exists():
                    guard.require(process.poll() is None and time.monotonic() < deadline,
                                  'Native capture probe did not complete; inspect its process log')
                    time.sleep(.05)
                if mode == 'deadline':
                    process.kill()
                code = process.wait(timeout=10)
                guard.require(code == 0 if mode == 'normal' else code != 0, 'Unexpected native producer exit')
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait(timeout=10)
        result = guard.capture_result(evidence, run_id)
        expected = 'capture-passed' if mode == 'normal' else 'capture-incomplete'
        guard.require(result['status'] == expected, 'Native retention result differs: ' + json.dumps(result))
        guard.require(len((evidence / 'egress-live.jsonl').read_bytes().splitlines()) == 5,
                      'The host evidence did not retain all native probe records')
        results[mode] = {'producer_exit': code, 'capture': result}
    guard.write_json(output / 'result.json', {
        'scope': 'Native TUN recorder lifecycle only; no product install, Docker workload or history acceptance',
        'recorder_sha256': guard.sha256(recorder), 'probe_sha256': guard.sha256(probe),
        'results': results})
    print(json.dumps(results, sort_keys=True))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--recorder', type=Path, required=True)
    parser.add_argument('--probe', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    options = parser.parse_args()
    run(options.recorder, options.probe, options.output)
