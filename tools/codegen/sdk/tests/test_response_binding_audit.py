"""Review removed response declarations even without named or closed models."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from audit import compare_response_media_types, inventory


def contract():
    return {"paths": {"/download": {"get": {
        "operationId": "download", "x-audiences": ["admin", "portal"],
        "responses": {
            "200": {"description": "Data", "content": {
                "application/json": {"schema": {"type": "string"}}, "text/plain": {},
            }},
            "204": {"description": "Empty"},
        },
    }}}}


def write(root, value):
    path = root / "sample/openapi.json"
    path.parent.mkdir(parents=True)
    path.write_text(json.dumps(value), encoding="utf-8")


def reports(before, after):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        write(root / "before", before)
        write(root / "after", after)
        return inventory(root / "before"), inventory(root / "after")


class ResponseBindingAuditTests(unittest.TestCase):
    def test_inline_media_removal_requires_review_for_each_preserved_audience(self):
        before, after = contract(), contract()
        del after["paths"]["/download"]["get"]["responses"]["200"]["content"]["application/json"]
        findings = compare_response_media_types(*reports(before, after))
        self.assertEqual([x["audience"] for x in findings], ["admin", "portal"])
        self.assertEqual({x["code"] for x in findings}, {"PUBLIC_RESPONSE_BINDING_REMOVED"})
        self.assertEqual(findings[0]["response_status"], "200")
        self.assertEqual(findings[0]["previous_media_types"], ["application/json", "text/plain"])
        self.assertEqual(findings[0]["current_media_types"], ["text/plain"])

    def test_no_content_status_removal_is_distinct_from_a_retained_empty_content(self):
        before, after = contract(), contract()
        after["paths"]["/download"]["get"]["responses"]["204"]["content"] = {}
        self.assertEqual(compare_response_media_types(*reports(before, after)), [])
        del after["paths"]["/download"]["get"]["responses"]["204"]
        findings = compare_response_media_types(*reports(before, after))
        self.assertEqual(len(findings), 2)
        self.assertEqual(findings[0]["previous_media_types"], [])
        self.assertIsNone(findings[0]["current_media_types"])

    def test_omitted_schema_media_types_are_recorded(self):
        before, after = contract(), contract()
        del after["paths"]["/download"]["get"]["responses"]["200"]["content"]["text/plain"]
        findings = compare_response_media_types(*reports(before, after))
        self.assertEqual(findings[0]["current_media_types"], ["application/json"])

    def test_shared_response_resolution_and_reference_renaming_keep_the_same_media(self):
        before, after = contract(), contract()
        operation = after["paths"]["/download"]["get"]
        after["components"] = {"responses": {"Read": operation["responses"]["200"]}}
        operation["responses"]["200"] = {"$ref": "#/components/responses/Read"}
        self.assertEqual(compare_response_media_types(*reports(before, after)), [])
        del after["components"]["responses"]["Read"]["content"]["application/json"]
        self.assertEqual(len(compare_response_media_types(*reports(before, after))), 2)

    def test_default_does_not_silence_removal_of_an_explicit_status(self):
        before, after = contract(), contract()
        operation = after["paths"]["/download"]["get"]
        operation["responses"]["default"] = operation["responses"].pop("200")
        findings = compare_response_media_types(*reports(before, after))
        self.assertEqual([x["response_status"] for x in findings], ["200", "200"])

    def test_additions_annotations_order_and_self_comparison_are_inert(self):
        before, after = contract(), contract()
        response = after["paths"]["/download"]["get"]["responses"]["200"]
        response["description"] = "Updated documentation"
        response["content"] = dict(reversed(list(response["content"].items())))
        response["content"]["application/octet-stream"] = {"schema": False}
        after["paths"]["/download"]["get"]["responses"]["409"] = {"description": "Conflict"}
        after["paths"]["/new"] = {"get": {"operationId": "newRead", "x-audiences": ["admin"],
                                         "responses": {"200": {"description": "New"}}}}
        baseline, current = reports(before, after)
        self.assertEqual(compare_response_media_types(baseline, current), [])
        self.assertEqual(compare_response_media_types(current, current), [])

    def test_identity_and_audience_boundaries_are_shared_with_existing_reviews(self):
        before, after = contract(), contract()
        operation = after["paths"]["/download"]["get"]
        del operation["responses"]["204"]
        operation["x-audiences"] = ["admin"]
        self.assertEqual([x["audience"] for x in compare_response_media_types(*reports(before, after))], ["admin"])
        operation["operationId"] = "newDownload"
        self.assertEqual(compare_response_media_types(*reports(before, after)), [])
        operation["operationId"] = "download"
        after["paths"]["/other"] = after["paths"].pop("/download")
        self.assertEqual(compare_response_media_types(*reports(before, after)), [])

    def test_real_cli_reports_binding_removal_and_exit_one(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            before, after = contract(), contract()
            del after["paths"]["/download"]["get"]["responses"]["200"]["content"]["application/json"]
            write(root / "before", before)
            write(root / "after", after)
            result = subprocess.run([sys.executable, str(SDK / "audit.py"), "--contracts", str(root / "after"),
                                     "--baseline", str(root / "before")], capture_output=True, text=True,
                                    timeout=5, check=False)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertEqual([x["code"] for x in json.loads(result.stdout)["compatibility_findings"]],
                             ["PUBLIC_RESPONSE_BINDING_REMOVED"] * 2)
