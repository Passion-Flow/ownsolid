"""Exercise both generated clients over loopback without an installation."""

import argparse
import asyncio
from datetime import datetime, timezone
from hashlib import sha256
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from io import BytesIO
import json
from pathlib import Path
import os
import secrets
import subprocess
import sys
import threading
from uuid import uuid4


def exercise(sdk):
    sys.path.insert(0, str(sdk / "gen" / "python"))
    import httpx
    from ownsolid_sdk.datasets import AuthenticatedClient as Datasets
    from ownsolid_sdk.datasets.api.default import admin_upload_dataset_source
    from ownsolid_sdk.datasets.types import File
    from ownsolid_sdk.identity import Client as Identity
    from ownsolid_sdk.identity.api.identity import get_csrf, login
    from ownsolid_sdk.identity.models.login_request import LoginRequest
    from ownsolid_sdk.nodes import AuthenticatedClient as Nodes
    from ownsolid_sdk.nodes.api.default import list_admin_nodes

    email = "sdk-smoke@example.invalid"
    password, session, csrf, context = (secrets.token_urlsafe(32) for _ in range(4))
    node_path = "/api/v1/sites/default/cells/default/admin/nodes"
    dataset_id, draft_id = uuid4(), uuid4()
    dataset_path = f"/api/v1/sites/default/cells/default/admin/datasets/{dataset_id}/drafts/{draft_id}/source"
    payload = b'{"text":"synthetic fixture"}\n'
    stamp = datetime.now(timezone.utc).isoformat()

    class API(BaseHTTPRequestHandler):
        def log_message(self, _format, *_args):
            return

        def send_json(self, status, data, cookie=None, content_type="application/json"):
            body = json.dumps(data).encode()
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            if cookie:
                self.send_header("Set-Cookie", cookie + "; Path=/; HttpOnly")
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == "/api/v1/auth/csrf":
                self.send_json(200, {"data": {"token": csrf, "expires_at": stamp}}, f"csrf_context={context}")
            elif self.path == node_path + "?limit=3" and f"admin_session={session}" in self.headers.get("Cookie", ""):
                self.send_json(200, {"data": {"items": [], "next_cursor": None}})
            else:
                self.send_error(404)

        def do_POST(self):
            if self.headers.get("Origin") != origin or self.headers.get("X-CSRF-Token") != csrf:
                self.send_error(400, "origin or CSRF header differs")
                return
            body = self.rfile.read(int(self.headers["Content-Length"]))
            cookies = self.headers.get("Cookie", "")
            if self.path == "/api/v1/auth/login" and f"csrf_context={context}" in cookies:
                if json.loads(body) != {"email": email, "password": password}:
                    self.send_error(400, "unexpected login body")
                    return
                self.send_json(200, {"data": {
                    "audience": "admin", "workspace": None,
                    "session": {"absolute_expires_at": stamp, "idle_timeout_seconds": 60},
                    "user": {
                        "id": str(uuid4()), "email": email, "display_name": "SDK Smoke", "locale": "en",
                        "status": "active", "is_platform_admin": True, "email_verified_at": None,
                        "version": 1, "created_at": stamp, "updated_at": stamp,
                    },
                }}, f"admin_session={session}")
            elif self.path == dataset_path and f"admin_session={session}" in cookies:
                if (body != payload or self.headers.get("Content-Type") != "application/x-ndjson"
                        or self.headers.get("X-Content-SHA256") != sha256(payload).hexdigest()):
                    self.send_error(400, "binary request did not preserve its media type or body")
                    return
                self.send_json(403, {
                    "type": "about:blank", "title": "Access denied", "status": 403,
                    "code": "ACCESS_DENIED", "request_id": str(uuid4()),
                }, content_type="application/problem+json")
            else:
                self.send_error(404)

    with ThreadingHTTPServer(("127.0.0.1", 0), API) as server:
        origin = f"http://{server.server_name}:{server.server_port}"
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            with httpx.Client(base_url=origin, headers={"Origin": origin}, trust_env=False, timeout=10) as http:
                identity = Identity(base_url=origin).set_httpx_client(http)
                token = get_csrf.sync_detailed(client=identity)
                assert token.status_code == 200 and token.parsed.data.token == csrf
                result = login.sync_detailed(client=identity, body=LoginRequest(email=email, password=password),
                                             x_csrf_token=token.parsed.data.token)
                assert result.status_code == 200 and result.parsed.data.audience.value == "admin"
                nodes = Nodes(base_url=origin, token="").set_httpx_client(http)
                result = list_admin_nodes.sync_detailed("default", "default", client=nodes, limit=3)
                assert result.status_code == 200 and result.parsed.data.items == []
                token = get_csrf.sync_detailed(client=identity)
                datasets = Datasets(base_url=origin, token="").set_httpx_client(http)
                result = admin_upload_dataset_source.sync_detailed(
                    "default", "default", dataset_id, draft_id, client=datasets,
                    body=File(BytesIO(payload)), origin=origin, x_csrf_token=token.parsed.data.token,
                    x_content_sha256=sha256(payload).hexdigest(), content_length=len(payload),
                )
                assert result.status_code == 403 and result.parsed.code == "ACCESS_DENIED"

            subprocess.run(
                ["go", "run", str(Path(__file__).with_name("wire.go").resolve())],
                cwd=sdk / "gen" / "go",
                env={**os.environ, "GOWORK": "off", "GOPROXY": "off"},
                input=json.dumps({
                    "Origin": origin, "Email": email, "Password": password, "DatasetID": str(dataset_id),
                    "DraftID": str(draft_id), "Payload": payload.decode(),
                }),
                text=True, check=True, timeout=120,
            )
        finally:
            server.shutdown()
            thread.join()
    print("Go and Python SDKs verified CSRF, login, session cookies, node lists and NDJSON requests")


