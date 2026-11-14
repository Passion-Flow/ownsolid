from pathlib import Path
import hashlib, json, runpy, ast

root = Path(__file__).resolve().parent
opportunity = root.parent
ns = runpy.run_path(str(root / "payload-policy.py"))
receive = ns["receive_payload"]
classify = ns["classify_opportunity"]
payload = (opportunity / "payload.tar").read_bytes()
expected_sha = hashlib.sha256(payload).hexdigest()
assert expected_sha == "843c3e272625d75133db64129ffbe1934acc465475a2af2a97dd8f8e4c50a5b0"
results = []


def check(name, data, expected_error=None, sha=expected_sha, reject_eof=False, expired=False):
    offset = 0
    receipt = {}
    calls = []
    def read(size):
        nonlocal offset
        calls.append(size)
        part = data[offset:offset + size]
        offset += len(part)
        return part
    def wait(remaining):
        assert 0 < remaining <= 45
        return not (reject_eof and offset == len(data))
    actual_error = None
    try:
        value = receive(read, wait, lambda: 45.0 if expired else 0.0, 45.0,
                        len(payload), sha, receipt)
        assert value == payload
    except (ValueError, TimeoutError) as error:
        actual_error = str(error)
    assert actual_error == expected_error, (name, actual_error, expected_error)
    if expired:
        assert not calls and receipt["received_bytes"] == 0
    if reject_eof:
        assert receipt["received_bytes"] == len(payload) and not receipt["eof_observed"]
    results.append({"case": name, "expected_error": expected_error,
                    "received_bytes": receipt["received_bytes"],
                    "scope": "controlled local input boundary, not reconstruction of the failed network transfer"})


check("exact_retained_payload_and_eof", payload)
check("controlled_short_eof", payload[:1024], "PAYLOAD_BYTE_COUNT_MISMATCH")
check("controlled_extra_byte", payload + b"x", "PAYLOAD_BYTE_COUNT_MISMATCH")
check("controlled_wrong_digest", payload, "PAYLOAD_DIGEST_MISMATCH", sha="0" * 64)
check("complete_bytes_without_eof", payload, "PAYLOAD_ADMISSION_DEADLINE_EXCEEDED", reject_eof=True)
check("expired_before_first_read", payload, "PAYLOAD_ADMISSION_DEADLINE_EXCEEDED", expired=True)
actual_outer = json.loads((opportunity / "outer-result.json").read_text())
actual_remote = json.loads((opportunity / "cleanup-native-evidence/result.json").read_text())
assert actual_outer["state"] == "NOTRUN"
assert classify(actual_outer["ssh_exit"], actual_outer["observed_stage_events"]) == "FAILED_AFTER_REMOTE_ENTRY"
assert classify(actual_outer["ssh_exit"], actual_outer["observed_stage_events"], actual_remote) == "FAILED_OR_INCOMPLETE"
assert actual_remote["error"] == "PAYLOAD_REFUSAL" and not actual_remote["metadata"]
for folder, name in [("arc111-native-e1.7axxa0p5", "preflight-result.json"),
                     ("arc111-native-formal61.qtc543lk", "outer-result.json")]:
    old = json.loads((opportunity.parent / folder / name).read_text())
    assert classify(old["ssh_exit"], []) == "ENTRY_UNCONFIRMED_CONNECTION_FAILED"
assert classify(0, []) == "INCOMPLETE_ENTRY_UNCONFIRMED"
result = {"state": "PASS", "payload_boundary_cases": results,
          "retained_record_classification_checks": 5,
          "actual_failed_received_length_or_digest": None,
          "actual_failed_received_length_or_digest_scope": "Not recorded; never inferred from controlled cases",
          "network_connections": 0, "child_processes": 0, "compilers": 0,
          "database_or_service_workloads": 0, "candidate_activated": False,
          "policy_sha256": hashlib.sha256((root / "payload-policy.py").read_bytes()).hexdigest()}
(root / "offline-result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
print(json.dumps({key: value for key, value in result.items() if key != "payload_boundary_cases"}, indent=2))
