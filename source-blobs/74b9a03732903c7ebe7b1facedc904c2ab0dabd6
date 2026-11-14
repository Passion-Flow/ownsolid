"""Generate Go and Python SDK source for the public ownsolid contracts."""

import argparse
import ast
import copy
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import quote


ROOT = Path(__file__).resolve().parents[3]
TEMPLATES = Path(__file__).resolve().parent / "templates"
CONTRACTS = ROOT / "api" / "openapi" / "v1"
METHODS = {"get", "put", "post", "delete", "options", "head", "patch", "trace"}
PUBLIC = {"admin", "portal", "model-gateway"}
AUDIENCES = PUBLIC | {"agent", "gateway"}
UNLISTED = {"knowledge"}
GO_VERSION = "v2.8.2"
PYTHON_VERSION = "0.29.1"


def public_audiences(operation, method, path):
    """Return the SDK-visible audiences, rejecting unclassified operations."""
    audiences = set(operation.get("x-audiences", ()))
    if not audiences or audiences - AUDIENCES:
        raise ValueError(f"{method.upper()} {path} needs known audiences")
    return audiences & PUBLIC


def public_sources(contracts):
    """Find local contracts that may contribute public operations."""
    return (source for source in sorted(contracts.glob("*/openapi.json"))
            if source.parent.name not in UNLISTED)


def _read_contract_json(source):
    """Reject ambiguous JSON before a source contract can lose operations."""
    def unique_object(pairs):
        result = {}
        for name, value in pairs:
            if name in result:
                raise ValueError(f"{source}: duplicate JSON member {json.dumps(name)}")
            result[name] = value
        return result

    def reject_constant(value):
        raise ValueError(f"{source}: non-finite JSON number {value} is not supported")

    try:
        document = json.loads(source.read_text(encoding="utf-8"),
                              object_pairs_hook=unique_object, parse_constant=reject_constant)
    except json.JSONDecodeError as exc:
        raise ValueError(f"{source}: invalid JSON: {exc}") from exc
    if not isinstance(document, dict):
        raise ValueError(f"{source}: OpenAPI contract must be a JSON object")
    return document


def read_contract(source):
    """Resolve the supported local path aliases before client projection."""
    document = _read_contract_json(source)
    aliases = set()
    owner = None
    for path, item in document.get("paths", {}).items():
        if "$ref" not in item:
            continue
        pointer = quote(path.replace("~", "~0").replace("/", "~1"), safe="~")
        expected = {"$ref": "../components/openapi.json#/paths/" + pointer}
        if source.parent.name != "nodes" or item != expected:
            raise ValueError(f"{source}: unsupported OpenAPI path reference for {path}")
        if owner is None:
            target = source.parent.parent / "components" / "openapi.json"
            owner = _read_contract_json(target)
        canonical = owner.get("paths", {}).get(path)
        if not canonical or "$ref" in canonical or not METHODS.intersection(canonical):
            raise ValueError(f"{source}: missing canonical OpenAPI path for {path}")
        document["paths"][path] = canonical
        aliases.add(path)
    return document, aliases


def project_contract(document):
    """Keep the public operations and every component reachable from them."""
    public_paths = {}
    for path, item in document["paths"].items():
        operations = {}
        for method, op in item.items():
            if method not in METHODS:
                continue
            if public_audiences(op, method, path):
                operations[method] = op
        if operations:
            shared = {key: value for key, value in item.items() if key not in METHODS}
            public_paths[path] = {**shared, **operations}

    # Resolve only document-local references so generation cannot fetch remote files.
    source_components = document.get("components", {})
    used = {"securitySchemes": copy.deepcopy(source_components.get("securitySchemes", {}))}

    def visit(value):
        if isinstance(value, list):
            for child in value:
                visit(child)
        elif isinstance(value, dict):
            if ref := value.get("$ref"):
                parts = ref.split("/")
                if len(parts) != 4 or parts[:2] != ["#", "components"]:
                    raise ValueError(f"unsupported reference in SDK contract: {ref}")
                group, name = (part.replace("~1", "/").replace("~0", "~") for part in parts[2:])
                entries = used.setdefault(group, {})
                if name not in entries:
                    entries[name] = copy.deepcopy(source_components[group][name])
                    visit(entries[name])
            for child in value.values():
                visit(child)

    visit(public_paths)
    schemas = used.get("schemas", {})
    if "DerivedModelCatalog" in schemas:
        files = schemas["DerivedModelCatalog"].get("properties", {}).get("files", {})
        if files.get("items") is False:
            # Both generators require an object here. JSON Schema 'not {}' also rejects all values.
            files["items"] = {"not": {}}
    metadata = ("openapi", "info", "jsonSchemaDialect", "servers", "security", "tags")
    projected = {key: document[key] for key in metadata if key in document}
    projected.update(paths=public_paths, components=used)
    return projected


