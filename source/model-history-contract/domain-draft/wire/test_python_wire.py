"""Controlled codecs only: no registered route, signature, authorization or SQL claim."""

import asyncio
import copy
import json
from pathlib import Path
import sys
import unittest
from uuid import UUID

import httpx

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "sdk/gen/python"))
from ownsolid_sdk.models.client import Client
from ownsolid_sdk.models.types import Unset
from ownsolid_sdk.models.api.default import (
    get_admin_model, get_admin_historical_model_version,
    list_admin_model_version_history, list_admin_models,
)
from ownsolid_sdk.models.models.list_admin_model_version_history_state import ListAdminModelVersionHistoryState
from ownsolid_sdk.models.models.model_history_problem_code import ModelHistoryProblemCode
from ownsolid_sdk.models.models.model_version_history_page import ModelVersionHistoryPage

FIXTURES = ROOT / "wire/fixtures"
IDS = json.loads((FIXTURES / "ids.json").read_text())
WORKSPACE, ANCHOR, MEMBER, FAMILY = (UUID(IDS[key]) for key in ("workspace", "ready", "retired", "family"))


class ProducerHistoryCodecTests(unittest.TestCase):
    def setUp(self):
        self.calls = []
        self.problem = None
        self.ready, self.retired, self.page, self.derived = (
            json.loads((FIXTURES / (name + ".json")).read_text())
            for name in ("ready", "retired", "page", "derived")
        )
        self.use_derived = False
        self.http = httpx.Client(base_url="https://admin.example.invalid", trust_env=False,
                                 follow_redirects=False, transport=httpx.MockTransport(self.respond))
        self.client = Client(base_url="https://admin.example.invalid").set_httpx_client(self.http)
        self.addCleanup(self.http.close)

    def respond(self, request):
        self.assertEqual(request.method, "GET")
        self.assertEqual(request.content, b"")
        self.assertNotIn("Idempotency-Key", request.headers)
        self.calls.append(request)
        query = request.url.params
        problem = self.problem
        if query.get("limit") == "0" or ("cursor" in query and query["cursor"] == ""):
            problem = (400, "VALIDATION_FAILED")
        if problem:
            status, code = problem
            return httpx.Response(status, headers={"Content-Type": "application/problem+json"}, json={
                "type": "about:blank", "title": code, "status": status, "code": code,
                "request_id": IDS["ready"],
            })
        prefix = "/api/v1/sites/default/cells/default/admin/models/" + IDS["ready"]
        if request.url.path == prefix + "/history":
            self.assertEqual(query["workspace_id"], IDS["workspace"])
            page = copy.deepcopy(self.page)
            if "state" in query:
                page["items"] = [row for row in page["items"] if row["state"] == query["state"]]
            return httpx.Response(200, json={"data": page})
        if request.url.path == prefix + "/history/" + IDS["retired"]:
            self.assertEqual(query["workspace_id"], IDS["workspace"])
            return httpx.Response(200, json={"data": self.derived if self.use_derived else self.retired})
        if request.url.path == prefix:
            return httpx.Response(200, json={"data": self.ready})
        if request.url.path.endswith("/admin/models"):
            return httpx.Response(200, json={"data": {"items": [self.ready]}})
        self.fail("Unexpected draft request: " + str(request.url))

    def history(self, **kwargs):
        return list_admin_model_version_history.sync_detailed(
            "default", "default", ANCHOR, client=self.client, workspace_id=WORKSPACE, **kwargs,
        )

    def detail(self):
        return get_admin_historical_model_version.sync_detailed(
            "default", "default", ANCHOR, MEMBER, client=self.client, workspace_id=WORKSPACE,
        )

    def test_omitted_policy_and_exact_eight_field_metadata(self):
        response = self.history()
        self.assertEqual(dict(self.calls[-1].url.params), {"workspace_id": IDS["workspace"]})
        page = response.parsed.data
        self.assertEqual((page.workspace_id, page.family_id, page.anchor_version_id), (WORKSPACE, FAMILY, ANCHOR))
        self.assertEqual([row.id for row in page.items], [ANCHOR, MEMBER])
        self.assertEqual([row.state.value for row in page.items], ["ready", "retired"])
        self.assertEqual(page.items[0].catalog_digest, page.items[1].catalog_digest)
        self.assertEqual(len(page.items[0].to_dict()), 8)
        self.assertFalse(hasattr(page.items[0], "family_id"))
        self.assertIsNone(page.next_cursor)
        self.assertIn("next_cursor", page.to_dict())
        self.assertEqual(len(self.calls), 1)

    def test_filter_limit_and_opaque_cursor_survive_without_client_interpretation(self):
        cursor = "uninterpreted.fixture-token"
        response = self.history(limit=7, cursor=cursor, state=ListAdminModelVersionHistoryState.RETIRED)
        self.assertEqual(dict(self.calls[-1].url.params), {
            "workspace_id": IDS["workspace"], "limit": "7", "cursor": cursor, "state": "retired",
        })
        self.assertEqual([row.id for row in response.parsed.data.items], [MEMBER])
        with self.assertRaises(ValueError):
            ListAdminModelVersionHistoryState("")

    def test_explicit_zero_and_empty_cursor_do_not_become_defaults(self):
        for options, key, value in (({"limit": 0}, "limit", "0"), ({"cursor": ""}, "cursor", "")):
            with self.subTest(options=options):
                response = self.history(**options)
                self.assertEqual(self.calls[-1].url.params[key], value)
                self.assertEqual(response.status_code, 400)
                self.assertEqual(response.parsed.code.value, "VALIDATION_FAILED")
        self.assertEqual(len(self.calls), 2)

    def test_retired_detail_binds_anchor_and_member_and_omits_base_lineage(self):
        value = self.detail().parsed.data
        self.assertEqual((value.id, value.family_id, value.anchor_version_id), (MEMBER, FAMILY, ANCHOR))
        self.assertEqual(value.state.value, "retired")
        self.assertEqual(value.manifest.to_dict(), self.retired["manifest"])
        self.assertIsInstance(value.dataset_lineage, Unset)
        self.assertNotIn("dataset_lineage", value.to_dict())
        self.assertNotIn("family_id", self.calls[-1].url.params)

    def test_derived_detail_keeps_distinct_training_and_holdout_version_references(self):
        self.use_derived = True
        value = self.detail().parsed.data
        self.assertEqual(value.manifest.to_dict(), self.derived["manifest"])
        for field in ("training", "holdout"):
            source = self.derived["dataset_lineage"][field]
            reference = getattr(value.dataset_lineage, field)
            self.assertEqual(str(reference.dataset_id), source["dataset_id"])
            self.assertEqual(str(reference.dataset_version_id), source["dataset_version_id"])
            self.assertEqual(reference.manifest_sha256, source["manifest_sha256"])

    def test_ready_detail_and_list_keep_the_original_source_shapes(self):
        detail = get_admin_model.sync_detailed("default", "default", ANCHOR, client=self.client).parsed.data
        listing = list_admin_models.sync_detailed("default", "default", client=self.client,
                                                  workspace_id=WORKSPACE).parsed.data.items[0]
        for value in (detail, listing):
            self.assertEqual(value.state, "ready")
            self.assertNotIn("family_id", value.to_dict())
            self.assertNotIn("anchor_version_id", value.to_dict())

    def test_default_problem_decodes_only_known_http_mapping_examples(self):
        for status, code in ((403, "ACCESS_DENIED"), (400, "INVALID_CURSOR"),
                             (409, "STORAGE_COMMIT_OUTCOME_UNKNOWN")):
            self.problem = status, code
            response = self.history()
            self.assertEqual(response.status_code, status)
            self.assertEqual(response.parsed.code.value, code)
            self.assertFalse(hasattr(response.parsed, "data"))
        # Unmapped producer errors are codec vocabulary, not invented HTTP examples.
        self.assertEqual(ModelHistoryProblemCode("MODEL_HISTORY_UNAVAILABLE").value, "MODEL_HISTORY_UNAVAILABLE")
        self.assertEqual(len(self.calls), 3)

    def test_empty_final_page_and_required_null_continuation(self):
        self.page["items"] = []
        value = self.history().parsed.data
        self.assertEqual(value.items, [])
        self.assertIsNone(value.next_cursor)
        missing = copy.deepcopy(self.page)
        del missing["next_cursor"]
        with self.assertRaises(KeyError):
            ModelVersionHistoryPage.from_dict(missing)

    def test_async_operation_uses_the_same_anchor_scope_and_one_page(self):
        async def check():
            async with httpx.AsyncClient(base_url="https://admin.example.invalid", trust_env=False,
                                         follow_redirects=False, transport=httpx.MockTransport(self.respond)) as http:
                client = self.client.set_async_httpx_client(http)
                response = await list_admin_model_version_history.asyncio_detailed(
                    "default", "default", ANCHOR, client=client, workspace_id=WORKSPACE,
                )
                self.assertEqual(response.parsed.data.anchor_version_id, ANCHOR)
        asyncio.run(check())
        self.assertEqual(len(self.calls), 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
