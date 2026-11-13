"""Review closed response fields without presenting a general Schema safety proof."""

import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from audit import compare_response_objects, inventory, response_surface


def contract():
    return {
        "paths": {"/models/{id}": {"get": {
            "operationId": "readModel", "x-audiences": ["admin", "portal"],
            "responses": {"200": {"description": "Model", "content": {
                "application/json": {"schema": {"$ref": "#/components/schemas/Model"}},
            }}},
        }}},
        "components": {"schemas": {"Model": {
            "type": "object", "additionalProperties": False,
            "properties": {"id": {"type": "string"}, "name": {"type": "string"}},
            "required": ["id", "name"],
        }}},
    }


def compare(before, after):
    with tempfile.TemporaryDirectory() as scratch:
        reports = []
        for name, document in (("before", before), ("after", after)):
            root = Path(scratch) / name
            domain = root / "models"
            domain.mkdir(parents=True)
            (domain / "openapi.json").write_text(json.dumps(document))
            reports.append(inventory(root))
        return compare_response_objects(*reports)


class ResponseObjectAuditTests(unittest.TestCase):
    def test_new_optional_field_in_same_closed_model_requires_each_audience_review(self):
        before, after = contract(), contract()
        after["components"]["schemas"]["Model"]["properties"]["family_id"] = {"type": "string"}
        findings = compare(before, after)
        self.assertEqual([item["audience"] for item in findings], ["admin", "portal"])
        self.assertEqual({item["code"] for item in findings}, {"PUBLIC_RESPONSE_OBJECT_SHAPE_CHANGED"})
        self.assertEqual(findings[0]["schema_location"], "#/components/schemas/Model")
        self.assertEqual(findings[0]["previous_shape"]["properties"], ["id", "name"])
        self.assertEqual(findings[0]["current_shape"]["properties"], ["family_id", "id", "name"])
        self.assertEqual(findings[0]["current_shape"]["required"], ["id", "name"])

    def test_property_required_and_closure_changes_are_review_signals_in_both_directions(self):
        for kind in ("property", "required", "closure"):
            with self.subTest(kind=kind):
                before, after = contract(), contract()
                model = after["components"]["schemas"]["Model"]
                if kind == "property":
                    del model["properties"]["name"]
                    model["required"].remove("name")
                elif kind == "required":
                    model["required"] = ["id"]
                else:
                    del model["additionalProperties"]
                self.assertEqual(len(compare(before, after)), 2)
                self.assertEqual(len(compare(after, before)), 2)

    def test_inline_array_items_and_escaped_property_locations(self):
        before = contract()
        operation = before["paths"]["/models/{id}"]["get"]
        operation["responses"]["200"]["content"]["application/json"]["schema"] = {
            "type": "array", "items": {"type": "object", "properties": {
                "a/b~": {"type": "object", "additionalProperties": False, "properties": {}},
            }},
        }
        after = copy.deepcopy(before)
        after["paths"]["/models/{id}"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]["items"]["properties"]["a/b~"]["properties"]["extra"] = {}
        findings = compare(before, after)
        self.assertEqual([item["schema_location"] for item in findings],
                         ["#/items/properties/a~1b~0", "#/items/properties/a~1b~0"])

    def test_recursive_escaped_refs_terminate_and_keep_response_object_ref_bindings(self):
        value = contract()
        model = value["components"]["schemas"].pop("Model")
        reference = "#/components/schemas/Model~1Read~0"
        value["components"]["schemas"]["Model/Read~"] = model
        model["properties"]["next"] = {"$ref": reference}
        operation = value["paths"]["/models/{id}"]["get"]
        response = operation["responses"]["200"]
        response["content"]["application/json"]["schema"] = {"$ref": reference}
        value["components"]["responses"] = {"Read": response}
        operation["responses"]["200"] = {"$ref": "#/components/responses/Read"}
        names, shapes = response_surface(value, operation)
        self.assertEqual(names, {"200 application/json": [reference]})
        self.assertEqual(list(shapes["200 application/json"]), [reference])
        self.assertEqual(compare(value, copy.deepcopy(value)), [])

    def test_annotations_order_open_objects_and_value_constraints_are_outside_shape_review(self):
        before, after = contract(), contract()
        model = after["components"]["schemas"]["Model"]
        model["required"].reverse()
        model["properties"] = dict(reversed(list(model["properties"].items())))
        model["description"] = "Changed documentation"
        model["examples"] = [{"$ref": "https://example.invalid/literal"}]
        model["properties"]["name"]["maxLength"] = 1
        self.assertEqual(compare(before, after), [])
        before["components"]["schemas"]["Model"]["additionalProperties"] = True
        model["additionalProperties"] = True
        model["properties"]["new"] = {}
        self.assertEqual(compare(before, after), [])

    def test_removed_response_body_requires_review_and_new_body_does_not(self):
        before, after = contract(), contract()
        response = after["paths"]["/models/{id}"]["get"]["responses"]["200"]
        response["content"] = {"application/vnd.example+json": response["content"]["application/json"]}
        findings = compare(before, after)
        self.assertEqual([item["response"] for item in findings], ["200 application/json"] * 2)
        self.assertTrue(all(item["current_shape"] is None for item in findings))
        after["paths"]["/models/{id}"]["get"]["responses"]["200"] = copy.deepcopy(
            before["paths"]["/models/{id}"]["get"]["responses"]["200"])
        after["paths"]["/models/{id}"]["get"]["responses"]["201"] = response
        self.assertEqual(compare(before, after), [])

    def test_renamed_removed_and_added_operations_keep_operation_identity_boundary(self):
        before, after = contract(), contract()
        after["components"]["schemas"]["Model"]["properties"]["extra"] = {}
        operation = after["paths"]["/models/{id}"]["get"]
        operation["operationId"] = "newReadModel"
        self.assertEqual(compare(before, after), [])
        operation["operationId"] = "readModel"
        operation["x-audiences"] = ["admin"]
        self.assertEqual([item["audience"] for item in compare(before, after)], ["admin"])
        after["paths"] = {"/other": {"get": operation}}
        self.assertEqual(compare(before, after), [])

    def test_command_exit_one_contains_shape_review_without_model_rename(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            for name in ("before", "after"):
                domain = root / name / "models"
                domain.mkdir(parents=True)
                value = contract()
                if name == "after":
                    value["components"]["schemas"]["Model"]["properties"]["family_id"] = {}
                (domain / "openapi.json").write_text(json.dumps(value))
            result = subprocess.run(
                [sys.executable, str(SDK / "audit.py"), "--contracts", str(root / "after"),
                 "--baseline", str(root / "before")], capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 1, result.stderr)
            findings = json.loads(result.stdout)["compatibility_findings"]
            self.assertEqual([item["code"] for item in findings], ["PUBLIC_RESPONSE_OBJECT_SHAPE_CHANGED"] * 2)

    def test_malformed_object_shape_fails_before_a_compatibility_report(self):
        for name, invalid in (("properties", []), ("required", "id"), ("required", [1])):
            with self.subTest(name=name, invalid=invalid):
                value = contract()
                value["components"]["schemas"]["Model"][name] = invalid
                with self.assertRaisesRegex(ValueError, "response object"):
                    response_surface(value, value["paths"]["/models/{id}"]["get"])
