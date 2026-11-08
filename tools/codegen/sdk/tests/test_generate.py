"""Regression checks for the SDK's public contract projection."""

import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, METHODS, PUBLIC, UNLISTED, generate, project_contract


class ProjectionTests(unittest.TestCase):
    def test_preserves_public_operations_and_parameters(self):
        document = {
            "openapi": "3.1.1",
            "info": {"title": "Example", "version": "1"},
            "paths": {
                "/records/{id}": {
                    "parameters": [{"$ref": "#/components/parameters/ID"}],
                    "get": {
                        "x-audiences": ["admin", "portal"],
                        "responses": {"200": {"$ref": "#/components/responses/Record"}},
                    },
                    "post": {"x-audiences": ["agent"]},
                },
                "/gateway": {"post": {"x-audiences": ["gateway"]}},
                "/v1/models": {"get": {"x-audiences": ["model-gateway"]}},
            },
            "components": {
                "parameters": {"ID": {"in": "path", "name": "id", "required": True}},
                "responses": {"Record": {"$ref": "#/components/schemas/Record"}},
                "schemas": {
                    "Record": {"properties": {"parent": {"$ref": "#/components/schemas/Record"}}},
                    "Unused": {"items": False},
                },
            },
        }
        original = copy.deepcopy(document)
        projected = project_contract(document)
        self.assertEqual(document, original)
        self.assertEqual(set(projected["paths"]), {"/records/{id}", "/v1/models"})
        self.assertEqual(set(projected["paths"]["/records/{id}"]), {"parameters", "get"})
        self.assertEqual(set(projected["components"]["schemas"]), {"Record"})
        self.assertIn("ID", projected["components"]["parameters"])

    def test_external_ref_refused_before_tools_run(self):
        document = {
            "paths": {
                "/records": {
                    "get": {
                        "x-audiences": ["admin"],
                        "responses": {"200": {"$ref": "https://example.invalid/record.json"}},
                    }
                }
            }
        }
        with self.assertRaisesRegex(ValueError, "unsupported reference"):
            project_contract(document)

    def test_projected_contracts_keep_public_operation_set(self):
        def operations(paths):
            return {
                (path, method)
                for path, item in paths.items()
                for method, op in item.items()
                if method in METHODS and set(op["x-audiences"]) & PUBLIC
            }

        sources = list(CONTRACTS.glob("*/openapi.json"))
        self.assertTrue(sources)
        for source in sources:
            if source.parent.name in UNLISTED:
                continue
            with self.subTest(domain=source.parent.name):
                original = json.loads(source.read_text(encoding="utf-8"))
                projected = project_contract(original)
                actual = {
                    (path, method)
                    for path, item in projected["paths"].items()
                    for method in item
                    if method in METHODS
                }
                self.assertEqual(actual, operations(original["paths"]))

    def test_existing_output_is_preserved(self):
        with tempfile.TemporaryDirectory() as temp:
            destination = Path(temp) / "existing"
            destination.mkdir()
            marker = destination / "owned"
            marker.write_text("keep", encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "already exists"):
                generate(destination, "unused", "unused")
            self.assertEqual(marker.read_text(encoding="utf-8"), "keep")


if __name__ == "__main__":
    unittest.main()
