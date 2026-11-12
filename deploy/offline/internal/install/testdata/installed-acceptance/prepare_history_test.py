"""Check installation-specific bindings without invoking a metrics store."""
import unittest
import uuid
import socket
from pathlib import Path
import tempfile

import prepare_history


class Bindings(unittest.TestCase):
    def test_preparation_uses_the_parents_explicit_local_docker_socket(self):
        with tempfile.TemporaryDirectory() as temporary, socket.socket(socket.AF_UNIX) as listener:
            path = str(Path(temporary) / 'daemon.sock')
            listener.bind(path)
            self.assertEqual(prepare_history.docker_command(path), ('docker', '--host', 'unix://' + path))
            with self.assertRaises(ValueError):
                prepare_history.docker_command('tcp://outside.invalid:2375')

    def test_input_uses_observed_email_scope_and_a_distinct_session(self):
        ready = {'admin_origin': 'https://admin.ownsolid.test:8443',
                 'administrator_email': 'fixture@example.invalid'}
        scope = {'primary_workspace': str(uuid.uuid4()), 'foreign_workspace': str(uuid.uuid4())}
        policy = {'raw_origin': 'https://10.77.0.1:18428', 'long_origin': 'https://10.77.0.1:28428'}
        windows = {tier: {'workspace_id': scope['primary_workspace'],
                         'cells': [{'site_id': 'default', 'cell_id': 'default', 'observed': 2, 'reachable': 0}]}
                   for tier in ['raw', 'rollup']}
        run = str(uuid.uuid4())
        result = prepare_history.client_input(ready, scope, policy, windows, 'a' * 40, run)
        self.assertEqual(result['email'], ready['administrator_email'])
        self.assertEqual(result['workspace_id'], scope['primary_workspace'])
        self.assertEqual(result['foreign_workspace_id'], scope['foreign_workspace'])
        self.assertEqual(result['raw_window'], windows['raw'])
        self.assertEqual(result['session_file'], '/evidence/history-session-' + run + '.json')
        self.assertEqual(len(set(result['required_commands'])), 8)
        other = prepare_history.client_input(ready, scope, policy, windows, 'a' * 40, str(uuid.uuid4()))
        self.assertNotEqual(result['session_file'], other['session_file'])
        self.assertNotIn('password', result)

    def test_store_redirects_never_trigger_a_second_request(self):
        self.assertIsNone(prepare_history.NoRedirect().redirect_request(
            None, None, 302, 'Found', {}, 'https://outside.example.invalid'))


if __name__ == '__main__':
    unittest.main()
