"""Review response model references independently of wire compatibility."""

import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from audit import compare_operations, compare_response_models, inventory, response_models


def document(model="Alias", nested=False):
    alias = {"type": "object", "properties": {"id": {"type": "string"}}}
    schemas = {model: alias}
    if nested:
        schemas["Page"] = {"type": "object", "properties": {
            "items": {"type": "array", "items": {"$ref": "#/components/schemas/" + model}},
        }}
    return {
        "paths": {"/aliases": {"get": {
            "operationId": "readAliases", "x-audiences": ["admin", "portal"],
            "responses": {"200": {"description": "Aliases", "content": {
                "application/json": {"schema": {"$ref": "#/components/schemas/" + ("Page" if nested else model)}},
            }}},
        }}},
        "components": {"schemas": schemas},
    }


def write(root, value):
    domain = root / "distribution"
    domain.mkdir(parents=True)
    (domain / "openapi.json").write_text(json.dumps(value))


class ResponseModelAuditTests(unittest.TestCase):
    def test_direct_and_nested_replacements_require_review_for_each_audience(self):
        for nested in (False, True):
            with self.subTest(nested=nested), tempfile.TemporaryDirectory() as scratch:
                root = Path(scratch)
                before, after = root / "baseline", root / "candidate"
                write(before, document(nested=nested))
                write(after, document("AliasRead", nested=nested))
                baseline, candidate = inventory(before), inventory(after)
                self.assertEqual(compare_operations(baseline, candidate), [])
                findings = compare_response_models(baseline, candidate)
                self.assertEqual([item["audience"] for item in findings], ["admin", "portal"])
                self.assertEqual({item["code"] for item in findings}, {"PUBLIC_RESPONSE_MODELS_CHANGED"})
                previous = ["#/components/schemas/Alias"]
                current = ["#/components/schemas/AliasRead"]
                if nested:
                    previous.append("#/components/schemas/Page")
                    current.append("#/components/schemas/Page")
                self.assertEqual(findings[0]["previous_response_models"], {"200 application/json": previous})
                self.assertEqual(findings[0]["current_response_models"], {"200 application/json": current})
                result = subprocess.run(
                    [sys.executable, str(SDK / "audit.py"), "--contracts", str(after), "--baseline", str(before)],
                    capture_output=True, text=True, check=False,
                )
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertEqual(json.loads(result.stdout)["compatibility_findings"], findings)

    def test_annotations_order_duplicates_and_request_models_are_not_response_changes(self):
        before = document(nested=True)
        after = copy.deepcopy(before)
        model = after["components"]["schemas"]["Alias"]
        model["description"] = "Updated documentation"
        model["examples"] = [{"$ref": "https://example.invalid/literal-data"}]
        model["properties"]["$ref"] = {"type": "string"}
        after["components"]["schemas"]["Page"]["allOf"] = [
            {"$ref": "#/components/schemas/Alias"},
            {"$ref": "#/components/schemas/Alias"},
        ]
        after["paths"]["/aliases"]["get"]["requestBody"] = {
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Request"}}},
        }
        after["components"]["schemas"]["Request"] = {"type": "object"}
        old = response_models(before, before["paths"]["/aliases"]["get"])
        new = response_models(after, after["paths"]["/aliases"]["get"])
        self.assertEqual(new, old)

    def test_recursive_and_escaped_models_with_reusable_response_objects(self):
        value = document("Alias/Read~")
        reference = "#/components/schemas/Alias~1Read~0"
        value["components"]["schemas"]["Alias/Read~"]["properties"]["next"] = {"$ref": reference}
        response = value["paths"]["/aliases"]["get"]["responses"]["200"]
        response["content"]["application/json"]["schema"]["$ref"] = reference
        value["components"]["responses"] = {"Read": response}
        value["paths"]["/aliases"]["get"]["responses"]["200"] = {"$ref": "#/components/responses/Read"}
        self.assertEqual(response_models(value, value["paths"]["/aliases"]["get"]),
                         {"200 application/json": [reference]})

    def test_removed_and_renamed_operations_keep_the_primary_identity_finding(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            before, after = root / "baseline", root / "candidate"
            write(before, document())
            changed = document("AliasRead")
            operation = changed["paths"]["/aliases"]["get"]
            operation["operationId"] = "newName"
            operation["x-audiences"] = ["admin"]
            write(after, changed)
            baseline, candidate = inventory(before), inventory(after)
            self.assertEqual(compare_response_models(baseline, candidate), [])
            self.assertEqual([finding["code"] for finding in compare_operations(baseline, candidate)],
                             ["PUBLIC_OPERATION_ID_CHANGED", "PUBLIC_OPERATION_REMOVED"])

    def test_external_and_missing_response_references_fail_without_fetching(self):
        for reference in ("https://example.invalid/model.json", "#/components/schemas/Missing"):
            with self.subTest(reference=reference), tempfile.TemporaryDirectory() as scratch:
                root = Path(scratch)
                value = document()
                value["paths"]["/aliases"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] = reference
                write(root, value)
                result = subprocess.run(
                    [sys.executable, str(SDK / "audit.py"), "--contracts", str(root)],
                    capture_output=True, text=True, check=False,
                )
                self.assertEqual(result.returncode, 2)
                self.assertIn("response reference", result.stderr)

    def test_response_object_cycles_are_rejected(self):
        value = document()
        value["components"]["responses"] = {"Loop": {"$ref": "#/components/responses/Loop"}}
        value["paths"]["/aliases"]["get"]["responses"]["200"] = {"$ref": "#/components/responses/Loop"}
        with self.assertRaisesRegex(ValueError, "cyclic response object"):
            response_models(value, value["paths"]["/aliases"]["get"])
