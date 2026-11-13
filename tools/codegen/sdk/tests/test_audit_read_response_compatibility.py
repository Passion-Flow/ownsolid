"""Preserve audit-read v1 response accessors without disguising the contract diff."""

import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from generate import (CONTRACTS, audit_read_409_compatibility_required,
                      generate_domain, preserve_go_audit_read_default, read_contract)


PARSER = '''func ParseListAuditEventsHTTPResponse(rsp *http.Response) (*ListAuditEventsHTTPResponse, error) {
	response := &ListAuditEventsHTTPResponse{}
	switch {
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && rsp.StatusCode == 409:
		var dest Problem
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.ApplicationproblemJSON409 = &dest
	case strings.Contains(rsp.Header.Get("Content-Type"), "json") && true:
		var dest Problem
		if err := json.Unmarshal(bodyBytes, &dest); err != nil {
			return nil, err
		}
		response.ApplicationproblemJSONDefault = &dest
	}
	switch {
	case rsp.StatusCode == 409:
		var headers ListAuditEventsHTTPResponse409Headers
		response.Headers409 = &headers
	case true:
		var headers ListAuditEventsHTTPResponseDefaultHeaders
		response.HeadersDefault = &headers
	}
	return response, nil
}'''
OTHER = PARSER.replace("ListAuditEvents", "ListOtherEvents")


class AuditReadResponseCompatibilityTests(unittest.TestCase):
    def test_current_contract_uses_the_reviewed_shared_problem(self):
        document, _ = read_contract(CONTRACTS / "identity/openapi.json")
        operation = document["paths"]["/api/v1/admin/audit-events"]["get"]
        self.assertTrue(audit_read_409_compatibility_required(operation))
        self.assertEqual(operation["security"], [{"AdminSession": []}])
        self.assertEqual(operation["x-error-codes"]["409"], ["STORAGE_COMMIT_OUTCOME_UNKNOWN"])

    def test_a_contract_without_explicit_409_needs_no_adaptation(self):
        self.assertFalse(audit_read_409_compatibility_required({"responses": {
            "default": {"$ref": "#/components/responses/Problem"},
        }}))

    def test_changed_response_references_require_a_new_review(self):
        operation = {"responses": {status: {"$ref": "#/components/responses/Problem"}
                                   for status in ("409", "default")}}
        for status in ("409", "default"):
            changed = copy.deepcopy(operation)
            changed["responses"][status] = {"$ref": "#/components/responses/Other"}
            with self.subTest(status=status), self.assertRaisesRegex(ValueError, "409/default"):
                audit_read_409_compatibility_required(changed)
        del operation["responses"]["default"]
        with self.assertRaisesRegex(ValueError, "409/default"):
            audit_read_409_compatibility_required(operation)

    def test_response_reference_changes_are_refused_before_any_generator(self):
        document, _ = read_contract(CONTRACTS / "identity/openapi.json")
        operation = document["paths"]["/api/v1/admin/audit-events"]["get"]
        del operation["responses"]["default"]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "identity/openapi.json"
            source.parent.mkdir()
            source.write_text(json.dumps(document), encoding="utf-8")
            with patch("generate.subprocess.run") as tools:
                with self.assertRaisesRegex(ValueError, "409/default"):
                    generate_domain(source, root / "temp", root / "go", root / "python", "unused-go", "unused-python")
            tools.assert_not_called()
            self.assertFalse((root / "go").exists())
            self.assertFalse((root / "python").exists())

    def test_only_the_audit_parser_gets_legacy_body_and_header_values(self):
        prefix = "package identity\n\n"
        source = prefix + PARSER + "\n\n" + OTHER
        result = preserve_go_audit_read_default(source)
        self.assertTrue(result.startswith(prefix))
        self.assertTrue(result.endswith("\n\n" + OTHER))
        self.assertEqual(result.count("response.ApplicationproblemJSONDefault = response.ApplicationproblemJSON409"), 1)
        self.assertEqual(result.count("ListAuditEventsHTTPResponseDefaultHeaders(*response.Headers409)"), 1)
        self.assertLess(result.index("response.Headers409 = &headers"),
                        result.index("\tif rsp.StatusCode == 409 {"))
        self.assertEqual(preserve_go_audit_read_default(result), result)

    def test_missing_ambiguous_incomplete_or_changed_parsers_fail_closed(self):
        cases = [OTHER, PARSER + "\n" + PARSER, PARSER[:-1],
                 PARSER.replace("var dest Problem", "var dest OtherProblem"),
                 PARSER.replace("rsp.StatusCode == 409:", "rsp.StatusCode == 408:"),
                 PARSER.replace("response.Headers409 = &headers", "response.Headers408 = &headers"),
                 PARSER.replace("var headers ListAuditEventsHTTPResponseDefaultHeaders", "var headers OtherHeaders"),
                 PARSER.replace("return response, nil", "return nil, nil")]
        for index, source in enumerate(cases):
            with self.subTest(index=index), self.assertRaisesRegex(ValueError, "generated"):
                preserve_go_audit_read_default(source)

    def test_changed_existing_adaptation_is_not_patched_a_second_time(self):
        adapted = preserve_go_audit_read_default(PARSER)
        changed = adapted.replace("response.ApplicationproblemJSONDefault = response.ApplicationproblemJSON409",
                                  "response.ApplicationproblemJSONDefault = nil")
        with self.assertRaisesRegex(ValueError, "adaptation changed"):
            preserve_go_audit_read_default(changed)
