import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

from projectone_data.validation import Limits, Row
from projectone_training.boundary import read_installed_catalog
from projectone_training.catalog import catalog_digest
from projectone_training.data import TrainingError, tokenize_file
from projectone_training.preview import MAXIMUM_SAMPLES, preview_samples

_RUNTIME = Path(__file__).resolve().parents[1]
_CATALOG = read_installed_catalog(_RUNTIME)
_CATALOG_SHA256 = catalog_digest(_CATALOG)
_LIMITS = Limits(65536, 10, 16384, 8192, 512, 5)
_TEST_MODEL = 'OWNSOLID_TEST_QWEN_TOKENIZER_DIRECTORY'


class PreviewRejections(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.model = Path(directory.name).resolve() / 'unavailable'
        self.row = Row(1, 'sample', 'A prompt', 'An answer')

    def assert_refused(self, code, *, samples=None, digest=_CATALOG_SHA256, budget=256):
        with self.assertRaises(TrainingError) as failure:
            preview_samples(self.model, digest, [self.row] if samples is None else samples, budget)
        self.assertEqual(failure.exception.code, code)

    def test_catalog_and_missing_local_assets(self):
        self.assert_refused('TOKENIZER_PREVIEW_CATALOG_MISMATCH', digest='0' * 64)
        self.assert_refused('TOKENIZER_PREVIEW_ASSET_UNAVAILABLE')
        self.model.mkdir()
        self.assert_refused('TOKENIZER_PREVIEW_ASSET_UNAVAILABLE')
        (self.model / 'config.json').write_bytes(b'')
        self.assert_refused('TOKENIZER_PREVIEW_ASSET_CHANGED')

    def test_sample_count_and_utf8_byte_bounds(self):
        for rows in ([], [self.row] * (MAXIMUM_SAMPLES + 1),
                     [Row(1, 'sample', '\u754c' * 11000, 'answer')],
                     [Row(1, 'id', 'p' * 4200, 'c' * 4200)] * MAXIMUM_SAMPLES):
            with self.subTest(rows=len(rows)):
                self.assert_refused('TOKENIZER_PREVIEW_SAMPLE_LIMIT', samples=rows)
        exact = Row(1, 'id', 'p' * 16383, 'c' * 16383)
        self.assert_refused('TOKENIZER_PREVIEW_ASSET_UNAVAILABLE', samples=[exact, exact])

    def test_invalid_canonical_row_and_token_budget(self):
        for row in (Row(0, 'sample', 'prompt', 'answer'),
                    Row(1, 'sample', 'prompt', '\ud800'),
                    Row(1, ' sample ', 'prompt', 'answer')):
            self.assert_refused('TOKENIZER_PREVIEW_SAMPLE_INVALID', samples=[row])
        for budget in (31, 2049, True, '256'):
            with self.subTest(budget=budget):
                self.assert_refused('TOKENIZER_PREVIEW_TOKEN_LIMIT', budget=budget)


@unittest.skipUnless(os.environ.get(_TEST_MODEL), f'set {_TEST_MODEL} to the approved local snapshot')
class ActualTokenizerPreview(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.model = self.root / 'model'
        self.model.mkdir()
        original = Path(os.environ[_TEST_MODEL])
        for name in ('config.json', 'merges.txt', 'tokenizer.json', 'tokenizer_config.json', 'vocab.json'):
            shutil.copyfile(original / name, self.model / name)

    def test_preview_and_worker_match_the_previous_tokenization(self):
        from transformers import AutoTokenizer
        tokenizer = AutoTokenizer.from_pretrained(self.model, local_files_only=True, trust_remote_code=False)
        row = Row(1, 'sample', '  Inspect ' + 'sample ' * 40 + '\u4f60\u597d\n',
                  '  Answer \U0001f642 with punctuation.\n')
        messages = [{'role': 'user', 'content': row.prompt}]
        options = dict(add_generation_prompt=True, enable_thinking=False, return_dict=False)
        prefix = tokenizer.apply_chat_template(messages, tokenize=True, **options)
        rendered = tokenizer.apply_chat_template(messages, tokenize=False, **options)
        completion = tokenizer.encode(row.completion, add_special_tokens=False) + [tokenizer.eos_token_id]
        total = len(prefix) + len(completion)
        path = self.root / 'legacy.jsonl'
        raw = (json.dumps(row.value()) + '\n').encode('utf-8')
        path.write_bytes(raw)
        sha256 = hashlib.sha256(raw).hexdigest()
        training = tokenize_file(path, sha256, tokenizer, _LIMITS, total, 'train')
        self.assertEqual(training[0], {'input_ids': prefix + completion, 'attention_mask': [1] * total,
                                       'labels': [-100] * len(prefix) + completion})
        self.assertEqual(training.provenance, [{'line_number': row.line_number, 'content_sha256': row.content_hash(),
                                                'prompt_tokens': len(prefix), 'completion_tokens': len(completion)}])
        with self.assertRaises(TrainingError) as failure:
            tokenize_file(path, sha256, tokenizer, _LIMITS, total - 1, 'train')
        self.assertEqual((failure.exception.code, failure.exception.partition, failure.exception.line_number),
                         ('TRAINING_SEQUENCE_TOO_LONG', 'train', 1))

        # The extra local template must never replace the approved embedded template.
        (self.model / 'chat_template.jinja').write_text('unexpected template', encoding='utf-8')
        for budget, section in ((32, 'prompt'), (len(prefix), 'completion'), (total - 1, 'eos'), (total, None)):
            with self.subTest(budget=budget):
                result = preview_samples(self.model, _CATALOG_SHA256, [row], budget)
                self.assertEqual(result.model_catalog_sha256, _CATALOG_SHA256)
                self.assertEqual(result.maximum_tokens, budget)
                preview, = result.samples
                self.assertEqual((preview.line_number, preview.content_sha256), (1, row.content_hash()))
                self.assertEqual((preview.rendered_prompt, preview.completion, preview.eos),
                                 (rendered, row.completion, tokenizer.eos_token))
                self.assertEqual((preview.prompt_tokens, preview.completion_tokens, preview.total_tokens),
                                 (len(prefix), len(completion), total))
                self.assertEqual(preview.first_excess_token_index, budget if section else None)
                self.assertEqual(preview.first_excess_section, section)

        reserved = Row(1, 'sample', 'Prompt', tokenizer.eos_token)
        with self.assertRaises(TrainingError) as failure:
            preview_samples(self.model, _CATALOG_SHA256, [reserved], total)
        self.assertEqual((failure.exception.code, failure.exception.line_number), ('TRAINING_RESERVED_TOKEN', 1))

    def test_verified_assets_and_package_versions_fail_closed(self):
        row = Row(1, 'sample', 'Prompt', 'Answer')
        with patch('projectone_training.preview.importlib.metadata.version', return_value='0.0.0'):
            with self.assertRaises(TrainingError) as failure:
                preview_samples(self.model, _CATALOG_SHA256, [row], 256)
        self.assertEqual(failure.exception.code, 'TOKENIZER_PREVIEW_RUNTIME_UNAVAILABLE')

        config = self.model / 'config.json'
        raw = config.read_bytes()
        config.write_bytes(bytes([raw[0] ^ 1]) + raw[1:])
        with self.assertRaises(TrainingError) as failure:
            preview_samples(self.model, _CATALOG_SHA256, [row], 256)
        self.assertEqual(failure.exception.code, 'TOKENIZER_PREVIEW_ASSET_CHANGED')

        target = self.root / 'outside.json'
        target.write_bytes(raw)
        config.unlink()
        config.symlink_to(target)
        with self.assertRaises(TrainingError) as failure:
            preview_samples(self.model, _CATALOG_SHA256, [row], 256)
        self.assertEqual(failure.exception.code, 'TOKENIZER_PREVIEW_ASSET_UNAVAILABLE')
