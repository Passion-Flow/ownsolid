"""Build a private test overlay from the GW owner's exact alias-read handoff."""

import argparse
import hashlib
import json
from pathlib import Path


SOURCE = {
    "alias_capabilities.go", "alias_reads.go", "history.go", "http/human.go", "model.go",
}
TESTS = {
    "alias_capabilities_test.go", "alias_capability_recipe_test.go",
    "alias_capability_http_test.go", "alias_read_http_test.go",
}
PREFIX = "services/control/internal/modules/distribution/"


def prepare(repo, handoff, output):
    """Verify the owner's files and add stricter assertions without editing them."""
    repo, handoff, output = repo.resolve(), handoff.resolve(), output.absolute()
    contract = json.loads((handoff / "contract.json").read_text())
    expected_source = {PREFIX + name for name in SOURCE}
    expected_tests = {PREFIX + name for name in TESTS}
    if set(contract["source_paths"]) != expected_source or set(contract["test_paths"]) != expected_tests:
        raise ValueError("the alias-read handoff has an unexpected source boundary")
    if contract["production_default"] is not False:
        raise ValueError("the source handoff must retain the disabled publication default")
    verified = {}
    for line in (handoff / "source-manifest.sha256").read_text().splitlines():
        digest, relative = line.split("  ", 1)
        file = (handoff / relative).resolve()
        if not file.is_relative_to(handoff) or hashlib.sha256(file.read_bytes()).hexdigest() != digest:
            raise ValueError("handoff checksum or path is invalid: " + relative)
        verified[relative] = digest
    for relative in expected_source | expected_tests:
        if "source/" + relative not in verified:
            raise ValueError("the handoff did not checksum " + relative)
    output.mkdir(parents=True, exist_ok=False)
    replacements = {
        str(repo / relative): str(handoff / "source" / relative)
        for relative in sorted(expected_source | expected_tests)
    }
    relative = PREFIX + "alias_read_http_test.go"
    original = (handoff / "source" / relative).read_text()
    detail = "\t\t// The draft adds exactly one optional closed object to the released read."
    page = '\t\t\tresult := client.Call(http.MethodGet, next, nil, http.StatusOK, "")'
    if original.count(detail) != 1 or original.count(page) != 1:
        raise ValueError("the owner's assertion anchors changed; review the fixture before composing")
    augmented = original.replace(detail, """\t\tif err := schemas["ModelAliasRead"].Validate(value); err != nil {
			t.Fatal("actual read does not match the candidate capability schema", err)
		}
""" + detail)
    augmented = augmented.replace(page, page + """
			if err := schemas["ModelAliasPageResponse"].Validate(map[string]any{"data": result}); err != nil {
				t.Fatal("actual page does not match the candidate capability schema", err)
			}""")
    extra = output / "alias_read_http_test.go"
    extra.write_text(augmented)
    replacements[str(repo / relative)] = str(extra)
    replacements[str(repo / PREFIX / "lane_rel_alias_schema_test.go")] = str(
        repo / "tools/codegen/sdk/tests/testdata/alias_capability_schema_test.go"
    )
    (output / "overlay.json").write_text(json.dumps({"Replace": replacements}, indent=2) + "\n")
    (output / "provenance.json").write_text(json.dumps({
        "base": contract["base"],
        "gateway_tree": contract["gateway_tree"],
        "owner_files": verified,
        "additional_assertions": ["ModelAliasRead", "ModelAliasPageResponse"],
        "augmented_test_sha256": hashlib.sha256(extra.read_bytes()).hexdigest(),
        "contract_sha256": hashlib.sha256((repo / "api/openapi/v1/distribution/openapi.json").read_bytes()).hexdigest(),
    }, indent=2) + "\n")
    return output / "overlay.json"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--handoff", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    print(prepare(args.repo, args.handoff, args.out))


if __name__ == "__main__":
    main()
