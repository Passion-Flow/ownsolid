"""Reject non-schema response targets without weakening compatibility review."""

import copy
import json
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from audit import compare_response_models, inventory, response_surface


def contract(schema):
    return {"paths": {"/sample": {"get": {
        "operationId": "readSample", "x-audiences": ["admin"],
        "responses": {"200": {"description": "Sample", "content": {
            "application/json": {"schema": schema},
        }}},
    }}}, "components": {"schemas": {}}}


def surface(document):
    return response_surface(document, document["paths"]["/sample"]["get"])


def write(root, document):
    path = root / "sample/openapi.json"
    path.parent.mkdir(parents=True)
    path.write_text(json.dumps(document), encoding="utf-8")
    return path


class ResponseSchemaAuditTests(unittest.TestCase):
    def test_invalid_media_schema_roots_fail_without_network(self):
        with patch.object(socket.socket, "connect", side_effect=AssertionError("Unexpected network")) as connect:
            for value in (None, 0, 1.5, "private-fixture-value", [], [{}]):
                with self.subTest(value=value), self.assertRaisesRegex(ValueError, "response schema"):
                    surface(contract(value))
            connect.assert_not_called()

    def test_invalid_reference_targets_are_not_reported_as_named_models(self):
        for value in (None, 0, "private-fixture-value", [], [{}]):
            document = contract({"$ref": "#/components/schemas/Target"})
            document["components"]["schemas"]["Target"] = value
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "response schema"):
                surface(document)

    def test_booleans_and_empty_objects_remain_valid_inline_and_by_reference(self):
        for value in (True, False, {}):
            with self.subTest(value=value):
                self.assertEqual(surface(contract(value)), ({}, {}))
                document = contract({"$ref": "#/components/schemas/Target"})
                document["components"]["schemas"]["Target"] = value
                self.assertEqual(surface(document)[0], {"200 application/json": ["#/components/schemas/Target"]})

    def test_omitted_media_schema_is_valid(self):
        document = contract(None)
        del document["paths"]["/sample"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]
        self.assertEqual(surface(document), ({}, {}))

    def test_shared_response_target_schema_is_checked(self):
        document = contract("private-fixture-value")
        operation = document["paths"]["/sample"]["get"]
        document["components"]["responses"] = {"Shared": operation["responses"]["200"]}
        operation["responses"]["200"] = {"$ref": "#/components/responses/Shared"}
        with self.assertRaisesRegex(ValueError, "response schema"):
            surface(document)

    def test_annotation_payloads_and_unreferenced_components_remain_outside_traversal(self):
        document = contract({"type": "string", "examples": [None, 3, {"$ref": "literal-data"}]})
        document["components"]["schemas"]["Unused"] = "not-reachable"
        self.assertEqual(surface(document), ({}, {}))

    def test_cli_invalid_candidate_or_baseline_has_context_and_no_partial_report(self):
        for invalid_baseline in (False, True):
            with self.subTest(invalid_baseline=invalid_baseline), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                bad = write(root / "invalid", contract("private-fixture-value"))
                write(root / "valid", contract(True))
                command = [sys.executable, str(SDK / "audit.py"), "--contracts",
                           str(root / ("valid" if invalid_baseline else "invalid"))]
                if invalid_baseline:
                    command += ["--baseline", str(root / "invalid")]
                result = subprocess.run(command, capture_output=True, text=True, timeout=5, check=False)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, "")
                self.assertIn(str(bad), result.stderr)
                self.assertIn("GET /sample", result.stderr)
                self.assertNotIn("private-fixture-value", result.stderr)

    def test_explicit_409_remains_a_source_review_even_with_identical_default_problem(self):
        before = contract({"type": "string"})
        before["components"]["schemas"]["Problem"] = {"type": "object"}
        responses = before["paths"]["/sample"]["get"]["responses"]
        responses["default"] = {"description": "Problem", "content": {
            "application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}},
        }}
        after = copy.deepcopy(before)
        after["paths"]["/sample"]["get"]["responses"]["409"] = copy.deepcopy(responses["default"])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write(root / "before", before)
            write(root / "after", after)
            findings = compare_response_models(inventory(root / "before"), inventory(root / "after"))
            self.assertEqual([item["code"] for item in findings], ["PUBLIC_RESPONSE_MODELS_CHANGED"])
            result = subprocess.run([sys.executable, str(SDK / "audit.py"), "--contracts", str(root / "after"),
                                     "--baseline", str(root / "before")], capture_output=True, text=True,
                                    timeout=5, check=False)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(json.loads(result.stdout)["compatibility_findings"], findings)
