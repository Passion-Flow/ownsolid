"""Test-only admission, capture finalization and single-driver handoffs."""
import argparse
from collections import Counter
import datetime
import fcntl
import hashlib
import ipaddress
import json
import math
import os
from pathlib import Path
import re
import stat
import struct
import tempfile
import time
import uuid

REQUIRED_INPUTS = {
    'control', 'ownsolid-install', 'history-prepare.test', 'installed.test',
    'scope.test', 'password-blocklist.txt', 'egress-recorder', 'probe',
    'compose.yaml', 'fleet-rollup-rules.yaml', 'images.tar', 'source-sha',
    'installed-history', 'client-source.go', 'client-tests.go', 'client-proof.json',
    'inside.sh',
    'runtime.json', 'tls-relay.py',
    'prepare-history.py',
}
CLIENT_TESTS = {
    'TestVerifyMeasuredZeroAndSparseAbsence',
    'TestPublicTLSCookieAuditAndStaleSessionLifecycle',
    'TestNullAuditItemsCannotProveNoAudit', 'TestCreateSessionStateDoesNotOverwrite',
    'TestOpaqueUnixRelayPreservesTLSIdentity',
    'TestNativeCachedMetricsSnapshotIsRefreshed',
    'TestCounterFreshnessRejectsEpochChangeAndTimeout',
}
HOOKS = ('long-stop', 'long-start', 'long-bad-tls', 'long-restore-tls',
         'workspace-disable', 'workspace-restore')
PHASES = ('web', 'sql', 'cli', 'obs')
HISTORY_INPUTS = {'history-prepare.test', 'fleet-rollup-rules.yaml', 'installed-history',
                  'client-source.go', 'client-tests.go', 'client-proof.json', 'prepare-history.py'}


def phase_order(value):
    require(isinstance(value, list) and value and all(isinstance(item, str) for item in value)
            and len(set(value)) == len(value)
            and value == [phase for phase in PHASES if phase in value]
            and (('web' in value) == ('sql' in value)), 'Select ordered independent phases; pair Web with SQL')
    return value


class Refused(ValueError):
    """An incomplete or mismatched fixture input cannot admit a window."""


def require(condition, message):
    if not condition:
        raise Refused(message)


def sha256(path):
    """Hash a file without loading image archives into memory."""
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    info = os.fstat(descriptor)
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
        os.close(descriptor)
        raise Refused('Use an owned regular input')
    with os.fdopen(descriptor, 'rb') as stream:
        digest = hashlib.sha256()
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
        return digest.hexdigest()


def regular(path, private=False):
    """Reject symlinks, foreign owners and unsafe private-file modes."""
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid(),
            'Use an owned regular fixture file')
    require(not private or not info.st_mode & 0o077, 'Protect fixture metadata with mode0600')
    return info


def read_json(path):
    regular(path, private=True)
    require(path.stat().st_size <= 65536, 'Fixture metadata exceeds its byte budget')
    def unique(pairs):
        value = {}
        for key, item in pairs:
            require(key not in value, 'Duplicate fixture metadata field')
            value[key] = item
        return value
    return json.loads(path.read_text(), object_pairs_hook=unique)


