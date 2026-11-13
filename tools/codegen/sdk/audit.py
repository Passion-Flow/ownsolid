"""Inventory the public OpenAPI operations in local ownsolid contracts."""

import argparse
from collections import defaultdict
import json
from pathlib import Path
import re
from urllib.parse import unquote

from generate import CONTRACTS, METHODS, public_audiences, public_sources, read_contract


def response_object_shape(schema):
    """Return the declared object names and closure, independently of value constraints."""
    object_keys = ("properties", "required", "additionalProperties")
    if not any(key in schema for key in object_keys) and schema.get("type") != "object":
        return None
    properties, required = schema.get("properties", {}), schema.get("required", [])
    if (not isinstance(properties, dict) or not isinstance(required, list)
            or any(not isinstance(name, str) for name in required)):
        raise ValueError("response object properties and required names must be valid")
    return {
        "properties": sorted(properties), "required": sorted(set(required)),
        "closed": schema.get("additionalProperties") is False,
    }


def response_surface(document, operation):
    """Collect named models and object shapes for each local response schema."""
    def resolve(reference):
        if not isinstance(reference, str) or not reference.startswith("#"):
            raise ValueError("response references must use local JSON pointers")
        if re.search(r"%(?![0-9a-fA-F]{2})", reference):
            raise ValueError("response reference contains an invalid percent escape")
        try:
            pointer = unquote(reference[1:], errors="strict")
        except UnicodeError as exc:
            raise ValueError("response reference contains invalid UTF-8") from exc
        if not pointer.startswith("/"):
            raise ValueError("response references must use local JSON pointers")
        value = document
        try:
            for segment in pointer[1:].split("/"):
                if re.search(r"~(?:[^01]|$)", segment):
                    raise ValueError("response reference contains an invalid tilde escape")
                token = segment.replace("~1", "/").replace("~0", "~")
                if isinstance(value, list):
                    if not re.fullmatch(r"0|[1-9][0-9]*", token):
                        raise ValueError("response reference contains an invalid array index")
                    # An out-of-range token need not enter Python's bounded integer parser.
                    if len(token) > len(str(len(value))):
                        raise IndexError
                    value = value[int(token)]
                else:
                    value = value[token]
        except (KeyError, IndexError, TypeError) as exc:
            raise ValueError("response reference does not resolve in the local contract") from exc
        return value, pointer

    models, shapes = {}, {}
    responses = operation.get("responses", {})
    if not isinstance(responses, dict):
        raise ValueError("responses must be an object")
    for status, response in sorted(responses.items()):
        seen_responses = set()
        while isinstance(response, dict) and "$ref" in response:
            reference = response["$ref"]
            if reference in seen_responses:
                raise ValueError("cyclic response object reference")
            seen_responses.add(reference)
            response, _ = resolve(reference)
        if not isinstance(response, dict):
            raise ValueError("response must be an object")
        content_types = response.get("content", {})
        if not isinstance(content_types, dict):
            raise ValueError("response content must be an object")
        for media_type, content in sorted(content_types.items()):
            if not isinstance(content, dict):
                raise ValueError("response media type must be an object")
            references, seen, objects = set(), set(), {}

            def visit(value, location):
                if isinstance(value, list):
                    for index, item in enumerate(value):
                        visit(item, f"{location}/{index}")
                elif isinstance(value, dict):
                    shape = response_object_shape(value)
                    if shape is not None:
                        objects[location] = shape
                    if "$ref" in value:
                        reference = value["$ref"]
                        if not isinstance(reference, str):
                            raise ValueError("response reference must be a string")
                        target, pointer = resolve(reference)
                        if pointer.startswith("/components/schemas/"):
                            references.add(reference)
                        if reference not in seen:
                            seen.add(reference)
                            visit(target, reference)
                    # Annotation payloads such as examples can contain literal
                    # "$ref" fields. Only schema-bearing keywords form type edges.
                    for keyword in ("allOf", "anyOf", "oneOf", "prefixItems"):
                        visit(value.get(keyword), f"{location}/{keyword}")
                    for keyword in ("items", "contains", "not", "if", "then", "else",
                                    "additionalProperties", "unevaluatedProperties",
                                    "additionalItems", "unevaluatedItems", "propertyNames",
                                    "contentSchema"):
                        visit(value.get(keyword), f"{location}/{keyword}")
                    for keyword in ("properties", "patternProperties", "dependentSchemas",
                                    "dependencies"):
                        children = value.get(keyword, {})
                        if isinstance(children, dict):
                            for name, child in sorted(children.items()):
                                escaped = name.replace("~", "~0").replace("/", "~1")
                                visit(child, f"{location}/{keyword}/{escaped}")

            visit(content.get("schema"), "#")
            if references:
                models[f"{status} {media_type}"] = sorted(references)
            if objects:
                shapes[f"{status} {media_type}"] = dict(sorted(objects.items()))
    return models, shapes


