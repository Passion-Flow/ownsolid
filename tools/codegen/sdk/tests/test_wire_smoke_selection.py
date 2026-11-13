"""Keep targeted Python qualification from restarting already qualified Go tests."""

from contextlib import ExitStack
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import wire_smoke


CHECKS = ("exercise_imports", "exercise", "exercise_streaming", "exercise_monitoring_problems",
          "exercise_audit_reads_python", "exercise_audit_reads_python_protocol", "exercise_audit_reads_go")


class WireSmokeSelectionTests(unittest.TestCase):
    def test_python_only_keeps_all_python_checks_and_never_selects_go(self):
        sdk = Path("fixture-sdk")
        with ExitStack() as stack:
            checks = {name: stack.enter_context(patch.object(wire_smoke, name)) for name in CHECKS}
            commands = stack.enter_context(patch.object(wire_smoke.subprocess, "run"))
            wire_smoke.run_checks(sdk, python_only=True, expected_domains=28)
        checks["exercise_imports"].assert_called_once_with(sdk, 28)
        checks["exercise"].assert_called_once_with(sdk, include_go=False)
        for name in CHECKS[2:-1]:
            self.assertEqual(checks[name].call_count, 1, name)
        checks["exercise_audit_reads_go"].assert_not_called()
        commands.assert_not_called()

    def test_default_selection_retains_the_full_wire_entry(self):
        sdk = Path("fixture-sdk")
        with ExitStack() as stack:
            checks = {name: stack.enter_context(patch.object(wire_smoke, name)) for name in CHECKS}
            wire_smoke.run_checks(sdk)
        checks["exercise"].assert_called_once_with(sdk, include_go=True)
        for check in checks.values():
            self.assertEqual(check.call_count, 1)

    def test_incomplete_domain_scope_fails_before_importing_any_sdk(self):
        with tempfile.TemporaryDirectory() as scratch:
            sdk = Path(scratch)
            package = sdk / "gen/python/ownsolid_sdk/identity"
            package.mkdir(parents=True)
            (package / "client.py").write_text("")
            with patch.object(wire_smoke.importlib, "import_module") as imports:
                with self.assertRaisesRegex(AssertionError, "expected 28 SDK domains, found 1"):
                    wire_smoke.exercise_imports(sdk, expected_domains=28)
            imports.assert_not_called()

    def test_another_installed_sdk_cannot_satisfy_the_import_check(self):
        with tempfile.TemporaryDirectory() as scratch:
            sdk = Path(scratch)
            python = (sdk / "gen/python").resolve()
            package = python / "ownsolid_sdk/identity"
            package.mkdir(parents=True)
            (package / "client.py").write_text("")
            outside = SimpleNamespace(__file__=str(sdk / "other/client.py"))
            try:
                with patch.object(wire_smoke.importlib, "import_module", return_value=outside):
                    with self.assertRaisesRegex(AssertionError, "came from another SDK"):
                        wire_smoke.exercise_imports(sdk, expected_domains=1)
            finally:
                sys.path.remove(str(python))
