"""Review the producer-bound prototype without publishing an unregistered API."""

import json
from hashlib import sha256
from pathlib import Path
import sys
import unittest

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from generate import CONTRACTS, project_contract


ANCHOR = "/api/v1/sites/{site_id}/cells/{cell_id}/admin/models/{model_id}"
HISTORY = ANCHOR + "/history"
DETAIL = HISTORY + "/{version_id}"
FIXTURE = SDK / "tests/testdata/model-history-domain/v1/models/openapi.json"
FIELDS = {"id", "workspace_id", "name", "model_key", "kind", "catalog_digest", "state", "created_at"}


class ModelHistoryDraftTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.baseline_bytes = (CONTRACTS / "models/openapi.json").read_bytes()
        cls.baseline = json.loads(cls.baseline_bytes)
        cls.draft = json.loads(FIXTURE.read_text())
        cls.schemas = cls.draft["components"]["schemas"]
        cls.producer = cls.draft["x-ownsolid-producer"]

    def test_draft_is_isolated_and_all_released_paths_and_components_are_unchanged(self):
        self.assertEqual(self.draft["x-ownsolid-publication-state"], "prototype-unregistered")
        self.assertEqual(sha256(self.baseline_bytes).hexdigest(), self.producer["public-models-contract-sha256"])
        self.assertEqual(self.producer["candidate"], "2b4aaae5f8c690df9943b7dd7a7e4b5004aa421b")
        self.assertEqual(self.producer["domain"], "343cdd9d9a69572b77021baec5383b8acb4bfe64")
        self.assertEqual(set(self.draft["paths"]) - set(self.baseline["paths"]), {HISTORY, DETAIL})
        for path, operation in self.baseline["paths"].items():
            self.assertEqual(self.draft["paths"][path], operation, path)
        for group, values in self.baseline["components"].items():
            for name, value in values.items():
                self.assertEqual(self.draft["components"][group][name], value, name)
        projected_base = project_contract(self.baseline)
        projected = project_contract(self.draft)
        for name, value in projected_base["components"]["schemas"].items():
            self.assertEqual(projected["components"]["schemas"][name], value, name)

    def test_new_reads_use_an_exact_anchor_and_current_admin_security(self):
        for path in (HISTORY, DETAIL):
            item = self.draft["paths"][path]
            self.assertEqual(set(item), {"parameters", "get"})
            operation = item["get"]
            self.assertEqual(operation["x-audiences"], ["admin"])
            self.assertEqual(operation["security"], self.baseline["paths"][ANCHOR]["get"]["security"])
            self.assertNotIn("requestBody", operation)
            names = {p["name"] for p in item["parameters"] + operation["parameters"]}
            self.assertTrue({"site_id", "cell_id", "workspace_id", "model_id"} <= names)
            self.assertFalse(names & {"family_id", "tenant_id", "actor_id", "session_id", "name", "slug"})
            self.assertEqual("version_id" in names, path == DETAIL)

    def test_metadata_and_page_follow_the_actual_flat_domain_projection(self):
        item = self.schemas["ModelVersionHistoryEntry"]
        self.assertFalse(item["additionalProperties"])
        self.assertEqual(set(item["properties"]), FIELDS)
        self.assertEqual(set(item["required"]), FIELDS)
        page = self.schemas["ModelVersionHistoryPage"]
        expected = {"workspace_id", "family_id", "anchor_version_id", "items", "next_cursor"}
        self.assertEqual(set(page["properties"]), expected)
        self.assertEqual(set(page["required"]), expected)
        self.assertFalse(page["additionalProperties"])
        cursor = page["properties"]["next_cursor"]
        self.assertEqual(cursor["type"], ["string", "null"])
        self.assertNotIn("default", cursor)
        self.assertEqual(page["properties"]["items"]["maxItems"], 100)

    def test_omitted_navigation_remains_distinct_from_explicit_invalid_options(self):
        params = {p["name"]: p for p in self.draft["paths"][HISTORY]["get"]["parameters"]}
        self.assertEqual(params["limit"]["schema"], {"type": "integer", "minimum": 1, "maximum": 100})
        self.assertEqual(params["state"]["schema"]["enum"], ["ready", "retired"])
        self.assertEqual(params["cursor"]["schema"]["minLength"], 1)
        self.assertEqual(params["cursor"]["schema"]["maxLength"], 512)
        for name in ("limit", "state", "cursor"):
            self.assertFalse(params[name].get("required", False))
            self.assertNotIn("default", params[name]["schema"])
        self.assertEqual(self.producer["default-limit-policy"], "runtime.ui.page_size")

    def test_retained_detail_reuses_manifests_and_optional_lineage_without_widening_ready_dtos(self):
        detail = self.schemas["HistoricalAdminModelDetail"]
        self.assertEqual(set(detail["properties"]), FIELDS | {"family_id", "anchor_version_id", "manifest", "dataset_lineage"})
        self.assertNotIn("dataset_lineage", detail["required"])
        self.assertEqual(detail["properties"]["dataset_lineage"], {"$ref": "#/components/schemas/ModelDatasetLineage"})
        self.assertEqual(detail["properties"]["manifest"], self.schemas["ModelVersion"]["properties"]["manifest"])
        self.assertEqual(detail["properties"]["state"]["enum"], ["ready", "retired"])
        for name in ("ModelVersion", "AdminModelDetail"):
            self.assertEqual(self.schemas[name]["properties"]["state"], {"type": "string", "const": "ready"})
            self.assertNotIn("family_id", self.schemas[name]["properties"])

    def test_domain_errors_do_not_invent_http_status_admission(self):
        missing = set(self.producer["domain-codes-without-http-mapping"])
        self.assertEqual(missing, {"MODEL_HISTORY_UNAVAILABLE", "MODEL_HISTORY_INVALID", "MODEL_FAMILY_INVALID",
                                   "MODEL_IDENTITY_REQUIRED", "MODEL_STORAGE_MISMATCH"})
        self.assertTrue(missing <= set(self.schemas["ModelHistoryProblem"]["properties"]["code"]["enum"]))
        for path in (HISTORY, DETAIL):
            operation = self.draft["paths"][path]["get"]
            self.assertEqual(set(operation["responses"]), {"200", "default"})
            self.assertNotIn("x-error-codes", operation)
            self.assertEqual(operation["responses"]["default"]["content"]["application/problem+json"]["schema"],
                             {"$ref": "#/components/schemas/ModelHistoryProblem"})

    def test_metadata_fields_leave_room_inside_the_actual_complete_page_budget(self):
        # Control characters use six JSON bytes per rune, more than ordinary UTF-8 names.
        item = {"id": "11111111-1111-4111-8111-111111111111", "workspace_id": "22222222-2222-4222-8222-222222222222",
                "name": "\x01" * 100, "model_key": "qwen3-0.6b", "kind": "approved_base", "catalog_digest": "a" * 64,
                "state": "retired", "created_at": "9999-12-31T23:59:59.999999Z"}
        encoded = lambda value: json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
        self.assertLessEqual(len(encoded(item)), self.producer["maximum-item-bytes"])
        page = {"workspace_id": item["workspace_id"], "family_id": item["id"], "anchor_version_id": item["id"],
                "items": [item] * 100, "next_cursor": "a" * 512}
        self.assertLessEqual(len(encoded({"data": page})), self.producer["maximum-page-bytes"])
        self.assertEqual(self.draft["paths"][HISTORY]["get"]["x-ownsolid-maximum-response-bytes"], 524288)
        self.assertEqual(self.draft["paths"][DETAIL]["get"]["x-ownsolid-maximum-response-bytes"], 131072)
