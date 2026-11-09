"""Offline replay against a real data-processor publication and pinned model catalog."""

import copy
import json
import os
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
import unittest
from uuid import uuid4

from projectone_data.publication import materialize
from projectone_data.validation import Row
from projectone_model_lifecycle.evaluation_report import compact, sha256
from projectone_training.catalog import catalog_digest


ROOT = Path(__file__).resolve().parents[3]
RECIPE = ROOT / 'api/model-lifecycle/v1/recipe.json'
CATALOG = ROOT / 'api/agent/v1/model-catalog.json'


class EvaluationReplayTest(unittest.TestCase):
    def setUp(self):
        self.temp = TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        partition = self.root / 'partition'
        partition.mkdir()
        rows = [
            Row(i * 3 + j + 1, f'fixture-group-{i}', f'Fixture case {i}-{j}', label)
            for i in range(2) for j, label in enumerate(('account', 'billing', 'technical'))
        ]
        raw = b''.join(compact(row.value()) + b'\n' for row in rows)
        manifest = materialize(rows, partition, '00112233445566778899aabbccddeeff', 5000, sha256(raw))
        self.validation = partition / 'validation.jsonl'
        self.version = {
            'id': str(uuid4()), 'dataset_id': str(uuid4()), 'workspace_id': str(uuid4()),
            'manifest_sha256': manifest['manifest_file']['sha256'],
            'effective_source_sha256': manifest['source_sha256'], 'manifest': manifest,
        }
        recipe = json.loads(RECIPE.read_bytes())
        configuration = {name: field['default'] for name, field in recipe['configuration']['properties'].items()}
        configuration['device'] = 'cpu_verification'
        lines = [json.loads(line) for line in self.validation.read_bytes().splitlines()]
        observations = ('account', None, 'technical')
        self.capture = {
            'schema_version': 1, 'status': 'prepared', 'worker_version': '0.1.0',
            'recipe_id': recipe['recipe_id'], 'recipe_sha256': sha256(compact(recipe)),
            'model_catalog_sha256': catalog_digest(json.loads(CATALOG.read_bytes())),
            'workspace_id': self.version['workspace_id'],
            'heldout_id': str(uuid4()), 'attempt_id': str(uuid4()),
            'heldout_sha256': sha256(self.validation.read_bytes()),
            'configuration': configuration, 'configuration_sha256': sha256(compact(configuration)),
            'device_profile': 'cpu_verification', 'computation_dtype': 'float32',
            'execution_identity': {
                'profile': 'native_cpu_verification', 'python': '3.12.15',
                'worker_source_sha256': sha256(b'synthetic capture fixture'),
            },
            'packages': {'torch': 'synthetic fixture'},
            'chat_template_sha256': sha256(b'fixture template'),
            'instruction_sha256': sha256(recipe['instruction'].encode()),
            'row_count': len(lines),
            'base': {'correct': 2, 'row_count': 3, 'confusion': [[1, 0, 0, 0], [0, 0, 0, 1], [0, 0, 1, 0]]},
            'cases': [
                {
                    'source_id': row['source_id'], 'prompt_sha256': sha256(row['prompt'].encode()),
                    'expected_label': row['completion'],
                    'base': {'predicted_label': predicted, 'output_sha256': sha256(f'fixture prediction {i}'.encode()),
                             'generated_tokens': 1, 'duration_milliseconds': i},
                }
                for i, (row, predicted) in enumerate(zip(lines, observations, strict=True))
            ],
        }

    def replay(self, *, version=None, capture=None, validation=None, catalog=CATALOG, recipe=RECIPE,
               seed='0', extra=(), version_pin=None, capture_pin=None):
        version_file = self.root / 'version.json'
        capture_file = self.root / 'capture.json'
        version_bytes = compact(self.version if version is None else version)
        capture_bytes = compact(self.capture if capture is None else capture)
        version_file.write_bytes(version_bytes)
        capture_file.write_bytes(capture_bytes)
        return subprocess.run(
            [sys.executable, '-B', '-m', 'projectone_model_lifecycle.evaluation_report',
             '--dataset-version', str(version_file), '--dataset-version-sha256', version_pin or sha256(version_bytes),
             '--validation', str(validation or self.validation),
             '--capture', str(capture_file), '--capture-sha256', capture_pin or sha256(capture_bytes),
             '--model-catalog', str(catalog), '--recipe', str(recipe), *extra],
            capture_output=True, env={**os.environ, 'PYTHONHASHSEED': seed}, timeout=10,
        )

    def assert_refused(self, result, error):
        self.assertEqual((result.returncode, result.stdout, result.stderr), (1, b'', error.encode() + b'\n'))

    def test_replay_identical_across_processes(self):
        first = self.replay(seed='42')
        second = self.replay(seed='99')
        self.assertEqual((first.returncode, second.returncode), (0, 0), (first.stderr, second.stderr))
        self.assertEqual(first.stdout, second.stdout)
        report = json.loads(first.stdout)
        self.assertEqual(report['score'], self.capture['base'])
        self.assertEqual(report['dataset']['version_id'], self.version['id'])
        self.assertEqual(report['dataset']['validation_sha256'], self.capture['heldout_sha256'])
        self.assertEqual(report['model']['revision'], json.loads(CATALOG.read_bytes())['revision'])
        self.assertEqual(report['scorer']['recipe_sha256'], self.capture['recipe_sha256'])
        self.assertEqual(report['execution_profile']['seed'], self.capture['configuration']['seed'])
        self.assertEqual(report['execution_profile']['fresh_inference'], 'unverified')
        self.assertNotIn(b'Fixture case', first.stdout)
        self.assertNotIn(b'duration_milliseconds', first.stdout)
        retained = self.root / 'retained.json'
        retained.write_bytes(first.stdout)
        self.assertEqual(self.replay(extra=('--check-report', str(retained))).stdout, first.stdout)
        changed = copy.deepcopy(self.version)
        changed['id'] = str(uuid4())
        self.assert_refused(self.replay(version=changed, extra=('--check-report', str(retained))),
                            'EVALUATION_REPORT_MISMATCH')

    def test_dataset_capture_and_scope_pins(self):
        self.assert_refused(self.replay(version_pin='0' * 64), 'EVALUATION_DATASET_MISMATCH')
        self.assert_refused(self.replay(capture_pin='0' * 64), 'EVALUATION_CAPTURE_MISMATCH')
        other = self.root / 'foreign.jsonl'
        other.write_bytes(self.validation.read_bytes().replace(b'Fixture', b'Changed'))
        self.assert_refused(self.replay(validation=other), 'EVALUATION_DATASET_MISMATCH')
        changed = {**self.version, 'workspace_id': str(uuid4())}
        self.assert_refused(self.replay(version=changed), 'EVALUATION_WORKSPACE_MISMATCH')
        changed = {**self.capture, 'heldout_sha256': 'a' * 64}
        self.assert_refused(self.replay(capture=changed), 'EVALUATION_DATASET_MISMATCH')

    def test_model_scorer_and_profile_mismatches(self):
        catalog = self.root / 'other_catalog.json'
        catalog.write_bytes(compact({**json.loads(CATALOG.read_bytes()), 'revision': 'other-revision'}))
        self.assert_refused(self.replay(catalog=catalog), 'EVALUATION_MODEL_MISMATCH')
        recipe = self.root / 'other_recipe.json'
        recipe.write_bytes(compact({**json.loads(RECIPE.read_bytes()), 'instruction': 'Changed instruction'}))
        self.assert_refused(self.replay(recipe=recipe), 'EVALUATION_SCORER_MISMATCH')
        changed = copy.deepcopy(self.capture)
        changed['configuration']['seed'] += 1
        self.assert_refused(self.replay(capture=changed), 'EVALUATION_PROFILE_MISMATCH')

    def test_case_ids_labels_counts_and_scores(self):
        for field, value in (('source_id', 'other'), ('prompt_sha256', 'a' * 64), ('expected_label', 'billing')):
            with self.subTest(field=field):
                capture = copy.deepcopy(self.capture)
                capture['cases'][0][field] = value
                self.assert_refused(self.replay(capture=capture), 'EVALUATION_CASE_MISMATCH')
        changed = copy.deepcopy(self.capture)
        changed['cases'].pop()
        self.assert_refused(self.replay(capture=changed), 'EVALUATION_CASE_MISMATCH')
        changed = copy.deepcopy(self.capture)
        changed['cases'][1] = changed['cases'][0]
        self.assert_refused(self.replay(capture=changed), 'EVALUATION_CASE_MISMATCH')
        changed = copy.deepcopy(self.capture)
        changed['cases'][0]['base']['predicted_label'] = 'invalid'
        self.assert_refused(self.replay(capture=changed), 'EVALUATION_CASE_MISMATCH')
        changed = copy.deepcopy(self.capture)
        changed['base']['correct'] = 3
        self.assert_refused(self.replay(capture=changed), 'EVALUATION_CAPTURE_SCORE_MISMATCH')


if __name__ == '__main__':
    unittest.main()
