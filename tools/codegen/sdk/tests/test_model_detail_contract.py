"""Keep retained dataset lineage on the Admin model detail operation."""

from copy import deepcopy
import json
from pathlib import Path
import re
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


SCHEMA_REF = "#/components/schemas/"
DETAIL_FIELD = "dataset_lineage"


class ModelDetailContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        source = CONTRACTS / "models" / "openapi.json"
        document = json.loads(source.read_text(encoding="utf-8"))
        cls.projected = project_contract(document)
        cls.schemas = cls.projected["components"]["schemas"]

    def test_only_admin_detail_has_the_new_response(self):
        expected = {
            "getAdminModel": "AdminModelDetailResponse",
            "getPortalModel": "ModelVersionResponse",
            "listAdminModels": "ModelListResponse",
            "listPortalModels": "ModelListResponse",
        }
        operations = {
            op["operationId"]: op
            for item in self.projected["paths"].values()
            for op in item.values()
            if isinstance(op, dict) and "operationId" in op
        }
        for name, schema in expected.items():
            with self.subTest(operation=name):
                operation = operations[name]
                response = operation["responses"]["200"]["content"]["application/json"]
                self.assertEqual(response["schema"]["$ref"], SCHEMA_REF + schema)
        self.assertEqual(operations["getAdminModel"]["x-audiences"], ["admin"])
        self.assertEqual(operations["getAdminModel"]["security"], [{"adminSession": []}])
        self.assertIn("MODEL_DERIVED_CATALOG_INVALID", operations["getAdminModel"]["x-error-codes"]["409"])
        self.assertEqual(
            self.schemas["ModelList"]["properties"]["items"]["items"]["$ref"],
            SCHEMA_REF + "ModelVersion",
        )
        detail = deepcopy(self.schemas["AdminModelDetailResponse"])
        self.assertEqual(detail["properties"]["data"]["$ref"], SCHEMA_REF + "AdminModelDetail")
        detail["properties"]["data"]["$ref"] = SCHEMA_REF + "ModelVersion"
        self.assertEqual(detail, self.schemas["ModelVersionResponse"])

    def test_admin_detail_preserves_the_shared_fields(self):
        detail = deepcopy(self.schemas["AdminModelDetail"])
        self.assertNotIn(DETAIL_FIELD, detail["required"])
        lineage = detail["properties"].pop(DETAIL_FIELD)
        self.assertEqual(lineage["$ref"], SCHEMA_REF + "ModelDatasetLineage")
        approved_base, = (rule["then"] for rule in detail["allOf"]
                          if rule["if"]["properties"]["kind"]["const"] == "approved_base")
        self.assertEqual(approved_base.pop("not"), {"required": [DETAIL_FIELD]})
        self.assertEqual(detail, self.schemas["ModelVersion"])

    def test_lineage_requires_two_closed_canonical_references(self):
        lineage = self.schemas["ModelDatasetLineage"]
        self.assertEqual(lineage["type"], "object")
        self.assertFalse(lineage["additionalProperties"])
        self.assertEqual(set(lineage["required"]), {"training", "holdout"})
        self.assertEqual(lineage["properties"], {
            source: {"$ref": SCHEMA_REF + "ModelDatasetVersionReference"}
            for source in ("training", "holdout")
        })

        reference = self.schemas["ModelDatasetVersionReference"]
        self.assertEqual(reference["type"], "object")
        self.assertFalse(reference["additionalProperties"])
        valid = {
            "dataset_id": "9f97261a-935c-461d-a4c7-c1eebcc7fbf9",
            "dataset_version_id": "67c285fd-493f-4ac4-be3e-525d111eec09",
            "manifest_sha256": "f" * 64,
        }
        self.assertEqual(set(reference["required"]), set(valid))
        self.assertEqual(set(reference["properties"]), set(valid))
        for field, sample in valid.items():
            with self.subTest(field=field):
                schema = reference["properties"][field]
                self.assertEqual(schema["type"], "string")
                self.assertEqual(schema["maxLength"], len(sample))
                self.assertIsNotNone(re.fullmatch(schema["pattern"], sample))
                for invalid in (sample.upper(), sample + "\n", sample[1:]):
                    self.assertIsNone(re.fullmatch(schema["pattern"], invalid))
