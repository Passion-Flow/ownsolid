"""Verify the SDK projection of the optional dataset tokenizer preview."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from generate import CONTRACTS, project_contract


class DatasetTokenizerContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        source = CONTRACTS / "datasets" / "openapi.json"
        cls.document = project_contract(json.loads(source.read_text(encoding="utf-8")))

    def test_preview_retains_only_the_two_scoped_post_operations(self):
        prefix = "/api/v1/sites/{site_id}/cells/{cell_id}"
        suffix = "/datasets/{dataset_id}/drafts/{draft_id}/tokenizer-preview"
        paths = {
            name: item for name, item in self.document["paths"].items()
            if name.endswith("/tokenizer-preview")
        }
        self.assertEqual(set(paths), {
            prefix + "/admin" + suffix,
            prefix + "/workspaces/{workspace_id}" + suffix,
        })
        for name, item in paths.items():
            with self.subTest(path=name):
                audience = "portal" if "{workspace_id}" in name else "admin"
                self.assertEqual(set(item), {"post", "parameters"})
                operation = item["post"]
                self.assertEqual(operation["security"], [{audience + "Session": []}])
                headers = {field["name"] for field in operation["parameters"] if field["in"] == "header"}
                self.assertEqual(headers, {"Origin", "X-CSRF-Token"})
                self.assertFalse(any(field["in"] == "query" for field in operation["parameters"]))
                self.assertEqual(set(operation["responses"]),
                                 {"200", "400", "401", "403", "404", "409", "413", "415", "422", "429", "503", "default"})

    def test_fit_omits_overflow_pair_and_busy_keeps_retry_header(self):
        schemas = self.document["components"]["schemas"]
        sample = schemas["DatasetTokenizerPreviewSample"]
        index, section = "first_excess_token_index", "first_excess_section"
        self.assertFalse(sample["additionalProperties"])
        self.assertTrue({index, section}.isdisjoint(sample["required"]))
        self.assertEqual(sample["dependentRequired"], {index: [section], section: [index]})
        self.assertEqual(sample["properties"][section]["enum"], ["prompt", "completion", "eos"])
        responses = self.document["components"]["responses"]
        for name in ("DatasetTokenizerPreviewBusy", "DatasetTokenizerPreviewUnavailable"):
            header = responses[name]["headers"]["Retry-After"]
            self.assertTrue(header["required"])
            self.assertEqual(header["schema"], {"type": "integer", "minimum": 1})


if __name__ == "__main__":
    unittest.main()
