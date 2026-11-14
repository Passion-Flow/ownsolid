import json
import pathlib
import re
import shutil
import subprocess

proof = pathlib.Path(__file__).resolve().parent
roots = {
    "expiry": [
        "TestActualAbandonedImportExpiryRacesWithUpload",
        "TestActualAbandonedImportExpiryRecoversCommittedIntent",
        "TestActualAbandonedImportExpiryKeepsOtherScopesAndStates",
    ],
    "https": [
        "TestModelImportHTTPSExpiryIsScopedAndDistinctFromCancellation",
        "TestModelImportHTTPSIntentResolutionStrictScopeAndNoMutation",
        "TestStrictModelImportHTTPChunksCSRFAndPortalBoundary",
        "TestModelImportHTTPNondefaultAdmissionAndRetryPolicy",
    ],
    "tls": ["TestRealTLSInferenceAPICSRFSchemaAndLeasedModelTransfer"],
    "borrowed": ["TestActualBorrowedModelAccess"],
    "archive": ["TestActualModelImportFileSafety"],
    "publication": ["TestActualModelImportPublicationAuditRollbackAndFreshWorkerRecovery"],
}
results = {}
for label, names in roots.items():
    text = (proof / (label + ".log")).read_text()
    if re.search(r"^FAIL(?:$|\s)|^--- FAIL:", text, re.M) or not text.rstrip().endswith("PASS"):
        raise SystemExit("Actual test failure " + label)
    for name in names:
        if "--- PASS: " + name + " (" not in text:
            raise SystemExit("Missing passed root " + name)
    if label != "publication" and ("warning: no tests to run" in text or "--- SKIP:" in text):
        raise SystemExit("Unexpected test omission " + label)
    results[label] = {"passed_roots": names}

container = (proof / "retained-container").read_text().strip()
scratch = pathlib.Path((proof / "retained-scratch").read_text().strip())
if scratch.parent != pathlib.Path("/dev/shm") or not scratch.name.startswith("ownsolid-lane-m-acceptance."):
    raise SystemExit("Invalid owned scratch")
top = subprocess.check_output(["docker", "top", container, "-eo", "pid,comm"], text=True)
for line in top.splitlines()[1:]:
    fields = line.split(None, 1)
    if len(fields) != 2 or fields[1].strip() != "postgres":
        raise SystemExit("A fixture consumer is still running")
(proof / "consumer-exit-confirmed.log").write_text(top)

results["source_gate"] = "a3b3747ddb31577529a0bd0907adfc70f49e72ee"
results["original_harness_exit"] = int((proof / "acceptance-exit-code").read_text())
results["harness_reconciliation"] = (
    "All selected Go commands passed. The explicit publication snapshot-and-retirement "
    "exclusion emitted a no-subtests warning. The old blanket warning check caused "
    "the harness-only exit. Raw logs and the original harness exit are retained."
)
results["excluded_subtest"] = (
    "TestActualModelImportPublicationAuditRollbackAndFreshWorkerRecovery/snapshot-and-retirement"
)
subprocess.run(["docker", "rm", "-f", container], check=True, capture_output=True)
shutil.rmtree(scratch)
private = proof / "private"
for name in ("dsn", "password"):
    (private / name).unlink()
private.rmdir()
results["cleanup"] = "The owned PostgreSQL container, generated archive, temporary model store and private credentials were removed."
(proof / "validated-results.json").write_text(json.dumps(results, indent=2) + "\n")
print("Validated all six selected actual test commands and removed their owned fixture resources")
