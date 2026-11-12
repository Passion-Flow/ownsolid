"""Verify runner admission and cleanup without starting Docker or a product."""
import json
import socket
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
from unittest import mock
import uuid

import acceptance_guard as guard
import host_runner


class Runner(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.config = {'output_directory': str(self.root), 'container_name': 'lane-del-unit',
                       'run_id': str(uuid.uuid4()), 'operation_seconds': 1, 'cleanup_seconds': 1,
                       'hard_timeout_seconds': 60, 'startup_seconds': 1, 'suite_seconds': 10,
                       'cpus': 1, 'memory_bytes': 1024, 'docker_tmpfs_bytes': 512, 'pids_limit': 64,
                       'images': dict.fromkeys(['control', 'evaluator', 'postgres', 'metrics', 'logs', 'dind'],
                                               'sha256:' + 'a' * 64), 'relay_port': 8443,
                       'docker_socket': '/unit.sock'}
        self.admission = {'candidate': 'a' * 40, 'phases': ['cli']}

    def test_failed_admission_cannot_inspect_or_launch_docker(self):
        with mock.patch.object(host_runner, 'configuration') as configuration, \
                mock.patch.object(host_runner.Window, 'docker') as docker:
            with self.assertRaises(OSError):
                host_runner.run(self.root)
            configuration.assert_not_called()
            docker.assert_not_called()

    def runtime_configuration(self):
        config = dict(self.config)
        config['output_directory'] = str(self.root / 'new-evidence')
        config['heavy_locks'] = [str(self.root / 'a.lock'), str(self.root / 'b.lock')]
        config.update(minimum_free_memory_bytes=1, minimum_available_memory_bytes=1,
                      minimum_disk_bytes=1, maximum_io_avg10=5)
        sock = socket.socket(socket.AF_UNIX)
        self.addCleanup(sock.close)
        sock.bind(str(self.root / 's'))
        config['docker_socket'] = str(self.root / 's')
        guard.write_json(self.root / 'runtime.json', config)
        return config

    def test_runtime_rejects_unknown_values_and_reserves_cleanup_time(self):
        config = self.runtime_configuration()
        self.assertEqual(host_runner.configuration(self.root)['container_name'], 'lane-del-unit')
        for key, value in [('unknown', 1), ('cpus', False), ('memory_bytes', '1024'),
                           ('suite_seconds', 60), ('container_name', 'lane-b-foreign')]:
            with self.subTest(key=key):
                bad = dict(config)
                bad[key] = value
                guard.write_json(self.root / 'runtime.json', bad)
                with self.assertRaises(guard.Refused):
                    host_runner.configuration(self.root)

    def test_mismatched_relay_never_launches_an_admitted_window(self):
        config = self.runtime_configuration()
        config['images'].pop('evaluator')
        config['relay_port'] = 8444
        guard.write_json(self.root / 'runtime.json', config)
        with mock.patch.object(guard, 'check_inputs', return_value=self.admission):
            with mock.patch.object(host_runner.Window, 'docker') as docker:
                with self.assertRaisesRegex(guard.Refused, 'installed Control fixture'):
                    host_runner.run(self.root)
                docker.assert_not_called()

    def test_runtime_rejects_unpinned_images_before_launch(self):
        config = self.runtime_configuration()
        config['images']['control'] = 'example:latest'
        guard.write_json(self.root / 'runtime.json', config)
        with self.assertRaises(guard.Refused):
            host_runner.configuration(self.root)

    def test_occupied_name_is_never_stopped(self):
        window = host_runner.Window(self.root, self.config, self.admission)
        with mock.patch.object(window, 'docker', return_value=subprocess.CompletedProcess([], 0, '[]', '')) as docker:
            with self.assertRaisesRegex(guard.Refused, 'occupied'):
                window.launch()
            window.close()
            self.assertEqual(docker.call_count, 1)

    def test_graceful_stop_timeout_still_removes_owned_namespace(self):
        window = host_runner.Window(self.root, self.config, self.admission)
        window.created = True
        window.hard_deadline = time.time() + 10
        window.monotonic_deadline = time.monotonic() + 10
        calls = []
        state = {'State': {'Running': True}}
        def docker(*args, **kwargs):
            calls.append(args[0])
            if args[0] == 'stop':
                raise subprocess.TimeoutExpired('docker stop', 1)
            if args[0] == 'rm':
                state.clear()
            return subprocess.CompletedProcess([], 0, '', '')
        with mock.patch.object(window, 'inspect', side_effect=lambda: state or None), \
                mock.patch.object(window, 'docker', side_effect=docker):
            window.close()
        self.assertEqual(calls, ['stop', 'logs', 'rm'])
        result = guard.read_json(self.root / 'window-result.json')
        self.assertFalse(result['passed'])
        self.assertEqual(result['capture']['status'], 'capture-incomplete')
        self.assertEqual(result['cleanup_errors'], ['TimeoutExpired'])

    def test_foreign_label_blocks_cleanup(self):
        window = host_runner.Window(self.root, self.config, self.admission)
        window.created = True
        payload = [{'Config': {'Labels': {'io.ownsolid.acceptance.run_id': str(uuid.uuid4())}}}]
        with mock.patch.object(window, 'docker', return_value=subprocess.CompletedProcess([], 0, json.dumps(payload), '')) as docker:
            window.close()
            self.assertEqual(docker.call_count, 1)
        result = guard.read_json(self.root / 'window-result.json')
        self.assertFalse(result['passed'])
        self.assertEqual(result['cleanup_errors'], ['Refused'])

    def test_complete_handoff_stops_relay_and_releases_suite_once(self):
        window = host_runner.Window(self.root, self.config, self.admission)
        window.hard_deadline = time.time() + 60
        window.monotonic_deadline = time.monotonic() + 60
        relay = mock.Mock()
        relay.poll.return_value = None
        window.relay = relay
        guard.initialize_control(self.root, self.config['run_id'], self.admission['candidate'], int(time.time()) + 60, ['cli'])
        metadata = guard.read_json(self.root / 'control.json')
        metadata['phase'] = 'complete'
        guard.write_json(self.root / 'control.json', metadata)
        states = [{'State': {'Running': True}}, {'State': {'Running': True}},
                  {'State': {'Running': False, 'ExitCode': 0}}]
        with mock.patch.object(window, 'launch'), mock.patch.object(window, 'inspect', side_effect=states), \
                mock.patch.object(host_runner.time, 'sleep'):
            window.run()
        self.assertTrue((self.root / 'owners-finished').exists())
        relay.terminate.assert_called_once()
        relay.wait.assert_called_once()
        self.assertEqual(window.result, 0)


if __name__ == '__main__':
    unittest.main()
