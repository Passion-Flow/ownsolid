"""Exercise reusable parameter projection through the actual offline Help generator."""

import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[4]


class HelpParameterContractTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="ownsolid-help-parameters-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        codegen = self.root / "tools/codegen"
        codegen.mkdir(parents=True)
        for name in ("web-help.mjs", "openapi-paths.mjs"):
            shutil.copyfile(ROOT / "tools/codegen" / name, codegen / name)
        help_root = self.root / "web/packages/help"
        help_root.mkdir(parents=True)
        (help_root / "node_modules").symlink_to(ROOT / "web/packages/help/node_modules")
        (help_root / "package.json").write_text('{"type":"module"}')
        (help_root / "articles.json").write_text('{"console":[],"portal":[]}')
        translations = help_root / "locales/zh-cn"
        translations.mkdir(parents=True)
        (translations / "api-descriptions.json").write_text("{}")
        domain = self.root / "api/openapi/v1/example"
        domain.mkdir(parents=True)
        self.source = domain / "openapi.json"
        self.contract = {
            "paths": {
                "/example/{id}": {
                    "parameters": [{"$ref": "#/components/parameters/Id"}],
                    "get": {
                        "operationId": "readExample",
                        "x-audiences": ["admin", "portal"],
                        "parameters": [
                            {"$ref": "#/components/parameters/Csrf"},
                            {"$ref": "#/components/parameters/Session"},
                            {"$ref": "#/components/parameters/QueryAlias"},
                        ],
                        "responses": {"200": {}},
                    },
                },
            },
            "components": {
                "parameters": {
                    "Id": {"name": "id", "in": "path", "required": True,
                           "schema": {"type": "string"}},
                    "Csrf": {"name": "X-CSRF-Token", "in": "header", "required": True},
                    "Session": {"name": "session", "in": "cookie"},
                    "QueryAlias": {"$ref": "#/components/parameters/a~1b~0c"},
                    "a/b~c": {"name": "cursor", "in": "query", "schema": {"maxLength": 512}},
                },
            },
        }

    def generate(self):
        self.source.write_text(json.dumps(self.contract))
        return subprocess.run(
            ["node", str(self.root / "tools/codegen/web-help.mjs")],
            cwd=self.root, capture_output=True, text=True, check=False,
        )

    def test_resolves_references_before_hiding_transport_parameters(self):
        result = self.generate()
        self.assertEqual(result.returncode, 0, result.stderr)
        for entrance in ("console", "portal"):
            output = self.root / f"web/packages/help/src/gen/api/{entrance}/example.json"
            operation, = json.loads(output.read_text())["operations"]
            self.assertEqual(operation["parameters"], [
                {"name": "id", "in": "path", "required": True, "schema": {"type": "string"}},
                {"name": "cursor", "in": "query", "required": False, "schema": {"maxLength": 512}},
            ])

    def test_rejects_missing_cyclic_and_external_references(self):
        for reference, expected in (
            ("#/components/parameters/Missing", "missing parameter reference"),
            ("#/components/parameters/QueryAlias", "cyclic parameter reference"),
            ("https://example.invalid/parameters.json#/Csrf", "unsupported parameter reference"),
        ):
            with self.subTest(reference=reference):
                self.contract["components"]["parameters"]["a/b~c"] = {"$ref": reference}
                result = self.generate()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(expected, result.stderr)
                self.assertIn("example: get /example/{id}", result.stderr)

    def test_released_help_only_exposes_named_path_and_query_parameters(self):
        for entrance in ("console", "portal"):
            directory = ROOT / f"web/packages/help/src/gen/api/{entrance}"
            for source in directory.glob("*.json"):
                for operation in json.loads(source.read_text())["operations"]:
                    for parameter in operation["parameters"]:
                        with self.subTest(entrance=entrance, operation=operation["id"]):
                            self.assertTrue(parameter.get("name"))
                            self.assertIn(parameter.get("in"), ("path", "query"))
