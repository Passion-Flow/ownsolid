"""Check assistant qualification remains fail-closed with controlled evidence."""

from pathlib import Path
import tempfile
import unittest

from projectone_model_lifecycle import evaluation_report, evaluation_safety
from qualify_model import qualify, verify_native_observations


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


class NativeObservationsTest(unittest.TestCase):
    def test_native_archive_binds_exact_response_bytes_and_order(self):
        observations = []
        cases = []
        for identifier, response in (("first", "native response"), ("second", "another response")):
            prompt_digest = evaluation_report.sha256(identifier.encode())
            response_digest = evaluation_report.sha256(response.encode())
            observations.append({
                "source_id": identifier,
                "prompt_sha256": prompt_digest,
                "response": response,
                "response_sha256": response_digest,
            })
            cases.append({
                "source_id": identifier,
                "prompt_sha256": prompt_digest,
                "output_sha256": response_digest,
            })
        error = "^ASSISTANT_NATIVE_OBSERVATIONS_MISMATCH$"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "native.json"
            raw = evaluation_report.compact(observations)
            path.write_bytes(raw)
            digest = evaluation_report.sha256(raw)
            verify_native_observations(path, digest, cases)
            with self.assertRaisesRegex(evaluation_report.ReplayError, error):
                verify_native_observations(path, "0" * 64, cases)
            with self.assertRaisesRegex(evaluation_report.ReplayError, error):
                verify_native_observations(path, digest, cases[::-1])
            observations[0]["response"] = "changed"
            raw = evaluation_report.compact(observations)
            path.write_bytes(raw)
            with self.assertRaisesRegex(evaluation_report.ReplayError, error):
                verify_native_observations(path, evaluation_report.sha256(raw), cases)


if __name__ == "__main__":
    unittest.main()
