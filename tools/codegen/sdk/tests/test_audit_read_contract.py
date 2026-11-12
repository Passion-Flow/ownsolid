"""Keep audited reads explicit without changing their public event projection."""

import json
from pathlib import Path
import re
import sys
import unittest

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from generate import CONTRACTS, project_contract


PATH = "/api/v1/admin/audit-events"


class AuditReadContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = SDK.parents[2]
        cls.raw = json.loads((CONTRACTS / "identity/openapi.json").read_text())
        cls.projected = project_contract(cls.raw)

    def test_commit_unknown_reuses_the_actual_shared_problem_and_status(self):
        registry = (self.root / "services/control/internal/platform/httpserver/problem.go").read_text()
        self.assertRegex(registry, r'"STORAGE_COMMIT_OUTCOME_UNKNOWN"\s*:\s*409')
        for document in (self.raw, self.projected):
            with self.subTest(projected=document is self.projected):
                operation = document["paths"][PATH]["get"]
                self.assertEqual(operation["operationId"], "listAuditEvents")
                self.assertEqual(operation["x-audiences"], ["admin"])
                self.assertEqual(operation["security"], [{"AdminSession": []}])
                self.assertEqual(operation["x-error-codes"]["409"], ["STORAGE_COMMIT_OUTCOME_UNKNOWN"])
                self.assertEqual(operation["responses"]["409"], {"$ref": "#/components/responses/Problem"})
                problem = document["components"]["schemas"]["Problem"]
                self.assertIn("STORAGE_COMMIT_OUTCOME_UNKNOWN", problem["properties"]["code"]["enum"])

    def test_exact_action_publication_does_not_precede_server_admission(self):
        source = (self.root / "services/control/internal/modules/identity/lists.go").read_text()
        match = re.search(r'for _, a := range strings.Fields\("([^"]+)"\)', source)
        self.assertIsNotNone(match, "Review the server's authoritative action catalogue")
        admitted = set(match.group(1).split())
        operation = self.raw["paths"][PATH]["get"]
        action = next(value for value in operation["parameters"] if value.get("name") == "action")
        self.assertLessEqual(set(action["schema"]["enum"]), admitted)
        category = next(value for value in operation["parameters"] if value.get("name") == "category")
        self.assertRegex("audit", category["schema"]["pattern"])

    def test_recovery_is_explicit_and_reaches_the_existing_help_parameter_projection(self):
        for document in (self.raw, self.projected):
            operation = document["paths"][PATH]["get"]
            self.assertIn("Do not retry automatically", operation["description"])
            self.assertIn("new audited read", operation["description"])
            cursor = next(value for value in operation["parameters"]
                          if value.get("$ref") == "#/components/parameters/Cursor")
            self.assertIn("only explicitly", cursor["description"])
            self.assertIn("does not confirm whether the earlier access was recorded", cursor["description"])
            self.assertNotIn("requestBody", operation)
            self.assertFalse(any(value.get("name") == "Idempotency-Key" for value in operation["parameters"]))

    def test_standard_fields_and_verified_assistant_pair_remain_closed(self):
        standard = {
            "id", "occurred_at", "actor_user_id", "actor_email", "subject_user_id",
            "subject_email", "audience", "action", "outcome", "target_type",
            "target_id", "reason", "request_id",
        }
        event = self.raw["components"]["schemas"]["AuditEvent"]
        self.assertEqual(len(standard), 13)
        self.assertFalse(event["additionalProperties"])
        self.assertEqual(set(event["required"]), standard)
        self.assertEqual(set(event["properties"]), standard | {"actor_kind", "actor_id"})
        self.assertEqual(event["dependentRequired"], {"actor_kind": ["actor_id"], "actor_id": ["actor_kind"]})
        for field in ("metadata", "identifier_digest", "tenant_id", "workspace_id"):
            self.assertNotIn(field, event["properties"])

    def test_success_still_has_the_original_noncacheable_page_and_representation_header(self):
        operation = self.raw["paths"][PATH]["get"]
        success = operation["responses"]["200"]
        self.assertEqual(success["content"]["application/json"]["schema"],
                         {"$ref": "#/components/schemas/AuditListResponse"})
        self.assertEqual(success["headers"]["Cache-Control"]["schema"]["const"], "no-store")
        self.assertIn("X-Ownsolid-Audit-Actor", success["headers"]["Vary"]["description"])
        header = next(value for value in operation["parameters"]
                      if value.get("name") == "X-Ownsolid-Audit-Actor")
        self.assertEqual(header["schema"]["enum"], ["true"])
        self.assertFalse(header.get("required", False))