def exercise_streaming():
    import httpx
    from ownsolid_sdk.model_gateway import AuthenticatedClient
    from ownsolid_sdk.model_gateway.api.default import create_scoped_chat_completion
    from ownsolid_sdk.model_gateway.models.chat_request import ChatRequest
    from ownsolid_sdk.streaming import open_async_response, open_response

    token = secrets.token_urlsafe(32)
    chunks_read = []
    closed = []

    class Events(httpx.SyncByteStream, httpx.AsyncByteStream):
        def __iter__(self):
            chunks_read.append("first")
            yield b'data: {"choices":[]}\n\n'
            chunks_read.append("last")
            yield b"data: [DONE]\n\n"

        async def __aiter__(self):
            for chunk in self:
                yield chunk

        def close(self):
            closed.append(True)

        async def aclose(self):
            self.close()

    def respond(request):
        assert request.method == "POST" and request.url.path == "/v1/chat/completions"
        assert request.headers["Authorization"] == f"Bearer {token}"
        body = json.loads(request.content)
        assert body["stream"] is True
        if body["model"] == "unavailable":
            return httpx.Response(403, json={"error": {"message": "forbidden"}})
        return httpx.Response(200, headers={"Content-Type": "text/event-stream"}, stream=Events())

    gateway = AuthenticatedClient(
        base_url="https://gateway.example.invalid",
        token=token,
        httpx_args={"transport": httpx.MockTransport(respond), "trust_env": False},
    )
    body = ChatRequest.from_dict({
        "model": "fixture", "messages": [{"role": "user", "content": "hello"}], "stream": True,
    })
    with gateway:
        with open_response(create_scoped_chat_completion, client=gateway, body=body) as response:
            assert response.status_code == 200 and response.headers["Content-Type"] == "text/event-stream"
            assert not chunks_read
            assert next(response.iter_lines()) == 'data: {"choices":[]}'
            assert chunks_read == ["first"]
        assert closed == [True]

    async def check_async():
        async with gateway:
            async with open_async_response(create_scoped_chat_completion, client=gateway, body=body) as response:
                assert chunks_read == ["first"]
                assert await anext(response.aiter_lines()) == 'data: {"choices":[]}'
                assert chunks_read == ["first", "first"]
            assert closed == [True, True]

            body.model = "unavailable"
            async with open_async_response(create_scoped_chat_completion, client=gateway, body=body) as response:
                assert response.status_code == 403
                await response.aread()
                assert response.json() == {"error": {"message": "forbidden"}}

    asyncio.run(check_async())
    print("Python SDK verified incremental SSE, early close, and error responses")


def exercise_monitoring_problems():
    import httpx
    from ownsolid_sdk.monitoring import AuthenticatedClient
    from ownsolid_sdk.monitoring.api.default import admin_fleet_history

    origin = "https://admin.example.invalid"
    workspace = uuid4()
    start, end = "2026-11-01T00:00:00Z", "2026-11-01T00:01:00Z"
    for code in (
        "STORAGE_TIMEOUT", "STORAGE_CANCELLED", "STORAGE_COMMIT_ROLLED_BACK",
        "STORAGE_DEADLOCK", "STORAGE_LOCK_TIMEOUT",
    ):
        def respond(request):
            assert request.method == "GET"
            assert request.url.path == "/api/v1/sites/default/cells/default/admin/monitoring/fleet-history"
            assert dict(request.url.params) == {"workspace_id": str(workspace), "from": start, "to": end}
            return httpx.Response(503, headers={"Content-Type": "application/problem+json"}, json={
                "type": "about:blank", "title": "Synthetic transaction refusal",
                "status": 503, "code": code, "request_id": str(uuid4()),
            })

        with httpx.Client(base_url=origin, transport=httpx.MockTransport(respond),
                          trust_env=False, follow_redirects=False) as transport:
            client = AuthenticatedClient(base_url=origin, token="").set_httpx_client(transport)
            result = admin_fleet_history.sync_detailed(
                "default", "default", client=client, workspace_id=workspace, from_=start, to=end,
            )
            assert result.status_code == 503 and result.parsed.code == code
    print("Python SDK verified history transaction refusals and canonical timestamps")


