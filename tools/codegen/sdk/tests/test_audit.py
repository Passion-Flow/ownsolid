"""Check the offline public API inventory and audit exit status."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SDK = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK))
from audit import compare_operations, inventory


def write_contract(root, domain, paths):
    target = root / domain
    target.mkdir()
    (target / "openapi.json").write_text(json.dumps({"paths": paths}), encoding="utf-8")


class InventoryTests(unittest.TestCase):
    def test_visibility_and_overlap(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            write_contract(root, "storage", {
                "/items/{name}": {
                    "get": {"operationId": "read", "x-audiences": ["admin", "portal"]},
                    "post": {"operationId": "agentWrite", "x-audiences": ["agent"]},
                    "parameters": [],
                },
            })
            write_contract(root, "nodes", {
                "/items/{id}": {
                    "get": {"x-audiences": ["admin"], "summary": "Get item"},
                },
                "/nodes": {
                    "get": {"operationId": "nodeList", "x-audiences": ["admin"]},
                    "head": {"operationId": "nodeList", "x-audiences": ["agent"]},
                },
            })
            write_contract(root, "knowledge", {
                "/items/{id}": {"get": {"x-audiences": ["portal"]}},
            })
            report = inventory(root)
            self.assertEqual([op["domain"] for op in report["operations"]], ["nodes", "nodes", "storage"])
            self.assertTrue(report["operations"][0]["summary"])
            self.assertEqual(report["operations"][2]["audiences"], ["admin", "portal"])
            self.assertEqual([finding["code"] for finding in report["findings"]],
                             ["PUBLIC_OPERATION_ID_MISSING", "OPERATION_ID_DUPLICATE", "PUBLIC_ROUTE_DUPLICATE"])
            collision = report["findings"][-1]
            self.assertEqual(collision["audience"], "admin")
            self.assertEqual(collision["path_shape"], "/items/{}")
            self.assertEqual([entry["domain"] for entry in collision["declarations"]], ["nodes", "storage"])
            result = subprocess.run(
                [sys.executable, str(SDK / "audit.py"), "--contracts", str(root)],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 1)
            self.assertEqual(json.loads(result.stdout), report)

    def test_distinct_audiences_and_methods_do_not_conflict(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            write_contract(root, "one", {
                "/items/{name}": {"get": {"operationId": "read", "x-audiences": ["admin"]}},
            })
            write_contract(root, "two", {
                "/items/{id}": {
                    "get": {"operationId": "read", "x-audiences": ["portal"]},
                    "post": {"operationId": "write", "x-audiences": ["admin"]},
                },
            })
            self.assertEqual(inventory(root)["findings"], [])


    def test_version_comparison_checks_sdk_identity_and_audience(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            baseline, candidate = root / "baseline", root / "candidate"
            baseline.mkdir()
            candidate.mkdir()
            write_contract(baseline, "nodes", {
                "/nodes": {"get": {"operationId": "list", "x-audiences": ["admin", "portal"]}},
                "/legacy": {"get": {"operationId": "legacy", "x-audiences": ["admin"],
                                    "deprecated": True}},
            })
            write_contract(candidate, "nodes", {
                "/nodes": {"get": {"operationId": "listNodes", "x-audiences": ["admin"]}},
                "/new": {"get": {"operationId": "new", "x-audiences": ["admin"]}},
            })
            write_contract(candidate, "other", {
                "/legacy": {"get": {"operationId": "legacy", "x-audiences": ["admin"]}},
            })
            previous, current = inventory(baseline), inventory(candidate)
            self.assertEqual(current["findings"], [])
            self.assertEqual(compare_operations(previous, previous), [])
            self.assertTrue(previous["operations"][0]["deprecated"])
            findings = compare_operations(previous, current)
            self.assertEqual([(f["code"], f["path"], f["audience"]) for f in findings], [
                ("PUBLIC_OPERATION_REMOVED", "/legacy", "admin"),
                ("PUBLIC_OPERATION_ID_CHANGED", "/nodes", "admin"),
                ("PUBLIC_OPERATION_REMOVED", "/nodes", "portal"),
            ])
            result = subprocess.run(
                [sys.executable, str(SDK / "audit.py"), "--contracts", str(candidate), "--baseline", str(baseline)],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 1)
            self.assertEqual(json.loads(result.stdout)["compatibility_findings"], findings)

    def test_no_public_api_is_an_error(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            write_contract(root, "private", {"/nodes": {"get": {"x-audiences": ["agent"]}}})
            with self.assertRaisesRegex(ValueError, "no public OpenAPI operations"):
                inventory(root)


if __name__ == "__main__":
    unittest.main()
