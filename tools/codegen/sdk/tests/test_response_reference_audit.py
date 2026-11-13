"""Keep response pointer resolution faithful to local RFC 6901 references."""

import copy
import json
from pathlib import Path
import socket
import sys
import tempfile
import unittest
from unittest.mock import patch

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from audit import compare_response_objects, inventory, response_surface


def document(reference, schemas):
    return {
        "paths": {"/resources": {"get": {
            "operationId": "readResource", "x-audiences": ["admin", "portal"],
            "responses": {"200": {"description": "Resource", "content": {
                "application/json": {"schema": {"$ref": reference}},
            }}},
        }}},
        "components": {"schemas": schemas},
    }


def surface(value):
    return response_surface(value, value["paths"]["/resources"]["get"])


def object_schema():
    return {"type": "object", "additionalProperties": False,
            "properties": {"id": {"type": "string"}}, "required": ["id"]}


class ResponseReferenceAuditTests(unittest.TestCase):
    def test_array_element_ref_preserves_closed_shape_review_for_each_audience(self):
        reference = "#/components/schemas/Envelope/allOf/0"
        before = document(reference, {"Envelope": {"allOf": [object_schema()]}})
        after = copy.deepcopy(before)
        after["components"]["schemas"]["Envelope"]["allOf"][0]["properties"]["extra"] = {}
        with tempfile.TemporaryDirectory() as scratch:
            reports = []
            for name, value in (("before", before), ("after", after)):
                root = Path(scratch) / name
                domain = root / "resources"
                domain.mkdir(parents=True)
                (domain / "openapi.json").write_text(json.dumps(value))
                reports.append(inventory(root))
            findings = compare_response_objects(*reports)
        self.assertEqual([item["audience"] for item in findings], ["admin", "portal"])
        self.assertEqual({item["schema_location"] for item in findings}, {reference})
        self.assertEqual(surface(before)[0], surface(after)[0])

    def test_invalid_array_indices_cannot_use_python_indexing_shortcuts(self):
        for token in ("", "-", "-1", "+0", "00", "01", "0.0", "0e0", " 0", "\u0660", "1", "99999", "9" * 5000):
            with self.subTest(token=token):
                value = document("#/components/schemas/Envelope/allOf/" + token,
                                 {"Envelope": {"allOf": [object_schema()]}})
                with self.assertRaisesRegex(ValueError, "response reference"):
                    surface(value)

    def test_numeric_object_names_remain_valid(self):
        for token in ("0", "01", "-1", "-"):
            with self.subTest(token=token):
                reference = "#/components/schemas/Envelope/properties/" + token
                value = document(reference, {"Envelope": {"properties": {token: object_schema()}}})
                self.assertEqual(surface(value)[1]["200 application/json"][reference]["properties"], ["id"])

    def test_fragment_percent_utf8_and_literal_percent_decode_once(self):
        for reference, name in (
                ("#/components/schemas/GPU%20usage", "GPU usage"),
                ("#%2Fcomponents%2Fschemas%2FGPU%20usage", "GPU usage"),
                ("#/components/schemas/%E6%B8%A9%E5%BA%A6", "\u6e29\u5ea6"),
                ("#/components/schemas/rate%252F", "rate%2F")):
            with self.subTest(reference=reference):
                names, shapes = surface(document(reference, {name: object_schema()}))
                self.assertEqual(names, {"200 application/json": [reference]})
                self.assertEqual(list(shapes["200 application/json"]), [reference])

    def test_tilde_escapes_decode_once_in_the_required_order(self):
        for reference, name in (("#/components/schemas/A~1B~0", "A/B~"),
                                ("#/components/schemas/A~01B", "A~1B")):
            with self.subTest(reference=reference):
                names, shapes = surface(document(reference, {name: object_schema()}))
                self.assertEqual(names["200 application/json"], [reference])
                self.assertEqual(shapes["200 application/json"][reference]["required"], ["id"])

    def test_invalid_encodings_fail_even_when_a_literal_key_exists(self):
        for token in ("bad%", "bad%2", "bad%GG", "%FF", "%C0%AF", "A~", "A~2"):
            with self.subTest(token=token):
                value = document("#/components/schemas/" + token, {token: object_schema()})
                with self.assertRaisesRegex(ValueError, "response reference"):
                    surface(value)

    def test_encoded_shared_response_object_retains_its_body(self):
        reference = "#/components/schemas/Resource"
        value = document(reference, {"Resource": object_schema()})
        expected = surface(value)
        operation = value["paths"]["/resources"]["get"]
        response = operation["responses"]["200"]
        value["components"]["responses"] = {"Read Response": response}
        operation["responses"]["200"] = {"$ref": "#/components/responses/Read%20Response"}
        self.assertEqual(surface(value), expected)

    def test_recursive_encoded_reference_terminates(self):
        reference = "#/components/schemas/Recursive%20Resource"
        model = object_schema()
        model["properties"]["next"] = {"$ref": reference}
        names, shapes = surface(document(reference, {"Recursive Resource": model}))
        self.assertEqual(names["200 application/json"], [reference])
        self.assertEqual(list(shapes["200 application/json"]), [reference])

    def test_missing_external_and_anchor_references_fail_without_network(self):
        with patch.object(socket.socket, "connect", side_effect=AssertionError("Unexpected network")) as connect:
            for reference in ("#/components/schemas/Missing", "https://example.invalid/api.json#/Resource",
                              "../api.json#/Resource", "file:///api.json#/Resource", "#anchor", "#"):
                with self.subTest(reference=reference):
                    with self.assertRaisesRegex(ValueError, "response reference"):
                        surface(document(reference, {}))
            connect.assert_not_called()


if __name__ == "__main__":
    unittest.main()