def write_json(path, value):
    """Publish protected state only after syncing the complete replacement."""
    if os.path.lexists(path):
        regular(path, private=True)
    fd, temporary = tempfile.mkstemp(prefix='.acceptance-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(value, stream, sort_keys=True, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def check_inputs(root):
    """Admit all exact input bytes before a caller starts any service."""
    manifest = read_json(root / 'inputs.json')
    require(set(manifest) == {'format_version', 'candidate', 'inputs', 'phases', 'owner_inputs'},
            'Unknown input manifest fields')
    require(type(manifest['format_version']) is int and manifest['format_version'] == 1
            and isinstance(manifest['candidate'], str)
            and re.fullmatch('[0-9a-f]{40}', manifest['candidate']),
            'Pin one complete candidate revision')
    phases = phase_order(manifest['phases'])
    inputs = manifest['inputs']
    required = REQUIRED_INPUTS if 'obs' in phases else REQUIRED_INPUTS - HISTORY_INPUTS
    require(isinstance(inputs, dict) and required <= set(inputs), 'Required fixture inputs are missing')
    for name, digest in inputs.items():
        require(isinstance(name, str) and re.fullmatch('[a-z0-9][a-z0-9_.-]*', name)
                and Path(name).name == name and name not in {'.', '..'},
                'Fixture input names must be basenames')
        require(isinstance(digest, str) and re.fullmatch('[0-9a-f]{64}', digest), 'Pin every input SHA256')
        regular(root / name)
        require(sha256(root / name) == digest, 'Fixture input checksum mismatch: ' + name)
    require((root / 'source-sha').read_text().strip() == manifest['candidate'], 'Candidate source differs')
    require(set(manifest['owner_inputs']) == set(phases), 'Every selected owner must be ready before launch')
    for phase, name in manifest['owner_inputs'].items():
        require(name in inputs, 'Pin the selected owner report')
        report = read_json(root / name)
        require(report['status'] == 'ready' and report['phase'] == phase
                and report['candidate'] == manifest['candidate'] and report['files'], 'Owner inputs are not ready')
        require(all(inputs.get(item) == digest for item, digest in report['files'].items()),
                'Owner input file differs from the admitted manifest')
    if 'obs' not in phases:
        return {'status': 'inputs-ready', 'candidate': manifest['candidate'], 'phases': phases,
                'manifest_sha256': sha256(root / 'inputs.json'), 'client_sha256': None,
                'self_test_scope': 'History client excluded from this window'}
    with (root / 'installed-history').open('rb') as stream:
        header = stream.read(64)
    require(len(header) == 64 and header[:6] == b'\x7fELF\x02\x01'
            and struct.unpack_from('<H', header, 18)[0] == 62,
            'The admitted history client must be Linux amd64 ELF')
    proof = read_json(root / 'client-proof.json')
    require(isinstance(proof, dict) and isinstance(proof.get('race_tests'), dict)
            and set(proof['race_tests']) == CLIENT_TESTS and proof.get('vet') == 'passed',
            'The complete client self-test and vet report is required')
    require(all(type(value) in (int, float) and math.isfinite(value) and value >= 0
                for value in proof['race_tests'].values()),
            'Client self-test observations are invalid')
    require(all(proof['target'].get(key) == value for key, value in
                {'GOOS': 'linux', 'GOARCH': 'amd64', 'CGO_ENABLED': '0', 'trimpath': True}.items()),
            'Client target report differs from the admitted platform')
    for actual, reported in [('client-source.go', 'main.go'), ('client-tests.go', 'main_test.go'),
                             ('installed-history', 'installed-history-linux-amd64')]:
        require(proof['files'][reported] == inputs[actual], 'Client source, tests or ELF report mismatch')
    return {'status': 'inputs-ready', 'candidate': manifest['candidate'], 'phases': phases,
            'manifest_sha256': sha256(root / 'inputs.json'), 'client_sha256': inputs['installed-history'],
            'self_test_scope': proof['scope']}


def validate_probes(path):
    """Require the staged host and workload negative controls, including IPv6/DNS."""
    regular(path, private=True)
    data = path.read_bytes()
    require(data.endswith(b'\n'), 'Capture ends with an incomplete record')
    rows = [json.loads(line) for line in data.splitlines()]
    counts = Counter()
    for row in rows:
        require(set(row) <= {'time', 'protocol', 'source', 'destination', 'dns_question'}
                and {'time', 'protocol', 'source', 'destination'} <= set(row), 'Unknown capture record')
        require(isinstance(row['time'], str) and re.fullmatch(
            r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z', row['time']),
            'The capture record has no UTC observation time')
        source = row['source']
        require(isinstance(source, str), 'The packet source is absent')
        if source.startswith('['):
            host, port = source[1:].rsplit(']:', 1)
            require(ipaddress.ip_address(host).version == 6, 'Invalid IPv6 packet source')
        else:
            host, port = source.rsplit(':', 1)
            require(ipaddress.ip_address(host).version == 4, 'Invalid IPv4 packet source')
        require(port.isdecimal() and 0 < int(port) < 65536, 'Invalid packet source port')
        key = (row['protocol'], row['destination'], row.get('dns_question', ''))
        counts[key] += 1
    require(counts == Counter({('tcp', '192.0.2.10:9', ''): 2,
                               ('tcp', '[2001:db8::10]:9', ''): 1,
                               ('udp', '192.0.2.53:53', 'probe.invalid. A'): 2}),
            'The recorder controls are missing, duplicated or foreign')
    return data


def capture_result(evidence, run_id):
    """Never promote a snapshot or a forced-kill capture to a final pass."""
    result = {'status': 'capture-incomplete', 'run_id': run_id}
    log = evidence / 'egress-live.jsonl'
    closed = False
    try:
        regular(log, private=True)
        descriptor = os.open(log, os.O_RDONLY | os.O_NOFOLLOW)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        result.update(bytes=log.stat().st_size, sha256=sha256(log))
        final = read_json(evidence / 'capture-ended.json')
        require(set(final) == {'run_id', 'recorder_exit', 'workloads_stopped', 'bytes', 'sha256'},
                'Capture closure metadata differs')
        require(final['run_id'] == run_id and type(final['recorder_exit']) is int and final['recorder_exit'] == 0
                and final['workloads_stopped'] is True, 'Capture shutdown was not confirmed')
        require(type(final['bytes']) is int and final['bytes'] == result['bytes'] and final['sha256'] == result['sha256'],
                'Capture changed after its recorder closed')
        baseline = validate_probes(evidence / 'egress-baseline.jsonl')
        closed = True
        require(log.read_bytes() == baseline, 'Product traffic or a changed probe appeared')
        result['status'] = 'capture-passed'
    except (OSError, ValueError, KeyError, TypeError) as error:
        result['reason'] = str(error)
        # A complete, closed capture with unexpected traffic is a failed capture.
        if closed:
            result['status'] = 'capture-failed'
    write_json(evidence / 'capture-result.json', result)
    return result


def initialize_control(evidence, run_id, candidate, deadline, phases=None):
    """Create one fresh metadata channel before launching the parent."""
    require(str(uuid.UUID(run_id)) == run_id, 'Use a canonical window UUID')
    require(isinstance(candidate, str) and re.fullmatch('[0-9a-f]{40}', candidate), 'Pin the candidate')
    require(type(deadline) is int and deadline > time.time(), 'Set a future hard deadline')
    phases = phase_order(['obs'] if phases is None else phases)
    path = evidence / 'control.json'
    state = {'run_id': run_id, 'candidate': candidate, 'deadline': deadline, 'phases': phases,
                     'generation': 0, 'phase': 'prepared', 'driver': None,
                     'read_passed': False, 'next_hook': 0, 'pending_hook': None,
                     'history_prepared': False}
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'w') as stream:
        json.dump(state, stream)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())


