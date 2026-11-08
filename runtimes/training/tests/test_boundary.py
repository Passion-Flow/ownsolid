import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest

from projectone_training.boundary import read_installed_catalog, worker_source_digest
from projectone_training.catalog import catalog_digest
from projectone_training.data import TrainingError


class InstalledCatalogTest(unittest.TestCase):
    def setUp(self):
        temporary = TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.release = Path(temporary.name)
        self.runtime = self.release / 'runtimes/training'
        self.runtime.mkdir(parents=True)
        self.current = self.release / 'api/agent/v1/model-catalog.json'
        self.legacy = self.release / 'contracts/agent/v1/model-catalog.json'
        for path in (self.current, self.legacy):
            path.parent.mkdir(parents=True)

    def test_loads_the_repository_catalog(self):
        runtime = Path(__file__).resolve().parents[1]
        expected = runtime.parents[1] / 'api/agent/v1/model-catalog.json'
        self.assertEqual(
            catalog_digest(read_installed_catalog(runtime)),
            catalog_digest(json.loads(expected.read_text(encoding='utf-8'))),
        )

    def test_uses_legacy_catalog_only_when_current_file_is_absent(self):
        self.legacy.write_text('{"repository":"fixture/legacy"}', encoding='utf-8')
        self.assertEqual(read_installed_catalog(self.runtime)['repository'], 'fixture/legacy')
        self.current.write_text('{"repository":"fixture/current"}', encoding='utf-8')
        self.assertEqual(read_installed_catalog(self.runtime)['repository'], 'fixture/current')

    def test_refuses_an_invalid_current_file_despite_a_legacy_file(self):
        self.legacy.write_text('{"repository":"fixture/legacy"}', encoding='utf-8')
        self.current.symlink_to(self.legacy)
        with self.assertRaisesRegex(TrainingError, '^TRAINING_MODEL_INVALID$'):
            read_installed_catalog(self.runtime)

    def test_reports_a_missing_catalog(self):
        with self.assertRaisesRegex(TrainingError, '^TRAINING_MODEL_INVALID$'):
            read_installed_catalog(self.runtime)


class WorkerSourceDigestTest(unittest.TestCase):
    def test_hashes_runtime_and_data_sources_independently_of_tests(self):
        with TemporaryDirectory() as temporary:
            runtimes = Path(temporary)
            training = runtimes / 'training'
            data = runtimes / 'data-processing'
            training.mkdir()
            data.mkdir()
            (training / 'train.py').write_text('# worker', encoding='utf-8')
            processor = data / 'process.py'
            processor.write_text('# processor', encoding='utf-8')
            expected = worker_source_digest(training)
            for runtime in (training, data):
                tests = runtime / 'tests'
                tests.mkdir()
                (tests / 'test_fixture.py').write_text('# test', encoding='utf-8')
            self.assertEqual(worker_source_digest(training), expected)
            processor.write_text('# updated processor', encoding='utf-8')
            self.assertNotEqual(worker_source_digest(training), expected)