def response_models(document, operation):
    """Collect local named schema references for each typed response body."""
    return response_surface(document, operation)[0]


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
                models, shapes = response_surface(document, operation)
                row = {
                    "domain": domain, "method": method.upper(), "path": path, "audiences": audiences,
                    "operation_id": operation_id, "summary": bool(operation.get("summary")),
                    "description": bool(operation.get("description")),
                    "deprecated": operation.get("deprecated", False) is True,
                    "response_models": models, "response_objects": shapes,
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



def compare_response_models(baseline, current):
    """Flag named response model changes for review without classifying wire safety."""
    current_operations = {
        (op["domain"], op["method"], op["path"], audience): op
        for op in current["operations"] for audience in op["audiences"]
    }
    findings = []
    for previous in baseline["operations"]:
        for audience in previous["audiences"]:
            key = previous["domain"], previous["method"], previous["path"], audience
            candidate = current_operations.get(key)
            if candidate is None or candidate["operation_id"] != previous["operation_id"]:
                continue
            before, after = previous.get("response_models", {}), candidate.get("response_models", {})
            if before != after:
                findings.append({
                    "code": "PUBLIC_RESPONSE_MODELS_CHANGED",
                    "domain": previous["domain"], "method": previous["method"],
                    "path": previous["path"], "audience": audience,
                    "operation_id": previous["operation_id"],
                    "previous_response_models": before, "current_response_models": after,
                })
    return findings


def compare_response_objects(baseline, current):
    """Flag closed response object shape changes without claiming wire safety."""
    current_operations = {
        (op["domain"], op["method"], op["path"], audience): op
        for op in current["operations"] for audience in op["audiences"]
    }
    findings = []
    for previous in baseline["operations"]:
        for audience in previous["audiences"]:
            key = previous["domain"], previous["method"], previous["path"], audience
            candidate = current_operations.get(key)
            if candidate is None or candidate["operation_id"] != previous["operation_id"]:
                continue
            before = previous.get("response_objects", {})
            after = candidate.get("response_objects", {})
            for body in sorted(before):
                for location, old_shape in sorted(before[body].items()):
                    new_shape = after.get(body, {}).get(location)
                    was_or_is_closed = old_shape["closed"] or new_shape is not None and new_shape["closed"]
                    if old_shape != new_shape and was_or_is_closed:
                        findings.append({
                            "code": "PUBLIC_RESPONSE_OBJECT_SHAPE_CHANGED",
                            "domain": previous["domain"], "method": previous["method"],
                            "path": previous["path"], "audience": audience,
                            "operation_id": previous["operation_id"],
                            "response": body, "schema_location": location,
                            "previous_shape": old_shape, "current_shape": new_shape,
                        })
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
            previous = inventory(args.baseline)
            report["compatibility_findings"] = (
                compare_operations(previous, report) + compare_response_models(previous, report)
                + compare_response_objects(previous, report)
            )
    except (ValueError, KeyError, TypeError, OSError) as exc:
        parser.exit(2, f"OpenAPI audit failed: {exc}\n")
    print(json.dumps(report, indent=2))
    return int(bool(report["findings"] or report.get("compatibility_findings")))


if __name__ == "__main__":
    raise SystemExit(main())
