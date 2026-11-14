"""Exercise the real parent cleanup and evidence bind without product services."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import stat
import sys
import time
import uuid

import acceptance_guard as guard
import host_runner
from native_capture_selftest import INSIDE


def run(recorder, probe, image, socket, output, cpus, memory_bytes):
    """Run two small probe containers and reuse Window.close for both exit paths."""
    guard.require(sys.platform == 'linux' and os.uname().machine == 'x86_64' and os.geteuid() == 0,
                  'Run the Docker capture self-test as root on Linux amd64')
    guard.require(re.fullmatch('sha256:[0-9a-f]{64}', image), 'Pin the local image')
    guard.require(cpus > 0 and memory_bytes > 0, 'Set explicit positive probe resource caps')
    guard.require(Path(socket).is_absolute() and stat.S_ISSOCK(Path(socket).stat().st_mode),
                  'Select a local Unix Docker socket')
    output.mkdir(mode=0o700, exist_ok=False)
    bundle = output / 'inputs'
    bundle.mkdir(mode=0o700)
    for source, name in [(recorder, 'recorder'), (probe, 'probe')]:
        guard.regular(source)
        shutil.copyfile(source, bundle / name)
        (bundle / name).chmod(0o500)
    # Alpine's bundled ip lacks -o and nodad. The existing fixture waits for DAD.
    script = INSIDE.replace('ip -o link show | wc -l', 'ls -1 /sys/class/net | wc -l')
    script = script.replace(' dev egress0 nodad', ' dev egress0')
    script = script.replace('ip -6 route add default dev egress0\n', 'ip -6 route add default dev egress0\nsleep 2\n')
    (bundle / 'inside.sh').write_text(script)
    results = {}
    for mode in ['normal', 'deadline']:
        evidence = output / mode
        evidence.mkdir(mode=0o700)
        run_id = str(uuid.uuid4())
        config = {'output_directory': str(evidence), 'container_name': 'lane-del-capture-' + run_id[:8],
                  'run_id': run_id, 'docker_socket': socket, 'operation_seconds': 15, 'cleanup_seconds': 1}
        window = host_runner.Window(bundle, config, {'candidate': 'capture-selftest'})
        window.hard_deadline = time.time() + 60
        window.monotonic_deadline = time.monotonic() + 60
        try:
            guard.require(window.docker('container', 'inspect', window.name, check=False).returncode != 0,
                          'Self-test name is occupied')
            window.created = True
            window.docker('run', '--detach', '--pull', 'never', '--name', window.name,
                '--network', 'none', '--cap-add', 'NET_ADMIN', '--device', '/dev/net/tun',
                '--cpus', str(cpus), '--memory', str(memory_bytes), '--pids-limit', '64',
                '--label', 'io.ownsolid.acceptance.run_id=' + run_id,
                '--volume', str(bundle) + ':/fixture:ro', '--volume', str(evidence) + ':/evidence',
                '--entrypoint', '/bin/sh', image, '/fixture/inside.sh', '/fixture/recorder',
                '/fixture/probe', '/evidence', run_id, mode)
            until = time.monotonic() + 20
            while not (evidence / 'native-ready').exists():
                state = window.inspect()
                guard.require(state is not None and state['State']['Running'] and time.monotonic() < until,
                              'Native container probes did not finish')
                time.sleep(.05)
            if mode == 'normal':
                while True:
                    state = window.inspect()
                    if not state['State']['Running']:
                        window.result = state['State']['ExitCode']
                        break
                    guard.require(time.monotonic() < until, 'Normal capture did not close')
                    time.sleep(.05)
        finally:
            window.close()
        result = guard.read_json(evidence / 'window-result.json')
        expected = 'capture-passed' if mode == 'normal' else 'capture-incomplete'
        guard.require(result['capture']['status'] == expected, 'Parent capture classification differs')
        guard.require(result['passed'] is (mode == 'normal'), 'Parent suite outcome differs')
        guard.require(len((evidence / 'egress-live.jsonl').read_bytes().splitlines()) == 5,
                      'The host bind lost native packet records')
        guard.require(window.inspect() is None, 'The owned probe container remains')
        results[mode] = result
    guard.write_json(output / 'result.json', {
        'scope': 'Real Docker evidence bind and parent close only; no product or history acceptance',
        'image': image, 'recorder_sha256': guard.sha256(recorder), 'probe_sha256': guard.sha256(probe),
        'results': results})
    print(json.dumps(results, sort_keys=True))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--recorder', type=Path, required=True)
    parser.add_argument('--probe', type=Path, required=True)
    parser.add_argument('--image', required=True)
    parser.add_argument('--docker-socket', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--cpus', type=int, required=True)
    parser.add_argument('--memory-bytes', type=int, required=True)
    args = parser.parse_args()
    run(args.recorder, args.probe, args.image, args.docker_socket, args.output, args.cpus, args.memory_bytes)
