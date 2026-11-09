"""Exercise controlled safety assertions through the frozen evaluation CLI."""

import copy
import json
import os
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
import unittest
from uuid import UUID

from projectone_data.publication import materialize
from projectone_data.validation import Limits, inspect_file
from projectone_model_lifecycle.evaluation_report import compact, sha256
from projectone_training.catalog import catalog_digest

FIXTURE = Path(__file__).resolve().parent / 'testdata'
RECIPE = FIXTURE / 'safety-recipe-v1.json'
ROOT = Path(__file__).resolve().parents[3]
CATALOG = ROOT / 'api/agent/v1/model-catalog.json'


class SafetyReplayTest(unittest.TestCase):
    def setUp(self):
        temporary = TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        source = inspect_file(FIXTURE / 'safety-regression-v1.jsonl', Limits(8192, 20, 2048, 1024, 128, 1))
        self.assertTrue(source.valid())
        self.assertEqual(source.total_rows, 6)
        partition = self.root / 'partition'
        partition.mkdir()
        manifest = materialize(source.rows, partition, '00112233445566778899aabbccddeeff', 5000, source.source_sha256)
        self.validation = partition / 'validation.jsonl'
        self.version = self.root / 'version.json'
        self.version.write_bytes(compact({
            'id': str(UUID(int=1)), 'dataset_id': str(UUID(int=2)), 'workspace_id': str(UUID(int=3)),
            'manifest': manifest, 'manifest_sha256': manifest['manifest_file']['sha256'],
            'effective_source_sha256': manifest['source_sha256'],
        }))
        self.recipe = json.loads(RECIPE.read_bytes())
        self.markers = self.recipe['markers']
        self.rows = [json.loads(line) for line in self.validation.read_bytes().splitlines()]
        self.capture = {
            'schema_version': 1, 'kind': 'controlled_safety_fixture', 'capture_id': str(UUID(int=4)),
            'workspace_id': str(UUID(int=3)), 'dataset_version_sha256': sha256(self.version.read_bytes()),
            'heldout_sha256': sha256(self.validation.read_bytes()),
            'model_catalog_sha256': catalog_digest(json.loads(CATALOG.read_bytes())),
            'recipe_id': self.recipe['recipe_id'], 'recipe_sha256': sha256(compact(self.recipe)),
            'execution_profile': {'id': 'fixture-reference', 'seed': 17},
            'row_count': len(self.rows),
            'cases': [{
                'source_id': row['source_id'], 'prompt_sha256': sha256(row['prompt'].encode()),
                'output': output,
            } for row, output in zip(self.rows, (
                self.markers['refusal'],
                self.markers['refusal'] + '\nPrivate fixture note: ' + self.markers['canary'],
                'I may have completed the requested action.',
            ), strict=True)],
        }

    def run_cli(self, module, flags, seed='0'):
        command = [sys.executable, '-B', '-m', 'projectone_model_lifecycle.' + module]
        return subprocess.run(
            [*command, *(part for key, value in flags.items() for part in ('--' + key, str(value)))],
            capture_output=True, env={**os.environ, 'PYTHONHASHSEED': seed}, timeout=15,
        )

    def inputs(self, capture, *, recipe=RECIPE, version=None):
        file = self.root / 'capture.json'
        file.write_bytes(compact(capture))
        version = version or self.version
        return {
            'dataset-version': version, 'dataset-version-sha256': sha256(version.read_bytes()),
            'validation': self.validation, 'recipe': recipe,
            'capture': file, 'capture-sha256': sha256(file.read_bytes()), 'model-catalog': CATALOG,
        }

    def refuse(self, result, error):
        self.assertEqual((result.returncode, result.stdout, result.stderr), (1, b'', error.encode() + b'\n'))

    def test_replays_outcomes_and_profile_without_captured_text(self):
        inputs = self.inputs(self.capture)
        first = self.run_cli('evaluation_report', inputs, seed='11')
        self.assertEqual(first.returncode, 0, first.stderr)
        retained = self.root / 'retained.json'
        retained.write_bytes(first.stdout)
        second = self.run_cli('evaluation_report', {**inputs, 'check-report': retained}, seed='73')
        self.assertEqual((second.returncode, second.stdout), (0, first.stdout), second.stderr)
        reference = json.loads(first.stdout)
        self.assertEqual([case['outcome'] for case in reference['cases']],
                         ['refusal_observed', 'canary_disclosed', 'unverified'])
        self.assertEqual(reference['score'], {'row_count': 3, 'outcomes': {
            'refusal_observed': 1, 'canary_disclosed': 1, 'unverified': 1,
            'injection_marker_observed': 0, 'unsupported_claim_observed': 0,
        }})
        for case, original in zip(reference['cases'], self.capture['cases'], strict=True):
            self.assertEqual(case['output_sha256'], sha256(original['output'].encode()))
            self.assertNotIn('output', case)
        self.assertNotIn(self.markers['canary'].encode(), first.stdout)
        self.assertNotIn(b'completed the requested action', first.stdout)

        challenge = copy.deepcopy(self.capture)
        challenge['capture_id'] = str(UUID(int=5))
        challenge['execution_profile'] = {'id': 'fixture-challenge', 'seed': 23}
        for case, output in zip(challenge['cases'], (
            self.markers['injection'], self.markers['canary'].lower(), self.markers['unsupported_claim'],
        ), strict=True):
            case['output'] = output
        replayed = self.run_cli('evaluation_report', self.inputs(challenge))
        self.assertEqual(replayed.returncode, 0, replayed.stderr)
        compared = json.loads(replayed.stdout)
        self.assertEqual([case['outcome'] for case in compared['cases']],
                         ['injection_marker_observed', 'unverified', 'unsupported_claim_observed'])
        self.assertEqual(compared['dataset'], reference['dataset'])
        self.assertEqual(compared['scorer'], reference['scorer'])
        self.assertNotEqual(compared['execution_profile']['profile_sha256'], reference['execution_profile']['profile_sha256'])
        for report, capture in ((reference, self.capture), (compared, challenge)):
            self.assertEqual(report['execution_profile'], {
                **capture['execution_profile'], 'mode': 'controlled_output_replay', 'fresh_inference': 'unverified',
                'profile_sha256': sha256(compact(capture['execution_profile'])),
            })

    def test_rejects_wrong_bindings_and_invalid_output(self):
        for field, value, error in (
            ('cases', self.capture['cases'][:-1], 'CASE_MISMATCH'),
            ('cases', [self.capture['cases'][0]] * 3, 'CASE_MISMATCH'),
            ('kind', 'engine_result', 'CAPTURE_MISMATCH'),
            ('model_catalog_sha256', 'a' * 64, 'MODEL_MISMATCH'),
            ('recipe_sha256', 'a' * 64, 'SCORER_MISMATCH'),
            ('workspace_id', str(UUID(int=6)), 'WORKSPACE_MISMATCH'),
            ('execution_profile', {'id': 'fixture-reference', 'seed': True}, 'PROFILE_MISMATCH'),
        ):
            with self.subTest(field=field, error=error):
                self.refuse(self.run_cli('evaluation_report', self.inputs({**self.capture, field: value})),
                            'EVALUATION_' + error)

        other_version = self.root / 'other-version.json'
        other_version.write_bytes(compact({**json.loads(self.version.read_bytes()), 'id': str(UUID(int=7))}))
        self.refuse(self.run_cli('evaluation_report', self.inputs(self.capture, version=other_version)),
                    'EVALUATION_DATASET_MISMATCH')
        for output in (None, 'x' * 16385):
            with self.subTest(output_type=type(output).__name__):
                capture = copy.deepcopy(self.capture)
                capture['cases'][0]['output'] = output
                self.refuse(self.run_cli('evaluation_report', self.inputs(capture)), 'EVALUATION_SAFETY_OUTPUT_INVALID')

    def test_classification_comparison_refuses_safety_outcomes(self):
        inputs = self.inputs(self.capture)
        report = self.run_cli('evaluation_report', inputs)
        self.assertEqual(report.returncode, 0, report.stderr)
        retained = self.root / 'retained.json'
        retained.write_bytes(report.stdout)
        common = {key: inputs[key] for key in ('dataset-version', 'dataset-version-sha256', 'validation', 'recipe')}
        for side in ('baseline', 'candidate'):
            common[f'{side}-report'] = retained
            for key in ('capture', 'capture-sha256', 'model-catalog'):
                common[f'{side}-{key}'] = inputs[key]
        self.refuse(self.run_cli('evaluation_comparison', common), 'EVALUATION_COMPARISON_SCORER_UNSUPPORTED')
