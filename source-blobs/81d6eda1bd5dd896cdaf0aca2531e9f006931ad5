"""Inventory the public OpenAPI operations in local ownsolid contracts."""

import argparse
from collections import defaultdict
import json
from pathlib import Path
import re

from generate import CONTRACTS, METHODS, public_audiences, public_sources, read_contract


def inventory(contracts):
    """List public operations and the naming and route conflicts in their declarations."""
    operations, findings = [], []
    routes, operation_ids = defaultdict(list), defaultdict(list)
    for source in public_sources(contracts):
        domain = source.parent.name
        document, aliases = read_contract(source)
        for path, item in sorted(document["paths"].items()):
            for method, operation in sorted(item.items()):
                if method not in METHODS:
                    continue
                audiences = sorted(public_audiences(operation, method, path))
                operation_id = operation.get("operationId")
                if isinstance(operation_id, str) and operation_id.strip():
                    operation_ids[(domain, operation_id)].append((method.upper(), path, audiences))
                if not audiences:
                    continue
                row = {
                    "domain": domain, "method": method.upper(), "path": path, "audiences": audiences,
                    "operation_id": operation_id, "summary": bool(operation.get("summary")),
                    "description": bool(operation.get("description")),
                    "deprecated": operation.get("deprecated", False) is True,
                }
                operations.append(row)
                if not isinstance(operation_id, str) or not operation_id.strip():
                    findings.append({"code": "PUBLIC_OPERATION_ID_MISSING", "domain": domain,
                                     "method": row["method"], "path": path})
                if path in aliases:
                    continue
                shape = re.sub(r"\{[^{}]+\}", "{}", path)
                for audience in audiences:
                    routes[(audience, row["method"], shape)].append({"domain": domain, "path": path})

    for (domain, operation_id), entries in sorted(operation_ids.items()):
        if len(entries) > 1 and any(audiences for _, _, audiences in entries):
            findings.append({"code": "OPERATION_ID_DUPLICATE", "domain": domain,
                             "operation_id": operation_id,
                             "operations": [{"method": method, "path": path} for method, path, _ in entries]})
    for (audience, method, path), entries in sorted(routes.items()):
        if len(entries) > 1:
            findings.append({"code": "PUBLIC_ROUTE_DUPLICATE", "audience": audience,
                             "method": method, "path_shape": path, "declarations": entries})

    if not operations:
        raise ValueError("no public OpenAPI operations found")
    return {"operations": operations, "findings": findings}



def compare_operations(baseline, current):
    """Find released operation identities no longer preserved by the current contracts."""
    current_ids = {
        (op["domain"], op["method"], op["path"], audience): op["operation_id"]
        for op in current["operations"] for audience in op["audiences"]
    }
    findings = []
    for old in baseline["operations"]:
        for audience in old["audiences"]:
            key = old["domain"], old["method"], old["path"], audience
            detail = {"domain": old["domain"], "method": old["method"], "path": old["path"],
                      "audience": audience, "previous_operation_id": old["operation_id"]}
            if key not in current_ids:
                findings.append({"code": "PUBLIC_OPERATION_REMOVED", **detail})
            elif current_ids[key] != old["operation_id"]:
                findings.append({"code": "PUBLIC_OPERATION_ID_CHANGED", **detail,
                                 "current_operation_id": current_ids[key]})
    return findings


def main():
    """Print a deterministic report and fail when the audit finds conflicts."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contracts", type=Path, default=CONTRACTS,
                        help="local versioned contracts directory")
    parser.add_argument("--baseline", type=Path,
                        help="released local contract directory of the same API major")
    args = parser.parse_args()
    try:
        report = inventory(args.contracts)
        if args.baseline is not None:
            report["compatibility_findings"] = compare_operations(inventory(args.baseline), report)
    except (ValueError, KeyError, TypeError, OSError) as exc:
        parser.exit(2, f"OpenAPI audit failed: {exc}\n")
    print(json.dumps(report, indent=2))
    return int(bool(report["findings"] or report.get("compatibility_findings")))


if __name__ == "__main__":
    raise SystemExit(main())
