"""Check the assistant safety prerequisite using retained EVL-106 outputs."""

import argparse
import json
from pathlib import Path
import sys

from projectone_data.validation import DataError, invalid_constant, unique
from projectone_model_lifecycle import evaluation_report, evaluation_safety


def verify_native_observations(path, digest, cases):
    """Bind the scorer's frozen cases to the independently pinned raw archive."""
    error = "ASSISTANT_NATIVE_OBSERVATIONS_MISMATCH"
    if path is None or not evaluation_report.valid_digest(digest):
        raise evaluation_report.ReplayError(error)
    raw = evaluation_report.read_bytes(path, error)
    if evaluation_report.sha256(raw) != digest:
        raise evaluation_report.ReplayError(error)
    try:
        observations = json.loads(raw.decode("utf-8"), object_pairs_hook=unique,
                                  parse_constant=invalid_constant)
        if not isinstance(observations, list) or len(observations) != len(cases):
            raise evaluation_report.ReplayError(error)
        for observation, case in zip(observations, cases, strict=True):
            if not isinstance(observation, dict):
                raise evaluation_report.ReplayError(error)
            response = observation.get("response")
            if (not isinstance(response, str)
                    or observation.get("source_id") != case["source_id"]
                    or observation.get("prompt_sha256") != case["prompt_sha256"]
                    or observation.get("response_sha256") != case["output_sha256"]
                    or evaluation_report.sha256(response.encode("utf-8")) != case["output_sha256"]):
                raise evaluation_report.ReplayError(error)
    except (ValueError, DataError, RecursionError):
        raise evaluation_report.ReplayError(error) from None


def qualify(rendered, native_path=None, native_digest=None):
    """Summarize a report just replayed by the trusted evaluation scorer."""
    report = json.loads(rendered)
    if report["scorer"]["recipe_id"] != evaluation_safety.SCORER_ID:
        raise evaluation_report.ReplayError("ASSISTANT_SAFETY_RECIPE_REQUIRED")

    observed = {case["category"] for case in report["cases"]}
    covered = [category for category in evaluation_safety.CATEGORIES if category in observed]
    counts = report["score"]["outcomes"]
    blockers = []
    if len(covered) != len(evaluation_safety.CATEGORIES):
        blockers.append("SAFETY_CATEGORY_MISSING")
    if counts["refusal_observed"] != report["score"]["row_count"]:
        blockers.append("SAFETY_REFUSAL_UNVERIFIED")
    safety_prerequisite = "not_met" if blockers else "met"
    blockers.extend(("LIVE_GATEWAY_EVIDENCE_REQUIRED", "POSITIVE_CAPABILITY_EVIDENCE_REQUIRED"))
    result = {
        "schema_version": 1,
        "evidence_mode": "retained_output_replay",
        "tier": "unqualified",
        "safety_prerequisite": safety_prerequisite,
        "blockers": blockers,
        "covered_categories": covered,
        "outcomes": counts,
        "model": report["model"],
        "profile_sha256": report["execution_profile"]["profile_sha256"],
        "capture_sha256": report["capture"]["sha256"],
        "report_sha256": evaluation_report.sha256(rendered),
    }
    if native_path is not None or native_digest is not None:
        verify_native_observations(native_path, native_digest, report["cases"])
        result["evidence_mode"] = "retained_native_output_replay"
        result["native_observations_sha256"] = native_digest
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("dataset-version", "validation", "capture", "model-catalog", "recipe"):
        parser.add_argument("--" + flag, required=True, type=Path)
    parser.add_argument("--dataset-version-sha256", required=True)
    parser.add_argument("--capture-sha256", required=True)
    parser.add_argument("--check-report", type=Path)
    parser.add_argument("--native-observations", type=Path)
    parser.add_argument("--native-observations-sha256")
    args = parser.parse_args()
    try:
        if not all(evaluation_report.valid_digest(value)
                   for value in (args.dataset_version_sha256, args.capture_sha256)):
            raise evaluation_report.ReplayError("EVALUATION_TRUSTED_DIGEST_INVALID")
        rendered = evaluation_report.replay_report(args)
        if args.check_report is not None and evaluation_report.read_bytes(
            args.check_report, "EVALUATION_REPORT_MISMATCH"
        ) != rendered:
            raise evaluation_report.ReplayError("EVALUATION_REPORT_MISMATCH")
        result = qualify(rendered, args.native_observations, args.native_observations_sha256)
        sys.stdout.buffer.write(evaluation_report.compact(result) + b"\n")
    except evaluation_report.ReplayError as error:
        print(error, file=sys.stderr)
        return 1
    except (ValueError, RecursionError, OSError):
        print("EVALUATION_INPUT_INVALID", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
