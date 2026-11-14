"""Verify the published training pagination limits survive SDK projection."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


class TrainingPaginationContractTests(unittest.TestCase):
    def test_collection_and_metric_cursors_keep_distinct_bounds(self):
        source = CONTRACTS / "training" / "openapi.json"
        document = project_contract(json.loads(source.read_text(encoding="utf-8")))
        lengths = {
            operation["operationId"]: parameter["schema"]["maxLength"]
            for item in document["paths"].values()
            if (operation := item.get("get"))
            for parameter in operation.get("parameters", [])
            if parameter["in"] == "query" and parameter["name"] == "cursor"
        }
        self.assertEqual(lengths, {
            f"{audience}List{collection}": limit
            for audience in ("admin", "portal")
            for collection, limit in (
                ("Jobs", 512), ("Attempts", 512), ("Checkpoints", 512),
                ("Recoveries", 512), ("Metrics", 2048),
            )
        })
