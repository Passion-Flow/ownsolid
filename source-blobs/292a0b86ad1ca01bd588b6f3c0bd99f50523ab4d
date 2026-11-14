"""Check assistant qualification remains fail-closed with controlled evidence."""

import unittest

from projectone_model_lifecycle import evaluation_report, evaluation_safety
from qualify_model import qualify


class QualificationTest(unittest.TestCase):
    def summary(self, categories, refusals, recipe=evaluation_safety.SCORER_ID):
        report = evaluation_report.compact({
            "scorer": {"recipe_id": recipe},
            "cases": [{"category": category} for category in categories],
            "score": {
                "row_count": len(categories),
                "outcomes": {
                    "refusal_observed": refusals,
                    "unverified": len(categories) - refusals,
                },
            },
            "model": {"repository": "synthetic"},
            "execution_profile": {"profile_sha256": "a" * 64},
            "capture": {"sha256": "b" * 64},
        })
        result = qualify(report)
        self.assertEqual(result["report_sha256"], evaluation_report.sha256(report))
        self.assertEqual(result["tier"], "unqualified")
        self.assertIn("LIVE_GATEWAY_EVIDENCE_REQUIRED", result["blockers"])
        self.assertIn("POSITIVE_CAPABILITY_EVIDENCE_REQUIRED", result["blockers"])
        return result

    def test_exact_refusals_meet_only_the_safety_prerequisite(self):
        result = self.summary(evaluation_safety.CATEGORIES, 3)
        self.assertEqual(result["safety_prerequisite"], "met")

    def test_unverified_output_blocks_the_prerequisite(self):
        result = self.summary(evaluation_safety.CATEGORIES, 2)
        self.assertEqual(result["safety_prerequisite"], "not_met")
        self.assertIn("SAFETY_REFUSAL_UNVERIFIED", result["blockers"])

    def test_duplicate_category_cannot_replace_missing_coverage(self):
        injection, leakage, _ = evaluation_safety.CATEGORIES
        result = self.summary((injection, leakage, injection), 3)
        self.assertEqual(result["safety_prerequisite"], "not_met")
        self.assertIn("SAFETY_CATEGORY_MISSING", result["blockers"])

    def test_unrelated_scorer_is_rejected(self):
        with self.assertRaisesRegex(evaluation_report.ReplayError, "^ASSISTANT_SAFETY_RECIPE_REQUIRED$"):
            self.summary(evaluation_safety.CATEGORIES, 3, recipe="support_routing_v1")


if __name__ == "__main__":
    unittest.main()
