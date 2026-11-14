import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("frontend_metadata", Path(__file__).with_name("frontend_metadata.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class MetadataBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.mask = os.umask(0o077)
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.cache = self.root / "cache"
        self.cache.mkdir(mode=0o700)
        self.lock = self.root / "pnpm-lock.yaml"
        self.lock.write_text("---\nlockfileVersion: '9.0'\npackages:\n  '@scope/pkg@1.0.0':\n    resolution: {}\n  '@scope/pkg@2.0.0':\n    resolution: {}\nsnapshots: {}\n")
        self.package = self.root / "package.json"
        self.package.write_text('{"packageManager":"pnpm@12.11.2"}')
        self.relative = f"v12/metadata-full/{module.REGISTRY_DIRECTORY}/@scope/pkg.jsonl"
        self.metadata = self.cache / self.relative
        self.metadata.parent.mkdir(mode=0o700, parents=True)
        self.metadata.write_bytes(b'{"public_fixture":true}\n')
        self.output = self.root / "metadata.tar.gz"

    def tearDown(self):
        self.temporary.cleanup()
        os.umask(self.mask)

    def produce(self):
        return module.pack(self.cache, self.lock, self.package, self.output, 1024**2)

    def test_real_roundtrip_excludes_auth_and_verified_success_state(self):
        (self.cache / "lockfile-verified.jsonl").write_text("must not bypass checks")
        (self.cache / "auth.ini").write_text("synthetic excluded authentication fixture")
        result = self.produce()
        with tarfile.open(self.output) as archive:
            self.assertEqual(set(archive.getnames()), {"inventory.json", "pnpm-cache/" + self.relative})
        target = self.root / "staged"
        staged = module.extract(self.output, result["sha256"], target, 1024**2)
        self.assertEqual(staged["files"], 1)
        self.assertEqual((target / self.relative).read_bytes(), self.metadata.read_bytes())
        self.assertEqual((target / self.relative).stat().st_mtime_ns, self.metadata.stat().st_mtime_ns)

    def test_symlink_metadata_cannot_capture_another_file(self):
        self.metadata.unlink()
        other = self.root / "unrelated-fixture"
        other.write_text("not registry metadata")
        self.metadata.symlink_to(other)
        with self.assertRaisesRegex(ValueError, "EXACT_REGULAR"):
            self.produce()
        self.assertFalse(self.output.exists())

    def test_occupied_output_and_destination_are_preserved(self):
        result = self.produce()
        before = self.output.read_bytes()
        with self.assertRaisesRegex(ValueError, "OUTPUT_OCCUPIED"):
            self.produce()
        self.assertEqual(self.output.read_bytes(), before)
        target = self.root / "staged"
        target.mkdir(mode=0o700)
        with self.assertRaisesRegex(ValueError, "DESTINATION_OCCUPIED"):
            module.extract(self.output, result["sha256"], target, 1024**2)

    def test_traversal_rejected_before_destination_creation(self):
        self.produce()
        with tarfile.open(self.output) as archive:
            inventory = json.load(archive.extractfile("inventory.json"))
        inventory["files"][0]["path"] = "v12/metadata-full/../outside.jsonl"
        malicious = self.root / "malicious.tar.gz"
        with tarfile.open(malicious, "w:gz") as archive:
            for name, data in [("inventory.json", json.dumps(inventory).encode()),
                               ("pnpm-cache/" + inventory["files"][0]["path"], self.metadata.read_bytes())]:
                item = tarfile.TarInfo(name)
                item.mode, item.size = 0o600, len(data)
                archive.addfile(item, io.BytesIO(data))
        target = self.root / "staged"
        with self.assertRaisesRegex(ValueError, "NAMESPACE"):
            module.extract(malicious, module.digest(malicious), target, 1024**2)
        self.assertFalse(target.exists())

    def test_oversized_member_header_is_refused_without_payload_read(self):
        malicious = self.root / "oversized.tar.gz"
        import gzip
        item = tarfile.TarInfo("pnpm-cache/oversized")
        item.mode, item.size = 0o600, module.MAX_ENTRY_BYTES + 1
        with gzip.open(malicious, "wb") as stream:
            stream.write(item.tobuf())
        target = self.root / "staged"
        with self.assertRaisesRegex(ValueError, "ARCHIVE_MEMBERS"):
            module.extract(malicious, module.digest(malicious), target, 1024**2)
        self.assertFalse(target.exists())


if __name__ == "__main__":
    unittest.main()
