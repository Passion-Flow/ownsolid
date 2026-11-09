"""Keep dataset publication defaults separate from retained three-way reads."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


LEGACY_FILES = ["samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl"]
TEST_FIELDS = {"test_basis_points", "test_groups", "test_rows"}
TEST_FILE = "test.jsonl"


class DatasetManifestContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        source = CONTRACTS / "datasets" / "openapi.json"
        cls.document = project_contract(json.loads(source.read_text(encoding="utf-8")))
        cls.schemas = cls.document["components"]["schemas"]
        cls.operations = {
            operation["operationId"]: operation
            for path in cls.document["paths"].values()
            for operation in path.values()
            if isinstance(operation, dict) and "operationId" in operation
        }

    def test_legacy_manifest_keeps_the_closed_shape(self):
        manifest = self.schemas["DatasetManifest"]
        self.assertFalse(manifest["additionalProperties"])
        self.assertTrue(TEST_FIELDS.isdisjoint(manifest["required"]))
        self.assertEqual(manifest["properties"]["schema_version"]["enum"], [1, 2])
        conditions = self.version_conditions()
        legacy = conditions[1]
        self.assertEqual(legacy["properties"]["split_algorithm"], {"const": "source-union-sha256.v1"})
        self.assertEqual(
            legacy["properties"]["files"],
            {
                "maxItems": 5,
                "items": {"properties": {"path": {"enum": LEGACY_FILES}}},
            },
        )
        self.assertEqual({rule["required"][0] for rule in legacy["not"]["anyOf"]}, TEST_FIELDS)
        self.assertEqual(manifest["properties"]["files"]["minItems"], 5)

    def test_three_way_manifest_requires_all_sealed_assets_and_test_counts(self):
        manifest = self.schemas["DatasetManifest"]
        self.assertEqual(manifest["properties"]["files"]["maxItems"], 6)
        three_way = self.version_conditions()[2]
        self.assertEqual(three_way["properties"]["split_algorithm"], {"const": "source-union-sha256.v2"})
        self.assertEqual(set(three_way["required"]), TEST_FIELDS)
        files = three_way["properties"]["files"]
        self.assertEqual(files["minItems"], 6)
        names = [rule["contains"]["properties"]["path"]["const"] for rule in files["allOf"]]
        self.assertCountEqual(names, LEGACY_FILES + [TEST_FILE])
        asset = self.schemas["DatasetAsset"]
        self.assertCountEqual(asset["properties"]["path"]["enum"], names)
        self.assertEqual(
            asset["allOf"],
            [{
                "if": {"properties": {"path": {"const": TEST_FILE}}},
                "then": {"properties": {"size_bytes": {"minimum": 1}}},
            }],
        )

    def test_download_reaches_both_entrances_without_a_new_write_option(self):
        for audience in ("admin", "portal"):
            with self.subTest(audience=audience):
                download = self.operations[audience + "DatasetVersionFile"]
                parameter, = (field for field in download["parameters"] if field["name"] == "asset")
                self.assertCountEqual(
                    parameter["schema"]["enum"],
                    ["samples", "train", "validation", "test", "assignments", "duplicates", "manifest"],
                )
                publish = self.operations[audience + "PublishDataset"]
                body = publish["requestBody"]["content"]["application/json"]["schema"]
                request = self.schemas[body["$ref"].rsplit("/", 1)[-1]]
                self.assertEqual(
                    set(request["properties"]),
                    {"expected_revision", "seed", "validation_basis_points", "reason"},
                )
        self.assertEqual(
            self.schemas["DatasetVersion"]["properties"]["manifest"],
            {"$ref": "#/components/schemas/DatasetManifest"},
        )

    def version_conditions(self):
        return {
            rule["if"]["properties"]["schema_version"]["const"]: rule["then"]
            for rule in self.schemas["DatasetManifest"]["allOf"]
        }