def checked_tool(executable, version_flag, expected):
    """Resolve a prepared generator and verify its version."""
    path = shutil.which(executable)
    if not path:
        raise ValueError(f"generator not found: {executable}")
    observed = subprocess.run([path, version_flag], check=True, capture_output=True, text=True)
    version = (observed.stdout.split() or ["unknown"])[-1]
    if version != expected:
        raise ValueError(f"{executable} reports {version}; expected {expected}")
    return path


def serialize_python_uuid_headers(source):
    """Keep UUID parameters typed and encode their header values for HTTPX."""
    if "headers[" not in source or "UUID" not in source:
        return source
    builder = next((node for node in ast.parse(source).body
                    if isinstance(node, ast.FunctionDef) and node.name == "_get_kwargs"), None)
    if builder is None:
        return source
    parameters = (*builder.args.posonlyargs, *builder.args.args, *builder.args.kwonlyargs)
    uuid_parameters = {
        arg.arg for arg in parameters if arg.annotation is not None
        and ast.unparse(arg.annotation) in {"UUID", "UUID | Unset", "Unset | UUID"}
    }
    lines = source.splitlines(keepends=True)
    for node in ast.walk(builder):
        match node:
            case ast.Assign(targets=[ast.Subscript(value=ast.Name(id="headers"))],
                            value=ast.Name(id=name) as value) if name in uuid_parameters:
                # AST offsets count UTF-8 bytes, including any non-ASCII header names.
                index = value.lineno - 1
                start, end = value.col_offset, value.end_col_offset
                line = lines[index].encode("utf-8")
                lines[index] = (line[:start] + b"str(" + line[start:end] + b")" + line[end:]).decode("utf-8")
    return "".join(lines)

def guard_go_audit_read(source):
    """Replace only the pinned audit-read dispatch, refusing an unreviewed generator shape."""
    matches = list(re.finditer(r"^func \(c \*Client\) ListAuditEvents\(", source, re.MULTILINE))
    if len(matches) != 1:
        raise ValueError("expected one generated ListAuditEvents method; review the pinned Go generator")
    start = matches[0].start()
    end = source.find("\n}", start)
    if end < 0:
        raise ValueError("generated ListAuditEvents method is incomplete")
    end += 2
    method = source[start:end]
    ordinary, guarded = "\treturn c.Client.Do(req)\n", "\treturn c.doAuditedRead(req)\n"
    if method.count(guarded) == 1 and ordinary not in method:
        return source
    if method.count(ordinary) != 1 or guarded in method:
        raise ValueError("generated ListAuditEvents dispatch changed; review audit-read transport protection")
    return source[:start] + method.replace(ordinary, guarded) + source[end:]