def handoff(evidence, role, action, generation, hook=None):
    """Serialize the OBS driver claim and DEL-only ordered hook acknowledgements."""
    carrier = evidence / 'control.lock'
    fd = os.open(carrier, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        require(stat.S_ISREG(os.fstat(fd).st_mode) and os.fstat(fd).st_uid == os.geteuid(),
                'Invalid coordination lock carrier')
        fcntl.flock(fd, fcntl.LOCK_EX)
        state = read_json(evidence / 'control.json')
        if action == 'status':
            return state
        require(type(generation) is int and state['generation'] == generation, 'Stale handoff generation')
        require(action == 'failed' or time.time() < state['deadline'], 'The installation window expired')
        if action == 'installed-ready':
            require(role == 'DEL' and state['phase'] == 'prepared', 'Installed readiness is not pending')
            ready = read_json(evidence / 'acceptance-ready.json')
            provenance = read_json(evidence / 'installed-provenance.json')
            require(ready['candidate'] == state['candidate'] and ready['scope_prepared'] is True
                    and provenance['build_revision'] == state['candidate']
                    and provenance['actual_source_label'] == state['candidate']
                    and provenance['build_modified'] == 'false', 'Installed source or scope is incomplete')
            state['phase'] = 'obs-ready' if state['phases'][0] == 'obs' else state['phases'][0]
        elif action == 'begin-history':
            require(role == 'DEL' and state['phase'] == 'obs-ready' and state['driver'] is None,
                    'Prepare history only before its single driver claim')
            state.update(phase='history-preparation', history_prepared=False)
        elif action == 'history-ready':
            require(role == 'DEL' and state['phase'] == 'history-preparation', 'No history preparation is pending')
            seed = read_json(evidence / 'history-seed.json')
            ready = read_json(evidence / 'acceptance-ready.json')
            inputs = read_json(evidence / 'obs-history-input.json')
            require(seed['source_sha'] == state['candidate'] and seed['status'] == 'native_replay_verified'
                    and seed['workspace_id'] == inputs['workspace_id'] == ready['primary_workspace_id']
                    and ready['driver_input_sha256'] == sha256(evidence / 'obs-history-input.json'),
                    'Native preparation or installed input binding is incomplete')
            state.update(phase='obs-ready', history_prepared=True)
        elif action == 'claim':
            require(role == 'OBS' and state['phase'] == 'obs-ready' and state['driver'] is None,
                    'Only OBS can claim the single driver, once')
            require(state['history_prepared'] is True, 'Native preparation must finish before the counter baseline')
            ready = read_json(evidence / 'acceptance-ready.json')
            inputs = read_json(evidence / 'obs-history-input.json')
            require(inputs['source_sha'] == state['candidate']
                    and ready['driver_input_sha256'] == sha256(evidence / 'obs-history-input.json')
                    and inputs['workspace_id'] == ready['primary_workspace_id'],
                    'Driver input differs from installed readiness')
            policy = read_json(evidence / 'history-inputs' / 'monitoring.json')['fleet_history']
            raw_from = datetime.datetime.strptime(inputs['raw_window']['from'], '%Y-%m-%dT%H:%M:%SZ')
            raw_epoch = raw_from.replace(tzinfo=datetime.timezone.utc).timestamp()
            require(raw_epoch - 45 >= time.time() - policy['raw_horizon_seconds'],
                    'The raw window aged out; request fresh preparation before claiming the driver')
            state.update(driver='OBS', phase='obs')
        elif action == 'read-passed':
            require(role == 'OBS' and state['phase'] == 'obs' and not state['read_passed'],
                    'Only the claimed driver can complete the initial read')
            state['read_passed'] = True
        elif action == 'request-hook':
            require(role == 'OBS' and state['phase'] == 'obs' and state['read_passed']
                    and state['pending_hook'] is None and state['next_hook'] < len(HOOKS)
                    and hook == HOOKS[state['next_hook']], 'Hook is not the next authorized phase')
            state['pending_hook'] = hook
        elif action == 'hook-passed':
            require(role == 'DEL' and state['phase'] == 'obs' and hook is not None
                    and state['pending_hook'] == hook, 'DEL can acknowledge only the pending hook')
            state.update(pending_hook=None, next_hook=state['next_hook'] + 1)
        elif action == 'failed':
            allowed = {'prepared': {'DEL'}, 'obs-ready': {'DEL'}, 'history-preparation': {'DEL'}, 'obs': {'OBS', 'DEL'},
                       'web': {'WEB', 'DEL'}, 'sql': {'DEL'}, 'cli': {'CLI', 'DEL'}}
            require(role in allowed.get(state['phase'], set()), 'No active owned phase')
            state['phase'] = 'failed'
        elif action == 'obs-finished':
            require(role == 'OBS' and state['phase'] == 'obs' and state['read_passed']
                    and state['pending_hook'] is None and state['next_hook'] == len(HOOKS),
                    'The driver phases are incomplete')
            state['phase'] = 'complete'
        elif action == 'web-finished':
            require(role == 'WEB' and state['phase'] == 'web', 'Web cannot overlap the driver')
            state['phase'] = 'sql'
        elif action == 'sql-finished':
            require(role == 'DEL' and state['phase'] == 'sql', 'Independent SQL proof is not pending')
            position = state['phases'].index('sql') + 1
            next_phase = state['phases'][position] if position < len(state['phases']) else 'complete'
            state['phase'] = 'obs-ready' if next_phase == 'obs' else next_phase
        elif action == 'cli-finished':
            require(role == 'CLI' and state['phase'] == 'cli', 'CLI must follow independent SQL')
            state['phase'] = 'obs-ready' if 'obs' in state['phases'] else 'complete'
        else:
            raise Refused('Unknown coordination action')
        state['generation'] += 1
        write_json(evidence / 'control.json', state)
        return state
    finally:
        fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['preflight', 'capture', 'handoff'])
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--run-id')
    parser.add_argument('--role')
    parser.add_argument('--action')
    parser.add_argument('--generation', type=int)
    parser.add_argument('--hook')
    args = parser.parse_args()
    if args.command == 'preflight':
        result = check_inputs(args.root)
    elif args.command == 'capture':
        result = capture_result(args.root, args.run_id)
    else:
        result = handoff(args.root, args.role, args.action, args.generation, args.hook)
    print(json.dumps(result, sort_keys=True))
    if args.command == 'capture' and result['status'] != 'capture-passed':
        raise SystemExit(1)


if __name__ == '__main__':
    main()
