"""Keep native monitoring failures decodable by the generated public clients."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


class MonitoringContractTests(unittest.TestCase):
    def test_native_transaction_refusals_remain_in_the_public_problem_model(self):
        source = CONTRACTS / "monitoring/openapi.json"
        projected = project_contract(json.loads(source.read_text(encoding="utf-8")))
        codes = projected["components"]["schemas"]["Problem"]["properties"]["code"]["enum"]
        self.assertTrue({
            "STORAGE_TIMEOUT", "STORAGE_CANCELLED", "STORAGE_COMMIT_ROLLED_BACK",
            "STORAGE_DEADLOCK", "STORAGE_LOCK_TIMEOUT",
        }.issubset(codes))
