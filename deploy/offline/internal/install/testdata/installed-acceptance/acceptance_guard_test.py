"""Exercise admission and finalization; these tests do not claim an air gap."""
import hashlib
import json
import os
from pathlib import Path
import select
import struct
import subprocess
import sys
import tempfile
import time
import unittest
import uuid

import acceptance_guard as guard


class Guards(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.run_id = str(uuid.uuid4())
        self.candidate = 'a' * 40

    def write(self, name, data):
        path = self.root / name
        path.write_bytes(data)
        path.chmod(0o600)
        return path

    def manifest(self):
        for name in guard.REQUIRED_INPUTS:
            self.write(name, ('fixture ' + name).encode())
        elf = bytearray(64)
        elf[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', elf, 18, 62)
        self.write('installed-history', elf)
        self.write('source-sha', (self.candidate + '\n').encode())
        proof = {'scope': 'Synthetic self-test metadata for admission unit tests',
                 'race_tests': dict.fromkeys(guard.CLIENT_TESTS, 0), 'vet': 'passed',
                 'target': {'GOOS': 'linux', 'GOARCH': 'amd64', 'CGO_ENABLED': '0', 'trimpath': True},
                 'files': {reported: guard.sha256(self.root / actual) for actual, reported in
                           [('client-source.go', 'main.go'), ('client-tests.go', 'main_test.go'),
                            ('installed-history', 'installed-history-linux-amd64')]}}
        guard.write_json(self.root / 'client-proof.json', proof)
        owner = {'status': 'ready', 'candidate': self.candidate, 'phase': 'obs',
                 'files': {'installed-history': guard.sha256(self.root / 'installed-history')}}
        guard.write_json(self.root / 'owner-obs.json', owner)
        manifest = {'format_version': 1, 'candidate': self.candidate, 'phases': ['obs'],
                    'owner_inputs': {'obs': 'owner-obs.json'},
                    'inputs': {name: guard.sha256(self.root / name) for name in guard.REQUIRED_INPUTS}}
        manifest['inputs']['owner-obs.json'] = guard.sha256(self.root / 'owner-obs.json')
        guard.write_json(self.root / 'inputs.json', manifest)
        return manifest

    def controls(self):
        records = [
            ('tcp', '192.0.2.10:9', ''), ('tcp', '[2001:db8::10]:9', ''),
            ('udp', '192.0.2.53:53', 'probe.invalid. A'),
            ('tcp', '192.0.2.10:9', ''), ('udp', '192.0.2.53:53', 'probe.invalid. A')]
        rows = []
        for protocol, destination, question in records:
            row = {'time': '2026-11-12T00:00:00Z', 'source': '198.18.0.1:40000',
                   'protocol': protocol, 'destination': destination}
            if question:
                row['dns_question'] = question
            rows.append(json.dumps(row).encode() + b'\n')
        return b''.join(rows)

    def closed(self):
        log = self.root / 'egress-live.jsonl'
        guard.write_json(self.root / 'capture-ended.json', {
            'run_id': self.run_id, 'recorder_exit': 0, 'workloads_stopped': True,
            'bytes': log.stat().st_size, 'sha256': guard.sha256(log)})

    def admitted(self, phases=None):
        guard.initialize_control(self.root, self.run_id, self.candidate, int(time.time()) + 300, phases)
        inputs = {'source_sha': self.candidate, 'workspace_id': str(uuid.uuid4())}
        inputs['raw_window'] = {'from': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(time.time() - 600))}
        (self.root / 'history-inputs').mkdir(mode=0o700)
        guard.write_json(self.root / 'history-inputs' / 'monitoring.json',
                         {'fleet_history': {'raw_horizon_seconds': 3600}})
        guard.write_json(self.root / 'obs-history-input.json', inputs)
        guard.write_json(self.root / 'acceptance-ready.json', {
            'candidate': self.candidate, 'scope_prepared': True,
            'primary_workspace_id': inputs['workspace_id'],
            'driver_input_sha256': guard.sha256(self.root / 'obs-history-input.json')})
        guard.write_json(self.root / 'installed-provenance.json', {
            'build_revision': self.candidate, 'actual_source_label': self.candidate,
            'build_modified': 'false'})
        state = guard.handoff(self.root, 'DEL', 'installed-ready', 0)
        # Most state-machine tests start after synthetic preparation, without network I/O.
        state['history_prepared'] = True
        guard.write_json(self.root / 'control.json', state)
        return state

    def test_all_inputs_are_admitted_before_a_launcher_can_run(self):
        self.manifest()
        self.assertEqual(guard.check_inputs(self.root)['status'], 'inputs-ready')
        (self.root / 'installed-history').unlink()
        started = False
        with self.assertRaises(OSError):
            guard.check_inputs(self.root)
            started = True
        self.assertFalse(started)

    def test_missing_source_selftest_or_changed_client_is_refused(self):
        for name in ['client-source.go', 'client-tests.go', 'client-proof.json', 'installed-history', 'images.tar']:
            with self.subTest(name=name):
                self.manifest()
                self.write(name, b'changed')
                with self.assertRaises(guard.Refused):
                    guard.check_inputs(self.root)

    def test_elf_architecture_and_matching_selftest_are_required(self):
        manifest = self.manifest()
        binary = bytearray((self.root / 'installed-history').read_bytes())
        struct.pack_into('<H', binary, 18, 183)
        self.write('installed-history', binary)
        manifest['inputs']['installed-history'] = guard.sha256(self.root / 'installed-history')
        owner = guard.read_json(self.root / 'owner-obs.json')
        owner['files']['installed-history'] = manifest['inputs']['installed-history']
        guard.write_json(self.root / 'owner-obs.json', owner)
        manifest['inputs']['owner-obs.json'] = guard.sha256(self.root / 'owner-obs.json')
        guard.write_json(self.root / 'inputs.json', manifest)
        with self.assertRaisesRegex(guard.Refused, 'Linux amd64'):
            guard.check_inputs(self.root)

    def test_symlinks_and_unprotected_metadata_are_refused(self):
        self.manifest()
        path = self.root / 'client-source.go'
        path.rename(self.root / 'retained-source')
        path.symlink_to('retained-source')
        with self.assertRaises(guard.Refused):
            guard.check_inputs(self.root)
        (self.root / 'inputs.json').chmod(0o644)
        with self.assertRaisesRegex(guard.Refused, 'mode0600'):
            guard.check_inputs(self.root)

    def test_normal_capture_includes_the_last_writer_record(self):
        baseline = self.controls()
        self.write('egress-baseline.jsonl', baseline)
        self.write('egress-live.jsonl', baseline[:-1])
        producer = subprocess.Popen([sys.executable, '-c',
            'import os,sys; f=open(sys.argv[1],"ab"); f.write(b"\\n"); f.flush(); os.fsync(f.fileno()); f.close()',
            str(self.root / 'egress-live.jsonl')])
        self.assertEqual(producer.wait(timeout=10), 0)
        self.closed()
        result = guard.capture_result(self.root, self.run_id)
        self.assertEqual(result['status'], 'capture-passed')
        self.assertEqual(result['sha256'], hashlib.sha256(baseline).hexdigest())

    def test_killed_writer_retains_bytes_but_cannot_pass(self):
        baseline = self.controls()
        self.write('egress-baseline.jsonl', baseline)
        log = self.root / 'egress-live.jsonl'
        producer = subprocess.Popen([sys.executable, '-c',
            'import os,sys; f=open(sys.argv[1],"wb"); f.write(bytes.fromhex(sys.argv[2])); '
            'f.flush(); os.fsync(f.fileno()); print("ready",flush=True); sys.stdin.readline()',
            str(log), baseline.hex()], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
        try:
            self.assertTrue(select.select([producer.stdout], [], [], 10)[0])
            self.assertEqual(producer.stdout.readline(), b'ready\n')
            producer.kill()
            producer.wait(timeout=10)
        finally:
            if producer.poll() is None:
                producer.kill()
                producer.wait(timeout=10)
            producer.stdin.close()
            producer.stdout.close()
        log.chmod(0o600)
        result = guard.capture_result(self.root, self.run_id)
        self.assertEqual(result['status'], 'capture-incomplete')
        self.assertEqual(log.read_bytes(), baseline)

    def test_late_traffic_and_wrong_or_missing_closure_never_pass(self):
        baseline = self.controls()
        self.write('egress-baseline.jsonl', baseline)
        self.write('egress-live.jsonl', baseline + baseline.splitlines(keepends=True)[0])
        self.closed()
        self.assertEqual(guard.capture_result(self.root, self.run_id)['status'], 'capture-failed')
        self.assertEqual(guard.capture_result(self.root, str(uuid.uuid4()))['status'], 'capture-incomplete')
        (self.root / 'capture-ended.json').unlink()
        self.assertEqual(guard.capture_result(self.root, self.run_id)['status'], 'capture-incomplete')

    def test_no_claim_until_installed_and_no_second_initialization(self):
        guard.initialize_control(self.root, self.run_id, self.candidate, int(time.time()) + 300)
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'OBS', 'claim', 0)
        with self.assertRaises(FileExistsError):
            guard.initialize_control(self.root, str(uuid.uuid4()), self.candidate, int(time.time()) + 300)

    def test_only_obs_can_claim_and_stale_claim_is_refused(self):
        self.admitted()
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'DEL', 'claim', 1)
        state = guard.handoff(self.root, 'OBS', 'claim', 1)
        self.assertEqual(state['driver'], 'OBS')
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'OBS', 'claim', 1)
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'OBS', 'claim', 2)

    def test_hook_order_roles_and_failure_are_fenced(self):
        self.admitted()
        guard.handoff(self.root, 'OBS', 'claim', 1)
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'OBS', 'request-hook', 2, 'long-stop')
        guard.handoff(self.root, 'OBS', 'read-passed', 2)
        guard.handoff(self.root, 'OBS', 'request-hook', 3, 'long-stop')
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'OBS', 'hook-passed', 4, 'long-stop')
        guard.handoff(self.root, 'DEL', 'hook-passed', 4, 'long-stop')
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'DEL', 'hook-passed', 5, 'long-stop')
        guard.handoff(self.root, 'DEL', 'failed', 5)
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'OBS', 'request-hook', 6, 'long-start')

    def test_expired_window_allows_metadata_and_failure_only(self):
        self.admitted()
        state = guard.read_json(self.root / 'control.json')
        state['deadline'] = 1
        guard.write_json(self.root / 'control.json', state)
        self.assertEqual(guard.handoff(self.root, 'DEL', 'status', None)['phase'], 'obs-ready')
        with self.assertRaisesRegex(guard.Refused, 'expired'):
            guard.handoff(self.root, 'OBS', 'claim', 1)
        self.assertEqual(guard.handoff(self.root, 'DEL', 'failed', 1)['phase'], 'failed')

    def test_two_processes_cannot_both_claim_the_driver(self):
        self.admitted()
        command = [sys.executable, str(Path(guard.__file__)), 'handoff', '--root', str(self.root),
                   '--role', 'OBS', '--action', 'claim', '--generation', '1']
        processes = [subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                     for _ in range(2)]
        results = [process.communicate(timeout=10) for process in processes]
        self.assertEqual(sorted(process.returncode == 0 for process in processes), [False, True])
        self.assertEqual(guard.handoff(self.root, 'DEL', 'status', None)['generation'], 2)
        self.assertTrue(any(b'Stale handoff' in stderr for _, stderr in results))

    def test_complete_order_requires_all_hooks_web_sql_and_cli(self):
        state = self.admitted(['web', 'sql', 'cli', 'obs'])
        def advance(role, action, hook=None):
            nonlocal state
            state = guard.handoff(self.root, role, action, state['generation'], hook)
        with self.assertRaises(guard.Refused):
            advance('OBS', 'claim')
        advance('WEB', 'web-finished')
        advance('DEL', 'sql-finished')
        advance('CLI', 'cli-finished')
        advance('OBS', 'claim')
        advance('OBS', 'read-passed')
        with self.assertRaises(guard.Refused):
            advance('OBS', 'obs-finished')
        for hook in guard.HOOKS:
            advance('OBS', 'request-hook', hook)
            advance('DEL', 'hook-passed', hook)
        advance('OBS', 'obs-finished')
        self.assertEqual(state['phase'], 'complete')

    def test_ready_ui_and_cli_do_not_wait_for_an_excluded_history_client(self):
        manifest = self.manifest()
        manifest['phases'] = ['cli']
        guard.write_json(self.root / 'owner-cli.json', {
            'status': 'ready', 'phase': 'cli', 'candidate': self.candidate,
            'files': {'scope.test': manifest['inputs']['scope.test']}})
        manifest['owner_inputs'] = {'cli': 'owner-cli.json'}
        manifest['inputs']['owner-cli.json'] = guard.sha256(self.root / 'owner-cli.json')
        for name in guard.HISTORY_INPUTS:
            manifest['inputs'].pop(name)
            (self.root / name).unlink()
        manifest['inputs'].pop('owner-obs.json')
        guard.write_json(self.root / 'inputs.json', manifest)
        result = guard.check_inputs(self.root)
        self.assertEqual(result['status'], 'inputs-ready')
        self.assertIsNone(result['client_sha256'])

    def test_incomplete_shutdown_and_changed_final_bytes_are_incomplete(self):
        baseline = self.controls()
        self.write('egress-baseline.jsonl', baseline)
        log = self.write('egress-live.jsonl', baseline)
        self.closed()
        final = guard.read_json(self.root / 'capture-ended.json')
        final['workloads_stopped'] = False
        guard.write_json(self.root / 'capture-ended.json', final)
        self.assertEqual(guard.capture_result(self.root, self.run_id)['status'], 'capture-incomplete')
        self.closed()
        self.write(log.name, baseline[:-1])
        self.assertEqual(guard.capture_result(self.root, self.run_id)['status'], 'capture-incomplete')

    def test_missing_client_test_cannot_admit_a_matching_binary(self):
        manifest = self.manifest()
        proof = guard.read_json(self.root / 'client-proof.json')
        proof['race_tests'].pop(next(iter(guard.CLIENT_TESTS)))
        guard.write_json(self.root / 'client-proof.json', proof)
        manifest['inputs']['client-proof.json'] = guard.sha256(self.root / 'client-proof.json')
        guard.write_json(self.root / 'inputs.json', manifest)
        with self.assertRaisesRegex(guard.Refused, 'self-test'):
            guard.check_inputs(self.root)

    def test_boolean_exit_code_cannot_fabricate_recorder_success(self):
        baseline = self.controls()
        self.write('egress-baseline.jsonl', baseline)
        self.write('egress-live.jsonl', baseline)
        self.closed()
        final = guard.read_json(self.root / 'capture-ended.json')
        final['recorder_exit'] = False
        guard.write_json(self.root / 'capture-ended.json', final)
        self.assertEqual(guard.capture_result(self.root, self.run_id)['status'], 'capture-incomplete')

    def test_preparation_fences_the_driver_and_binds_the_new_scope(self):
        self.admitted()
        state = guard.handoff(self.root, 'DEL', 'begin-history', 1)
        self.assertEqual(state['phase'], 'history-preparation')
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'OBS', 'claim', 2)
        inputs = guard.read_json(self.root / 'obs-history-input.json')
        guard.write_json(self.root / 'history-seed.json', {'source_sha': self.candidate,
            'workspace_id': inputs['workspace_id'], 'status': 'native_replay_verified'})
        state = guard.handoff(self.root, 'DEL', 'history-ready', 2)
        self.assertTrue(state['history_prepared'])
        guard.handoff(self.root, 'OBS', 'claim', 3)
        with self.assertRaises(guard.Refused):
            guard.handoff(self.root, 'DEL', 'begin-history', 4)


    def test_aged_raw_window_is_refused_before_any_client_request(self):
        self.admitted()
        inputs = guard.read_json(self.root / 'obs-history-input.json')
        inputs['raw_window']['from'] = '2000-01-01T00:00:00Z'
        guard.write_json(self.root / 'obs-history-input.json', inputs)
        ready = guard.read_json(self.root / 'acceptance-ready.json')
        ready['driver_input_sha256'] = guard.sha256(self.root / 'obs-history-input.json')
        guard.write_json(self.root / 'acceptance-ready.json', ready)
        with self.assertRaisesRegex(guard.Refused, 'aged out'):
            guard.handoff(self.root, 'OBS', 'claim', 1)


if __name__ == '__main__':
    unittest.main()
