"""Verify legacy module metadata without accepting changed or missing cache inputs."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from verify_tool_modules import verify_cached_modules


class ToolModuleVerificationTests(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.cache = Path(self.scratch.name)
        self.root = self.cache / "example.com/legacy@v1.0.0"
        self.root.mkdir(parents=True)
        (self.root / "LICENSE").write_text("fixture license\n")
        (self.root / "legacy.go").write_text("package legacy\n")
        self.mod = self.cache / "cache/download/example.com/legacy/@v/v1.0.0.mod"
        self.mod.parent.mkdir(parents=True)
        self.mod.write_text("module example.com/legacy\n")
        self.pin = {"module": "example.com/legacy", "version": "v1.0.0",
                    "sum": "h1:BkgU+tnYXjo8KB/18tlNZNGo4BQiYpHLHd3msGqdQGg=",
                    "go_mod_sum": "h1:pS2KLshur9YBlehNARWQ3af1njEt+NcOfNS6ku9j6Ng="}

    def test_legacy_source_without_go_mod_checks_the_canonical_cached_mod(self):
        report = verify_cached_modules(self.cache, [self.pin])[0]
        self.assertFalse(report["source_go_mod_present"])
        self.assertEqual(report["verified_source_h1"], self.pin["sum"])
        self.assertEqual(report["verified_go_mod_h1"], self.pin["go_mod_sum"])
        self.assertEqual(report["cached_go_mod"], str(self.mod))
        self.assertIn("LICENSE", report["notices_sha256"])
        self.assertFalse((self.root / "go.mod").exists())

    def test_modern_source_and_cache_metadata_must_agree(self):
        (self.root / "go.mod").write_bytes(self.mod.read_bytes())
        self.pin["sum"] = "h1:Q1RvydQHkQWjmilcSLSvvGTaPfXOgdSe0kAoAW3uCm0="
        self.assertTrue(verify_cached_modules(self.cache, [self.pin])[0]["source_go_mod_present"])
        (self.root / "go.mod").write_text("module example.com/different\n")
        self.pin["sum"] = "h1:Pad+IWYp8ndIiqgPeBa08tdy8fxKSx3BSK8WhbH9cik="
        with self.assertRaisesRegex(ValueError, "source go.mod differs"):
            verify_cached_modules(self.cache, [self.pin])

    def test_changed_source_is_not_excused_by_a_valid_cached_mod(self):
        (self.root / "legacy.go").write_text("package changed\n")
        with self.assertRaisesRegex(ValueError, "source checksum"):
            verify_cached_modules(self.cache, [self.pin])

    def test_missing_or_changed_cached_mod_is_not_excused_by_valid_source(self):
        self.mod.write_text("module example.com/changed\n")
        with self.assertRaisesRegex(ValueError, "cached .mod checksum"):
            verify_cached_modules(self.cache, [self.pin])
        self.mod.unlink()
        with self.assertRaisesRegex(ValueError, "cached .mod is missing"):
            verify_cached_modules(self.cache, [self.pin])

    def test_symlinks_are_refused_for_source_and_cached_metadata(self):
        (self.root / "alias").symlink_to(self.root / "legacy.go")
        with self.assertRaisesRegex(ValueError, "symlink"):
            verify_cached_modules(self.cache, [self.pin])
        (self.root / "alias").unlink()
        self.mod.unlink()
        self.mod.symlink_to(self.root / "legacy.go")
        with self.assertRaisesRegex(ValueError, "unsafe"):
            verify_cached_modules(self.cache, [self.pin])

    def test_unsafe_names_and_empty_pins_fail_before_reading_cache_inputs(self):
        for field, value in [("module", "../outside"), ("version", "../../outside")]:
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "unsupported"):
                verify_cached_modules(self.cache, [{**self.pin, field: value}])
        with self.assertRaisesRegex(ValueError, "nonempty"):
            verify_cached_modules(self.cache, [])

    def test_cli_failure_emits_no_partial_success_report(self):
        pins = self.cache / "pins.json"
        pins.write_text(json.dumps({"modules": [self.pin]}))
        self.mod.unlink()
        result = subprocess.run([sys.executable, str(SDK / "verify_tool_modules.py"),
                                 "--cache-root", str(self.cache), "--pins", str(pins)],
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("canonical cached .mod is missing", result.stderr)
