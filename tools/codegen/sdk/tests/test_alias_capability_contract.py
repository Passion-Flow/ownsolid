"""Keep alias capability evidence confined to authorized public reads."""

import copy
import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, METHODS, project_contract


class AliasCapabilityContractTests(unittest.TestCase):
    def setUp(self):
        self.document = json.loads((CONTRACTS / "distribution/openapi.json").read_text())
        self.schemas = self.document["components"]["schemas"]

    def test_read_extends_the_complete_alias_without_changing_write_fields(self):
        read = copy.deepcopy(self.schemas["ModelAliasRead"])
        self.assertNotIn("capabilities", read["required"])
        capability = read["properties"].pop("capabilities")
        self.assertEqual(capability["$ref"], "#/components/schemas/ModelAliasReadCapabilities")
        self.assertEqual(read, self.schemas["ModelAlias"])
        self.assertEqual(
            self.schemas["ModelAliasResponse"]["properties"]["data"],
            {"$ref": "#/components/schemas/ModelAlias"},
        )
        self.assertFalse(self.schemas["ModelAliasReadCapabilities"]["additionalProperties"])
        self.assertEqual(
            set(self.schemas["ModelAliasReadCapabilities"]["required"]),
            {"text_generation_verified", "context_token_ceiling"},
        )

    def test_only_the_four_existing_read_operations_reach_capabilities(self):
        expected = {
            "adminListModelAlias", "adminGetModelAlias",
            "portalListModelAlias", "portalGetModelAlias",
        }

        def reaches(value, seen=None):
            seen = set() if seen is None else seen
            if isinstance(value, list):
                return any(reaches(item, seen) for item in value)
            if not isinstance(value, dict):
                return False
            ref = value.get("$ref")
            if ref == "#/components/schemas/ModelAliasRead":
                return True
            if ref and ref not in seen:
                seen.add(ref)
                parts = ref.split("/")
                if len(parts) == 4 and parts[:3] == ["#", "components", "schemas"]:
                    if reaches(self.schemas[parts[3]], seen):
                        return True
            return any(reaches(item, seen) for item in value.values())

        observed = set()
        for item in self.document["paths"].values():
            for method, operation in item.items():
                if method not in METHODS:
                    continue
                if reaches(operation.get("responses", {})):
                    self.assertEqual(method, "get")
                    observed.add(operation["operationId"])
                self.assertFalse(reaches(operation.get("requestBody", {})))
                self.assertFalse(reaches(operation.get("parameters", [])))
        self.assertEqual(observed, expected)

    def test_public_projection_retains_the_optional_closed_evidence(self):
        projected = project_contract(self.document)["components"]["schemas"]
        self.assertEqual(projected["ModelAliasRead"], self.schemas["ModelAliasRead"])
        self.assertEqual(
            projected["ModelAliasReadCapabilities"],
            self.schemas["ModelAliasReadCapabilities"],
        )
