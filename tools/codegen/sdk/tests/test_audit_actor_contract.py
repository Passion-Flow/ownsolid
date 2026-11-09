"""Check that the public SDK keeps the audit representation preference."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


class AuditActorContractTests(unittest.TestCase):
    def test_public_projection_keeps_optional_attribution(self):
        source = CONTRACTS / "identity" / "openapi.json"
        document = project_contract(json.loads(source.read_text(encoding="utf-8")))
        operation = document["paths"]["/api/v1/admin/audit-events"]["get"]
        header, = (parameter for parameter in operation["parameters"]
                   if parameter.get("name") == "X-Ownsolid-Audit-Actor")
        self.assertEqual(header["in"], "header")
        self.assertFalse(header.get("required", False))
        self.assertEqual(header["schema"], {"type": "string", "const": "true"})
        self.assertIn("Vary", operation["responses"]["200"]["headers"])
        event = document["components"]["schemas"]["AuditEvent"]
        self.assertFalse(event["additionalProperties"])
        self.assertEqual(event["dependentRequired"], {"actor_kind": ["actor_id"], "actor_id": ["actor_kind"]})
        for field, value in {"actor_kind": "product_assistant", "actor_id": "ownsolid.product-assistant"}.items():
            with self.subTest(field=field):
                self.assertNotIn(field, event["required"])
                self.assertEqual(event["properties"][field]["type"], "string")
                self.assertEqual(event["properties"][field]["const"], value)
        self.assertTrue({"actor_user_id", "actor_email", "subject_user_id", "subject_email"} <= set(event["required"]))
