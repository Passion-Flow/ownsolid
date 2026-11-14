"""Verify the optional Portal conversation search contract."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


class ApplicationSearchContractTests(unittest.TestCase):
    def test_search_is_optional_and_scoped_to_the_existing_portal_list(self):
        source = CONTRACTS / "applications" / "openapi.json"
        contract = project_contract(json.loads(source.read_text(encoding="utf-8")))
        route = "/api/v1/sites/{site_id}/cells/{cell_id}/application-conversations"
        operation = contract["paths"][route]["get"]
        self.assertEqual(operation["operationId"], "ListApplicationConversations")
        self.assertEqual(operation["x-audiences"], ["portal"])
        parameters = {parameter["name"]: parameter for parameter in operation["parameters"]}
        self.assertEqual(set(parameters), {"cursor", "limit", "q", "workspace_id"})
        self.assertTrue(parameters["workspace_id"]["required"])
        self.assertEqual(parameters["q"], {
            "name": "q", "in": "query",
            "schema": {"type": "string", "minLength": 1, "maxLength": 120},
        })
        self.assertEqual(operation["responses"]["200"]["content"]["application/json"]["schema"], {
            "$ref": "#/components/schemas/ApplicationConversationPageResponse",
        })

    def test_portal_application_reads_use_the_declared_session_cookie(self):
        source = CONTRACTS / "applications" / "openapi.json"
        contract = project_contract(json.loads(source.read_text(encoding="utf-8")))
        paths = {
            item["get"]["operationId"]: item["get"] for item in contract["paths"].values() if "get" in item
        }
        for name in ("ListApplications", "ListApplicationConversations"):
            with self.subTest(operation=name):
                self.assertEqual(paths[name]["security"], [{"PortalSession": []}])
        self.assertEqual(contract["components"]["securitySchemes"]["PortalSession"], {
            "type": "apiKey", "in": "cookie", "name": "__Host-portal_session",
        })
