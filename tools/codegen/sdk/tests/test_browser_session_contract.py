"""Keep the public device session contract scoped and bodyless on revocation."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


LIST_PATH = "/api/v1/me/sessions"
DELETE_PATH = LIST_PATH + "/{session_id}"


class BrowserSessionContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        source = CONTRACTS / "identity" / "openapi.json"
        cls.contract = project_contract(json.loads(source.read_text(encoding="utf-8")))
        cls.list_op = cls.contract["paths"][LIST_PATH]["get"]
        cls.revoke_op = cls.contract["paths"][DELETE_PATH]["delete"]

    def test_both_operations_require_one_entrance_session(self):
        for operation in (self.list_op, self.revoke_op):
            with self.subTest(operation=operation["operationId"]):
                self.assertEqual(set(operation["x-audiences"]), {"portal", "admin"})
                self.assertEqual(operation["security"], [{"PortalSession": []}, {"AdminSession": []}])
        self.assertEqual(self.contract["paths"]["/api/v1/me/session"]["get"]["operationId"], "getSessionStatus")

    def test_list_has_only_live_session_metadata_and_opaque_pagination(self):
        params = {param["name"]: param for param in self.list_op["parameters"]}
        self.assertEqual(set(params), {"limit", "cursor", "X-Ownsolid-Background"})
        self.assertEqual(params["limit"]["schema"], {"type": "integer", "minimum": 1, "maximum": 100})
        self.assertEqual(params["cursor"]["schema"]["maxLength"], 512)
        self.assertEqual(params["cursor"]["schema"]["minLength"], 1)
        self.assertEqual(params["X-Ownsolid-Background"]["schema"]["enum"], ["true"])
        schemas = self.contract["components"]["schemas"]
        page = schemas["BrowserSessionList"]
        self.assertFalse(page["additionalProperties"])
        self.assertEqual(set(page["required"]), {"items", "next_cursor"})
        self.assertEqual(page["properties"]["items"]["maxItems"], 100)
        self.assertEqual(page["properties"]["next_cursor"]["anyOf"],
                         [params["cursor"]["schema"], {"type": "null"}])
        session = schemas["BrowserSession"]
        self.assertFalse(session["additionalProperties"])
        fields = {"id", "current", "created_at", "last_seen_at", "absolute_expires_at", "authentication_method"}
        self.assertEqual(set(session["required"]), fields)
        self.assertEqual(set(session["properties"]), fields)
        self.assertEqual(session["properties"]["authentication_method"]["enum"], ["password", "sso"])

    def test_revocation_requires_origin_and_csrf_and_has_no_body(self):
        operation = self.revoke_op
        params = {param["name"]: param for param in operation["parameters"]}
        self.assertEqual(set(params), {"session_id", "Origin", "X-CSRF-Token"})
        for name, location in (("session_id", "path"), ("Origin", "header"), ("X-CSRF-Token", "header")):
            self.assertTrue(params[name]["required"])
            self.assertEqual(params[name]["in"], location)
        self.assertEqual(params["session_id"]["schema"], self.contract["components"]["schemas"]["BrowserSession"]["properties"]["id"])
        self.assertNotIn("requestBody", operation)
        self.assertNotIn("content", operation["responses"]["204"])
        self.assertIn("Set-Cookie", operation["responses"]["204"]["headers"])
        self.assertEqual(operation["x-error-codes"]["404"], ["RESOURCE_NOT_FOUND"])
        self.assertTrue({"ORIGIN_REJECTED", "CSRF_INVALID"} <= set(operation["x-error-codes"]["403"]))

    def test_storage_errors_keep_the_shared_problem_contract_unchanged(self):
        schemas = self.contract["components"]["schemas"]
        new = schemas["BrowserSessionStorageProblem"]
        codes = new["properties"]["code"]["enum"]
        self.assertEqual(new["properties"]["status"], {"type": "integer", "const": 503})
        self.assertIn("STORAGE_COMMIT_ROLLED_BACK", codes)
        self.assertNotIn("STORAGE_COMMIT_ROLLED_BACK", schemas["Problem"]["properties"]["code"]["enum"])
        for operation in (self.list_op, self.revoke_op):
            self.assertEqual(operation["x-error-codes"]["503"], codes)
            self.assertEqual(operation["x-error-codes"]["409"], ["STORAGE_COMMIT_OUTCOME_UNKNOWN"])