def generate_domain(source, temp, go_root, python_root, go_tool, python_tool):
    """Generate both clients from the same projected contract."""
    domain = source.parent.name.replace("-", "_")
    if not re.fullmatch(r"[a-z][a-z0-9_]*", domain):
        raise ValueError(f"cannot use {source.parent.name!r} as a package name")
    document, _ = read_contract(source)
    contract = project_contract(document)
    if not contract["paths"]:
        return False
    audited_read = contract["paths"].get("/api/v1/admin/audit-events", {}).get("get")
    named_reads = [(path, method) for path, item in contract["paths"].items()
                   for method, operation in item.items() if method in METHODS
                   and operation.get("operationId") == "listAuditEvents"]
    if (audited_read is not None or named_reads) and (
        domain != "identity" or audited_read is None
        or audited_read.get("operationId") != "listAuditEvents"
        or named_reads != [("/api/v1/admin/audit-events", "get")]
    ):
        raise ValueError("the published audit read moved or was renamed; review its SDK transport protection")
    local_contract = temp / f"{domain}.json"
    local_contract.write_text(json.dumps(contract, sort_keys=True), encoding="utf-8")

    go_config = temp / "go-config.json"
    go_config.write_text(json.dumps({
        "package": domain,
        "generate": {"models": True, "client": True},
        "output-options": {"response-type-suffix": "HTTPResponse"},
    }), encoding="utf-8")
    go_out = go_root / domain
    go_out.mkdir()
    with (go_out / "client.gen.go").open("wb") as output:
        subprocess.run([go_tool, "-config", str(go_config), str(local_contract)],
                       check=True, stdout=output)
    if audited_read is not None:
        client = go_out / "client.gen.go"
        client.write_text(guard_go_audit_read(client.read_text(encoding="utf-8")), encoding="utf-8")
        shutil.copyfile(TEMPLATES / "audit_read.go.template", go_out / "audit_read.gen.go")
    python_out = python_root / domain
    subprocess.run([
        python_tool, "generate", "--path", str(local_contract), "--meta", "none",
        "--config", str(TEMPLATES / "python-config.json"), "--fail-on-warning",
        "--output-path", str(python_out),
    ], check=True, stdout=sys.stderr)
    for path in python_out.rglob("*.py"):
        generated = path.read_text(encoding="utf-8")
        if path.relative_to(python_out).parts[0] == "api":
            generated = serialize_python_uuid_headers(generated)
        path.write_text("# Code generated by openapi-python-client. DO NOT EDIT.\n" + generated, encoding="utf-8")
    return True


def generate(destination, go_tool, python_tool):
    """Generate to a staging directory and publish only a complete output."""
    if destination.exists() or destination.is_symlink():
        raise ValueError(f"output already exists: {destination}; choose a new directory")
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".ownsolid-sdk-", dir=destination.parent) as directory:
        temp = Path(directory)
        staging = temp / "sdk"
        go_root = staging / "gen" / "go"
        python_root = staging / "gen" / "python" / "ownsolid_sdk"
        go_root.mkdir(parents=True)
        python_root.mkdir(parents=True)
        (python_root / "__init__.py").write_text(
            '# Code generated by ownsolid SDK generation. DO NOT EDIT.\n', encoding="utf-8"
        )
        count = 0
        for source in public_sources(CONTRACTS):
            count += generate_domain(source, temp, go_root, python_root, go_tool, python_tool)
        if count == 0:
            raise ValueError("no public OpenAPI contracts found")
        for filename in ("go.mod", "go.sum"):
            shutil.copyfile(TEMPLATES / f"{filename}.template", go_root / filename)
        shutil.copyfile(TEMPLATES / "streaming.py.template", python_root / "streaming.py")
        shutil.copyfile(TEMPLATES / "requirements.txt", python_root.parent / "requirements.txt")
        staging.rename(destination)
    return count


def main():
    """Run the pinned local generators against the repository contracts."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True, help="new destination directory")
    parser.add_argument("--go-generator", default="oapi-codegen")
    parser.add_argument("--python-generator", default="openapi-python-client")
    args = parser.parse_args()
    try:
        go_tool = checked_tool(args.go_generator, "-version", GO_VERSION)
        python_tool = checked_tool(args.python_generator, "--version", PYTHON_VERSION)
        count = generate(args.out.absolute(), go_tool, python_tool)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as exc:
        parser.exit(1, f"SDK generation failed: {exc}\n")
    print(f"Generated {count} Go and Python domain clients in {args.out}")


if __name__ == "__main__":
    main()
