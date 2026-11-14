"""Verify exact recorder self-tests and forbid public DNS in the raw site log."""
from collections import Counter
import json
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])
def read(name):
    return [json.loads(row) for row in (root/name).read_text().splitlines()]
for role in ("control", "server", "worker"):
    targets = ("192.0.2.15:9705", "[2001:db8:105::15]:9705") if role == "control" else (
        "192.0.2.13:9704", "[2001:db8:101::13]:9704")
    expected = Counter(("udp", address) for address in targets)
    actual = Counter((event["protocol"],event["destination"]) for event in read(f"{role}-egress.jsonl"))
    if actual != expected:
        raise SystemExit(f"Unexpected {role} packet counts: {actual}")
dns = Counter()
for row in read("site-dns.raw.jsonl"):
    name, kind = row["name"], row["type"]
    if name == "arc104-probe.invalid." and kind == 1:
        dns["self-test"] += 1
    elif kind == 13 and re.fullmatch(r"[0-9]+\.[0-9]+\.",name):
        dns["CoreDNS HINFO"] += 1
    elif kind == 2 and name == ".":
        dns["CoreDNS root NS"] += 1
    else:
        raise SystemExit(f"Unexpected site DNS name: {name} ({kind})")
if dns["self-test"] != 2:
    raise SystemExit(f"Expected two deliberate DNS questions: {dns}")
print("Exact IPv4 and IPv6 recorder probes for all three nodes and two DNS probes passed",dict(dns))