def exercise_audit_reads_python(sdk):
    """Check the generated operation, without a retrying HTTP or application wrapper."""
    sys.path.insert(0, str(sdk / "gen" / "python"))
    import httpx
    from ownsolid_sdk.identity import AuthenticatedClient
    from ownsolid_sdk.identity.api.administration import list_audit_events
    from ownsolid_sdk.identity.models.list_audit_events_x_ownsolid_audit_actor import ListAuditEventsXOwnsolidAuditActor
    from ownsolid_sdk.identity.models.problem import Problem

    selection = {"category": "audit", "cursor": "opaque+/=%", "limit": 2}
    code = "STORAGE_COMMIT_OUTCOME_UNKNOWN"

    def scenario(assistant, lost_reply):
        calls = []

        def respond(request):
            calls.append(request)
            assert request.method == "GET" and request.url.path == "/api/v1/admin/audit-events"
            assert dict(request.url.params) == {key: str(value) for key, value in selection.items()}
            assert request.content == b"" and "Idempotency-Key" not in request.headers
            assert request.headers.get("X-Ownsolid-Audit-Actor") == ("true" if assistant else None)
            if len(calls) == 1:
                if lost_reply:
                    raise httpx.ReadError("synthetic lost reply", request=request)
                return httpx.Response(409, headers={"Content-Type": "application/problem+json"}, json={
                    "type": "about:blank", "title": code, "status": 409, "code": code,
                    "request_id": "33333333-3333-4333-8333-333333333333",
                })
            return httpx.Response(200, json={"data": {"items": [], "next_cursor": None}})

        client = AuthenticatedClient(
            base_url="https://admin.example.invalid", token="",
            httpx_args={"transport": httpx.MockTransport(respond), "trust_env": False},
        )
        params = dict(selection)
        if assistant:
            params["x_ownsolid_audit_actor"] = ListAuditEventsXOwnsolidAuditActor.TRUE
        return client, params, calls

    def check_first(result, calls):
        assert len(calls) == 1 and result.status_code == 409
        assert isinstance(result.parsed, Problem) and result.parsed.code == code

    def check_second(result, calls):
        assert len(calls) == 2 and result.status_code == 200
        assert result.parsed.data.items == [] and result.parsed.data.next_cursor is None

    async def check_async(assistant, lost_reply):
        client, params, calls = scenario(assistant, lost_reply)
        async with client:
            if lost_reply:
                try:
                    await list_audit_events.asyncio_detailed(client=client, **params)
                except httpx.ReadError:
                    assert len(calls) == 1
                else:
                    raise AssertionError("a lost reply became success")
            else:
                check_first(await list_audit_events.asyncio_detailed(client=client, **params), calls)
            check_second(await list_audit_events.asyncio_detailed(client=client, **params), calls)

    for assistant in (False, True):
        for lost_reply in (False, True):
            client, params, calls = scenario(assistant, lost_reply)
            with client:
                if lost_reply:
                    try:
                        list_audit_events.sync_detailed(client=client, **params)
                    except httpx.ReadError:
                        assert len(calls) == 1
                    else:
                        raise AssertionError("a lost reply became success")
                else:
                    check_first(list_audit_events.sync_detailed(client=client, **params), calls)
                check_second(list_audit_events.sync_detailed(client=client, **params), calls)
            asyncio.run(check_async(assistant, lost_reply))
    print("Python SDK verified eight audit-read failure/manual-retry scenarios")


def exercise_audit_reads_go(sdk):
    """Run the actual generated Go operation against loopback protocol fixtures."""
    subprocess.run(
        ["go", "test", "-mod=readonly", "-race", "-count=1",
         str(Path(__file__).with_name("audit_read_wire_test.go").resolve()),
         str(Path(__file__).with_name("audit_read_legacy_test.go").resolve())],
        cwd=sdk / "gen" / "go",
        env={**os.environ, "GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off",
             "GOTOOLCHAIN": "local", "GOVCS": "*:off"},
        check=True, timeout=120,
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("sdk", type=Path)
    sdk = parser.parse_args().sdk.resolve()
    exercise(sdk)
    exercise_streaming()
    exercise_monitoring_problems()
    exercise_audit_reads_python(sdk)
    exercise_audit_reads_go(sdk)
