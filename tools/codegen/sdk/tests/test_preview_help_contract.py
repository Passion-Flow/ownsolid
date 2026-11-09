"""Keep published preview problems discoverable in the correct Help entrance."""

import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[4]
CUTOVER = {
    "409": {
        "ALIAS_PAUSE_REQUIRED", "ALIAS_ALREADY_PAUSED", "ALIAS_NOT_PAUSED",
        "ALIAS_REQUESTS_UNDRAINED", "ALIAS_ROLLBACK_TARGET_INVALID",
    },
}
TOKENIZER = {
    "404": {"DATASET_NOT_FOUND"},
    "409": {"DATASET_FILE_INVALID"},
    "422": {"DATASET_EMPTY_SOURCE", "DATASET_VALIDATION_FAILED"},
    "503": {
        "DATASET_PROCESSOR_TIMEOUT", "DATASET_PROCESSOR_MEMORY_EXCEEDED",
        "DATASET_DATA_INVALID", "DATASET_STORAGE_UNAVAILABLE",
    },
}


def document(path):
    return json.loads((ROOT / path).read_text(encoding="utf-8"))


class PreviewHelpContractTests(unittest.TestCase):
    def test_preview_problems_keep_their_statuses_and_bilingual_details(self):
        locales = [
            document(f"web/packages/i18n/locales/{locale}.json")
            for locale in ("en", "zh-cn")
        ]
        for entrance, domain, name, expected in (
            ("console", "distribution", "adminPreviewAliasCutover", CUTOVER),
            ("console", "datasets", "adminPreviewDatasetTokenizer", TOKENIZER),
            ("portal", "datasets", "portalPreviewDatasetTokenizer", TOKENIZER),
        ):
            with self.subTest(operation=name):
                contract = document(f"api/openapi/v1/{domain}/openapi.json")
                operation, = (
                    item["post"] for item in contract["paths"].values()
                    if item.get("post", {}).get("operationId") == name
                )
                help_api = document(f"web/packages/help/src/gen/api/{entrance}/{domain}.json")
                help_operation, = (row for row in help_api["operations"] if row["id"] == name)
                index = operation["x-error-codes"]
                self.assertEqual(help_operation["errors"], index)
                for status, codes in expected.items():
                    self.assertIn(status, operation["responses"])
                    self.assertTrue(codes.issubset(index[status]))
                    for other_status, other_codes in index.items():
                        if other_status != status:
                            self.assertTrue(codes.isdisjoint(other_codes))
                    for code in codes:
                        for locale in locales:
                            for field in ("title", "detail"):
                                self.assertTrue(locale[f"problem.{code}.{field}"])

    def test_portal_help_excludes_admin_previews(self):
        for domain, name in (
            ("distribution", "adminPreviewAliasCutover"),
            ("datasets", "adminPreviewDatasetTokenizer"),
        ):
            help_api = document(f"web/packages/help/src/gen/api/portal/{domain}.json")
            self.assertNotIn(name, {row["id"] for row in help_api["operations"]})
