import errno
import json
import os
from dataclasses import asdict
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

from projectone_training import checkpoints
from projectone_training.configuration import Configuration
from projectone_training.configuration_generated import CONTRACT
from projectone_training.data import TrainingError


class CheckpointTest(unittest.TestCase):
    def setUp(self):
        temporary = TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name).resolve() / 'checkpoint-2'
        self.directory.mkdir(mode=0o700)
        self.receipt = self.directory / 'projectone-checkpoint.json'
        defaults = {name: field['default'] for name, field in CONTRACT['configuration']['properties'].items()}
        self.configuration = Configuration(**{**defaults, 'device': 'cpu_verification', 'checkpoint_steps': 2})
        self.bindings = {
            'configuration': asdict(self.configuration),
            'configuration_sha256': self.configuration.digest(),
            'model_repository': 'ownsolid/synthetic-test',
            'scope': {'job_id': 'fixture-job'},
        }
        for name in ['adapter_model.safetensors', 'optimizer.pt', 'scheduler.pt', 'rng_state.pth']:
            (self.directory / name).write_bytes(b'checkpoint fixture')
        (self.directory / 'trainer_state.json').write_text('{"global_step":2}', encoding='utf-8')
        adapter = {
            'peft_type': 'LORA', 'task_type': 'CAUSAL_LM',
            'base_model_name_or_path': self.bindings['model_repository'],
            'target_modules': CONTRACT['recipe']['modules'],
            'r': self.configuration.rank, 'lora_alpha': self.configuration.alpha,
            'lora_dropout': float(self.configuration.dropout), 'bias': 'none',
        }
        (self.directory / 'adapter_config.json').write_text(json.dumps(adapter), encoding='utf-8')

    def seal(self, maximum_bytes=8192, maximum_files=16):
        return checkpoints.seal_checkpoint(self.directory, self.bindings, 2, maximum_bytes, maximum_files)

    def verify(self, receipt, bindings=None):
        reference = checkpoints.CheckpointReference(self.directory, receipt['receipt_sha256'])
        return checkpoints.verify_checkpoint(reference, bindings or self.bindings, self.configuration, 8192, 16)

    def test_seal_syncs_parent_before_publication_and_verifies(self):
        with patch.object(checkpoints, 'sync_directory', wraps=checkpoints.sync_directory) as sync:
            receipt = self.seal()
        self.assertEqual([call.args[0] for call in sync.call_args_list], [self.directory.parent, self.directory])
        self.assertEqual(self.receipt.stat().st_mode & 0o777, 0o600)
        seal, trainer = self.verify(receipt)
        self.assertEqual(seal['global_step'], 2)
        self.assertEqual(trainer['global_step'], 2)
        self.assertEqual([file['path'] for file in seal['files']], sorted(file['path'] for file in seal['files']))

    def test_missing_or_empty_optimizer_cannot_be_sealed(self):
        optimizer = self.directory / 'optimizer.pt'
        optimizer.unlink()
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INCOMPLETE$'):
            self.seal()
        optimizer.touch()
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INCOMPLETE$'):
            self.seal()
        self.assertFalse(self.receipt.exists())

    def test_invalid_trainer_state_cannot_be_sealed(self):
        trainer = self.directory / 'trainer_state.json'
        for raw, code in [
            ('{"global_step":1}', 'INCOMPLETE'),
            ('{"global_step":2.0}', 'INCOMPLETE'),
            ('{"global_step":1,"global_step":2}', 'INVALID'),
        ]:
            with self.subTest(raw=raw):
                trainer.write_text(raw, encoding='utf-8')
                with self.assertRaisesRegex(TrainingError, f'^TRAINING_CHECKPOINT_{code}$'):
                    self.seal()
                self.assertFalse(self.receipt.exists())

    def test_metadata_limit_is_checked_before_hashing(self):
        ceiling = CONTRACT['wire_limits']['maximum_checkpoint_metadata_bytes']
        with (self.directory / 'trainer_state.json').open('r+b') as trainer:
            trainer.truncate(ceiling + 1)
        with patch.object(checkpoints, 'file_digest', side_effect=AssertionError('unexpected hash')):
            with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INVALID$'):
                self.seal(maximum_bytes=ceiling + 8192)
        self.assertFalse(self.receipt.exists())

    def test_extra_nested_and_linked_files_cannot_be_sealed(self):
        extra = self.directory / 'extra'
        extra.mkdir()
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INVALID$'):
            self.seal()
        extra.rmdir()
        extra.write_bytes(b'extra')
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INVALID$'):
            self.seal()
        extra.unlink()
        optimizer = self.directory / 'optimizer.pt'
        optimizer.unlink()
        scheduler = self.directory / 'scheduler.pt'
        optimizer.symlink_to(scheduler)
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INVALID$'):
            self.seal()
        optimizer.unlink()
        os.link(scheduler, optimizer)
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INVALID$'):
            self.seal()
        self.assertFalse(self.receipt.exists())

    def test_limits_are_enforced_before_hashing(self):
        with patch.object(checkpoints, 'file_digest', side_effect=AssertionError('unexpected hash')):
            for limits in [{'maximum_bytes': 1}, {'maximum_files': 5}]:
                with self.subTest(limits=limits), self.assertRaisesRegex(TrainingError, '^TRAINING_OUTPUT_LIMIT$'):
                    self.seal(**limits)
        self.assertFalse(self.receipt.exists())

    def test_resume_rejects_incompatible_bindings_and_same_size_tampering(self):
        receipt = self.seal()
        other_job = {**self.bindings, 'scope': {'job_id': 'another-job'}}
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_INCOMPATIBLE$'):
            self.verify(receipt, other_job)
        optimizer = self.directory / 'optimizer.pt'
        optimizer.write_bytes(b'x' * optimizer.stat().st_size)
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_CHANGED$'):
            self.verify(receipt)

    def test_duplicate_seal_preserves_original(self):
        receipt = self.seal()
        with self.assertRaisesRegex(TrainingError, '^TRAINING_CHECKPOINT_CONFLICT$'):
            self.seal()
        self.verify(receipt)

    def test_failed_publication_cleans_temporary_file_and_can_retry(self):
        with patch.object(checkpoints.os, 'link', side_effect=OSError(errno.ENOSPC, 'fixture')):
            with self.assertRaises(OSError) as failed:
                self.seal()
        self.assertEqual(failed.exception.errno, errno.ENOSPC)
        self.assertFalse(self.receipt.exists())
        self.assertEqual(list(self.directory.glob('.pending-*')), [])
        self.verify(self.seal())
