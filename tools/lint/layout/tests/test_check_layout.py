"""Unit tests for the repository layout checker."""
import importlib.util
import sys
import unittest
from pathlib import Path

_SPEC = importlib.util.spec_from_file_location(
    "check_layout", Path(__file__).resolve().parent.parent / "check_layout.py"
)
check_layout = importlib.util.module_from_spec(_SPEC)
sys.modules["check_layout"] = check_layout
_SPEC.loader.exec_module(check_layout)


RULES = check_layout.load_rules()


def run(paths, contents=None):
    contents = contents or {}
    return check_layout.check_paths(paths, lambda path: contents.get(path, ""), RULES)


class PlacementTest(unittest.TestCase):
    def test_accepts_defined_layout(self):
        paths = [
            "README.md", "AGENTS.md", "Makefile", ".gitignore",
            "services/control/cmd/control/main.go",
            "web/apps/console/src/routes/index.tsx",
            "web/package.json",
            "docs/decisions/0001-repository-layout.md",
            "deploy/compose/.env.example",
            "pkg/license/verify.go",
            "runtimes/training/src/ownsolid_training/engine.py",
        ]
        self.assertEqual(run(paths), [])

    def test_rejects_unknown_root_file(self):
        self.assertTrue(run(["notes.txt"])[0].startswith("notes.txt: file not allowed"))

    def test_rejects_unknown_top_level_directory(self):
        self.assertIn("top-level directory 'scripts'", run(["scripts/build.sh"])[0])

    def test_rejects_env_file_outside_compose(self):
        self.assertIn("environment files", run(["services/control/config/.env.example"])[0])

    def test_rejects_undefined_subdirectory(self):
        self.assertIn("'deploy/aws' is not a defined subdirectory", run(["deploy/aws/main.tf"])[0])

    def test_rejects_loose_file_in_top_directory(self):
        self.assertIn("place files inside", run(["deploy/install.sh"])[0])

    def test_rejects_bad_decision_record_name(self):
        self.assertIn("NNNN-kebab-title.md", run(["docs/decisions/layout.md"])[0])


class NamingTest(unittest.TestCase):
    def test_rejects_uppercase_segment(self):
        self.assertIn("must be lowercase", run(["services/control/internal/Models/a.go"])[0])

    def test_accepts_conventional_names(self):
        self.assertEqual(run(["deploy/images/control/Dockerfile", "services/gateway/Cargo.toml"]), [])


class LanguageTest(unittest.TestCase):
    def test_rejects_non_english_letters(self):
        errors = run(["docs/product/scope.md"], {"docs/product/scope.md": "Models\n\u6a21\u578b\n"})
        self.assertIn(":2: non-English letter", errors[0])

    def test_allows_locales_directories(self):
        path = "web/packages/i18n/locales/zh-cn/messages.json"
        self.assertEqual(run([path], {path: "{\"save\": \"\u4fdd\u5b58\"}"}), [])

    def test_exempts_third_party_names_and_language(self):
        path = "services/control/third_party/httpsnoop/LICENSE.txt"
        self.assertEqual(run([path], {path: "Geisend\u00f6rfer"}), [])

    def test_allows_symbols_such_as_box_drawing(self):
        self.assertEqual(run(["docs/architecture/overview.md"], {"docs/architecture/overview.md": "\u251c\u2500\u2500 api \u2192 web"}), [])


if __name__ == "__main__":
    unittest.main()
