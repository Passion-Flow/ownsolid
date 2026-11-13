"""Keep source JSON from silently losing public SDK operations."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from generate import generate, read_contract


def operation(name):
    return json.dumps({"operationId": name, "x-audiences": ["admin"], "responses": {}})


class ContractJSONTests(unittest.TestCase):
    def refused(self, text, expected):
        with tempfile.TemporaryDirectory() as scratch:
            source = Path(scratch) / "openapi.json"
            source.write_text(text, encoding="utf-8")
            with self.assertRaisesRegex(ValueError, expected) as caught:
                read_contract(source)
            self.assertIn(str(source), str(caught.exception))
            return str(caught.exception)

    def test_duplicate_paths_cannot_hide_an_operation(self):
        text = '{"paths":{"/lost":{"get":' + operation("lostRead") + '}},"paths":{"/kept":{"post":' + operation("keptWrite") + '}}}'
        self.refused(text, 'duplicate JSON member "paths"')

    def test_duplicate_methods_cannot_change_operation_identity(self):
        text = '{"paths":{"/resources":{"get":' + operation("firstRead") + ',"get":' + operation("secondRead") + '}}}'
        error = self.refused(text, 'duplicate JSON member "get"')
        self.assertNotIn("firstRead", error)
        self.assertNotIn("secondRead", error)

    def test_escaped_equivalent_schema_names_are_duplicates(self):
        text = r'{"paths":{},"components":{"schemas":{"Item":{"properties":{"id":{"type":"string"},"\u0069d":{"type":"integer"}}}}}}'
        self.refused(text, 'duplicate JSON member "id"')

    def test_alias_owner_uses_the_same_unambiguous_reader(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            nodes, components = root / "nodes", root / "components"
            nodes.mkdir()
            components.mkdir()
            source, owner = nodes / "openapi.json", components / "openapi.json"
            source.write_text(json.dumps({"paths": {"/node": {
                "$ref": "../components/openapi.json#/paths/~1node",
            }}}))
            owner.write_text('{"paths":{"/node":{"get":{"operationId":"first","operationId":"second","x-audiences":["admin"]}}}}')
            with self.assertRaisesRegex(ValueError, 'duplicate JSON member "operationId"') as caught:
                read_contract(source)
            self.assertIn(str(owner), str(caught.exception))

    def test_same_names_in_different_objects_and_array_values_are_allowed(self):
        value = {"paths": {}, "components": {"schemas": {
            "First": {"properties": {"id": {"type": "string"}}},
            "Second": {"properties": {"id": {"type": "integer"}}},
        }}, "x-values": ["id", "id", {"id": 1}, {"id": 2}]}
        with tempfile.TemporaryDirectory() as scratch:
            source = Path(scratch) / "openapi.json"
            source.write_text(json.dumps(value))
            document, aliases = read_contract(source)
        self.assertEqual(document, value)
        self.assertEqual(aliases, set())

    def test_non_object_documents_have_an_actionable_error(self):
        for value in ([], None, True, 42, "text"):
            with self.subTest(value=value):
                self.refused(json.dumps(value), "OpenAPI contract must be a JSON object")

    def test_invalid_json_reports_the_file_and_location(self):
        error = self.refused('{"paths":}', "invalid JSON")
        self.assertIn("line 1 column", error)

    def test_non_finite_numbers_are_not_portable_contract_input(self):
        for token in ("NaN", "Infinity", "-Infinity"):
            with self.subTest(token=token):
                self.refused('{"paths":{},"x-bound":' + token + '}', "non-finite JSON number")

    def test_duplicate_error_does_not_include_member_values(self):
        error = self.refused('{"paths":{},"x-example":"private-first","x-example":"private-second"}', "duplicate JSON member")
        self.assertNotIn("private-first", error)
        self.assertNotIn("private-second", error)

    def test_ambiguous_input_publishes_nothing_and_runs_no_generator(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            domain = root / "contracts" / "example"
            domain.mkdir(parents=True)
            (domain / "openapi.json").write_text('{"paths":{},"paths":{}}')
            output = root / "output"
            with patch("generate.CONTRACTS", domain.parent), patch("generate.subprocess.run") as run:
                with self.assertRaisesRegex(ValueError, "duplicate JSON member"):
                    generate(output, "/unused/go-generator", "/unused/python-generator")
                run.assert_not_called()
            self.assertFalse(output.exists())
            self.assertEqual(list(root.glob(".ownsolid-sdk-*")), [])

    def test_inventory_command_refuses_ambiguity_without_an_incomplete_report(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            domain = root / "example"
            domain.mkdir()
            (domain / "openapi.json").write_text('{"paths":{},"paths":{}}')
            result = subprocess.run([sys.executable, str(SDK / "audit.py"), "--contracts", str(root)], capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout, "")
            self.assertIn('duplicate JSON member "paths"', result.stderr)


if __name__ == "__main__":
    unittest.main()
