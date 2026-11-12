"""Check old and updated generated alias clients without an external route."""

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from uuid import UUID

ROOT = Path(__file__).resolve().parent


def exercise_python(sdk, current):
    sys.path.insert(0, str(sdk / "gen/python"))
    import attrs
    import httpx
    from ownsolid_sdk.distribution import AuthenticatedClient
    from ownsolid_sdk.distribution.api.default import (
        admin_get_model_alias, admin_list_model_alias,
        portal_get_model_alias, portal_list_model_alias,
    )
    from ownsolid_sdk.distribution.models.model_alias import ModelAlias

    workspace = UUID("12345678-0000-4000-8000-000000000002")
    alias_id = UUID("12345678-0000-4000-8000-000000000001")
    alias = {
        "id": str(alias_id), "workspace_id": str(workspace),
        "gateway_id": "12345678-0000-4000-8000-000000000003",
        "node_id": "12345678-0000-4000-8000-000000000004",
        "deployment_id": "12345678-0000-4000-8000-000000000005",
        "name": "verified-alias", "client_origin": "https://gateway.example.invalid",
        "deployment_revision": 1, "revision": 1, "held_requests": 0,
        "disabled_at": None, "created_at": "2026-11-01T00:00:00Z",
        "available": True, "unavailable_code": None,
        "description": "", "description_revision": 1,
    }
    assert "capabilities" not in {field.name for field in attrs.fields(ModelAlias)}
    calls = 0
    for present in (False, True):
        value = dict(alias)
        if present:
            value["capabilities"] = {"text_generation_verified": True, "context_token_ceiling": 1024}
        for audience, detail, listing in (
            ("admin", admin_get_model_alias, admin_list_model_alias),
            ("portal", portal_get_model_alias, portal_list_model_alias),
        ):
            path = "/api/v1/sites/site-a/cells/cell-b/"
            path += (f"admin/distribution/workspaces/{workspace}" if audience == "admin"
                     else f"workspaces/{workspace}/distribution")
            path += "/aliases"

            def respond(request):
                nonlocal calls
                calls += 1
                assert request.method == "GET"
                assert request.url.path in (path, path + "/" + str(alias_id))
                if request.url.path == path:
                    assert dict(request.url.params) == {"limit": "1", "cursor": "original-cursor"}
                    data = {"items": [value], "next_cursor": "opaque-next"}
                else:
                    assert not request.url.query
                    data = value
                return httpx.Response(200, headers={"Content-Type": "application/json"}, json={"data": data})

            with httpx.Client(base_url="https://admin.example.invalid", transport=httpx.MockTransport(respond),
                              trust_env=False, follow_redirects=False) as transport:
                client = AuthenticatedClient(base_url="https://admin.example.invalid", token="").set_httpx_client(transport)
                result = detail.sync_detailed("site-a", "cell-b", workspace, alias_id, client=client)
                expected_type = "ModelAliasReadResponse" if current else "ModelAliasResponse"
                assert result.status_code == 200 and type(result.parsed).__name__ == expected_type
                item = result.parsed.to_dict()["data"]
                assert item["id"] == str(alias_id) and item["workspace_id"] == str(workspace)
                assert item["deployment_revision"] == 1 and item["description"] == ""
                assert ("capabilities" in item) == (current and present)
                if current and present:
                    assert item["capabilities"] == value["capabilities"]

                result = listing.sync_detailed("site-a", "cell-b", workspace, client=client,
                                               limit=1, cursor="original-cursor")
                assert result.status_code == 200 and type(result.parsed).__name__ == "ModelAliasPageResponse"
                page = result.parsed.to_dict()["data"]
                assert page["next_cursor"] == "opaque-next" and len(page["items"]) == 1
                assert page["items"][0] == item
    assert calls == 8
    print(f"Python {'updated' if current else 'released'} alias read clients preserved presence/omission and cursor")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("baseline", type=Path)
    parser.add_argument("candidate", type=Path)
    parser.add_argument("--python-only", choices=("baseline", "candidate"))
    args = parser.parse_args()
    if args.python_only:
        path = args.candidate if args.python_only == "candidate" else args.baseline
        exercise_python(path.resolve(), args.python_only == "candidate")
        return
    for variant, sdk in (("baseline", args.baseline.resolve()), ("candidate", args.candidate.resolve())):
        subprocess.run([sys.executable, __file__, str(args.baseline), str(args.candidate),
                        "--python-only", variant], check=True)
        go = sdk / "gen/go"
        with tempfile.TemporaryDirectory(prefix="alias-capability-", dir=go) as directory:
            test = Path(directory)
            shutil.copyfile(ROOT / "testdata/alias_capability_wire.go", test / "main.go")
            subprocess.run(["go", "run", "./" + test.name, variant], cwd=go, check=True,
                           env={**os.environ, "GOWORK": "off", "GOPROXY": "off"})


if __name__ == "__main__":
    main()
