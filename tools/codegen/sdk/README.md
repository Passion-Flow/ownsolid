# Generate the Go and Python SDKs

Build local source packages for the Admin, Portal and model gateway APIs.
The generator reads the versioned contracts and omits private machine
operations and the inherited knowledge contract. See the
[compatibility and dependency review](../../../docs/research/sdk.md).

## Prepare the generators

On a development machine with the Go and Python 3.12 toolchains, prepare the
pinned generators and a wheelhouse. Keep both available for a disconnected build.

The generator and runtime have distinct dependency locks. Use one download
invocation per lock.

```sh
GOWORK=off go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2
python3 -m pip download --only-binary=:all: --dest "$WHEELHOUSE" \
  --requirement tools/codegen/sdk/generator-requirements.txt
python3 -m pip download --only-binary=:all: --dest "$WHEELHOUSE" \
  --requirement tools/codegen/sdk/templates/requirements.txt
```

Use a Python 3.12 virtual environment for the generator:

```sh
"$GENERATOR_VENV/bin/python" -m pip install --no-index --find-links "$WHEELHOUSE" \
  --requirement tools/codegen/sdk/generator-requirements.txt
```

## Generate and use the SDKs

Choose an output directory that does not exist. The generator reads local
contracts and invokes the prepared tools by absolute path. Keep the prepared
Go toolchain on PATH as well: the pinned Go formatter queries go env -json,
and module resolution can query go list -m -e -json. These are metadata
queries, so a command allowlist for disconnected generation must account
for the fixed Go executable in addition to the two generator entrypoints.
The current Python post_hooks list is empty; it runs no Ruff or shell hook.
See the [process graph and resource boundary](../../../docs/research/sdk.md#file-only-generation-process-graph).

Source contracts must be JSON objects with unique member names at every depth.
Duplicate names and the nonstandard NaN/Infinity tokens fail with the owning
file in the error. The same rules apply to the Components alias owner. Correct
that source and retry; a rejected input publishes no partial SDK output.

```sh
python3 tools/codegen/sdk/generate.py --out "$SDK_DIR" \
  --go-generator "$GO_GENERATOR" \
  --python-generator "$GENERATOR_VENV/bin/openapi-python-client"
```

- Go modules are under `$SDK_DIR/gen/go`. Reference the local module
  `github.com/Passion-Flow/ownsolid/sdk/go` with a Go `replace` directive or a
  workspace. Prepare its Go module cache before transfer with `go mod download`
  inside the generated module. Verify with `GOWORK=off GOPROXY=off go test ./...`.
- Python modules are under `$SDK_DIR/gen/python/ownsolid_sdk`. Install the
  runtime requirements using `pip install --no-index --find-links "$WHEELHOUSE" -r "$SDK_DIR/gen/python/requirements.txt"`
  and add `$SDK_DIR/gen/python` to `PYTHONPATH`. Use a different virtual
  environment from the one that holds the generator.

Use a separate cookie jar per Admin or Portal origin. For a write, call the
identity client's `getCsrf` operation, then pass its token in `X-CSRF-Token`.
Configure the exact advertised origin in the `Origin` header. Supply fresh
CSRF data after a login, since the token is bound to the session. Model gateway
calls use the installation's gateway origin and its model access credential.

For Go, supply an `http.Client` using `WithHTTPClient`. Set `Transport.Proxy` to
`nil`, use `CheckRedirect` to refuse redirects, and attach a cookie jar and the
installation CA trust pool. Use `WithRequestEditorFn` to set the `Origin` header.
Reuse this transport when constructing multiple domain clients for one origin.

For Python, share one `httpx.Client` across domain clients using
`set_httpx_client`. For a cookie-authenticated operation whose generated type
expects `AuthenticatedClient`, set `token=""` and attach the transport that
holds the login cookie. For example,
`Nodes(base_url=origin, token="").set_httpx_client(transport)` where `Nodes`
is the nodes package's `AuthenticatedClient`. The attached HTTPX transport
supplies the session cookie.

Configure the Admin origin and its local CA in the
underlying transport:

```python
import os
import ssl

import httpx
from ownsolid_sdk.identity import Client
from ownsolid_sdk.identity.api.identity import get_csrf

origin = os.environ["OWNSOLID_SERVER"]
tls = ssl.create_default_context(cafile=os.environ["OWNSOLID_CA_FILE"])

with httpx.Client(
    base_url=origin,
    headers={"Origin": origin},
    trust_env=False,
    follow_redirects=False,
    verify=tls,
) as transport:
    identity = Client(base_url=origin).set_httpx_client(transport)
    response = get_csrf.sync_detailed(client=identity)
    print(response.status_code)
```

## Read the audit log deliberately

listAuditEvents records access before releasing its page. A 409
STORAGE_COMMIT_OUTCOME_UNKNOWN or a lost response leaves that access audit
uncertain. Do not automatically retry the read. An explicit retry with the same
filters, cursor and X-Ownsolid-Audit-Actor preference records a new access; it
neither replays the earlier operation nor proves that its audit was rolled back.
Keep the earlier request ID for diagnosis. Use category=audit for audit-family
events; exact action accepts only the published enumeration.

The generated Go method requires a standard *http.Client with a standard
*http.Transport (or its default). It clones that transport for one HTTP/1
request, disables connection reuse and refuses redirects. Installation CA/client
certificate configuration, the cookie jar and client timeout are retained;
the caller's shared client is unchanged. A general shared Go transport can
transparently replay an eligible GET after a network error on a reused
connection. Custom request executors and transports are rejected before this
read because their retry behavior cannot be inspected. Other operations keep
their configured executor. Disable application, proxy and load-balancer retries
for this path. Python's default HTTPX transport has zero connection retries;
retain retries=0, verified TLS, trust_env=False and follow_redirects=False when
configuring a transport. Keep any external retry wrapper disabled. Neither
generated client's 409 decoder starts a retry.
After regeneration, Go callers handle this status with
GetApplicationproblemJSON409(); it is no longer decoded through the default
Problem accessor. Python keeps the existing Problem model.

## Stream and download responses

For model gateway chat with `stream=True`, use the raw response adapter. It
preserves the request fields and gateway credential from the generated
operation and client. Check the status and media type before reading events.

```python
from ownsolid_sdk.model_gateway.api.default import create_scoped_chat_completion
from ownsolid_sdk.streaming import open_response

with open_response(create_scoped_chat_completion, client=gateway, body=chat) as response:
    response.raise_for_status()
    for line in response.iter_lines():
        print(line)
```

Use `open_async_response` with `async with` and `aiter_lines()` for an async
client. For downloads, read `iter_bytes()` after checking the status and
media type. In Go, use the generated method without the `WithResponse`
suffix to read or close the HTTP response body directly.

The Help projection tests also need the locked Web parser dependencies. In a
new worktree, prepare them from the local pnpm store first:

```sh
(cd web && pnpm install --offline --frozen-lockfile --ignore-scripts)
```

Run the projection checks with
`python3 -m unittest discover -s tools/codegen/sdk/tests`. With the SDK runtime
environment installed, run
`python tools/codegen/sdk/tests/wire_smoke.py "$SDK_DIR"` for the Go and Python
loopback checks. They exercise login, cookie reuse, typed node lists and a
binary dataset request with its declared media type. The same test verifies
Python streaming response ownership and error bodies. Audit-read checks exercise
Go's shared-connection replay control, lost replies, explicit retry, 409,
TLS/cookies, timeouts, redirects, custom-executor refusal and concurrency.
Python checks both sync and async audit reads after 409 or a lost reply with
standard and assistant representations. These loopback/controlled-transport
checks do not qualify server authorization, SQL commits or a browser flow.

## Audit the public operation inventory

Run `python3 tools/codegen/sdk/audit.py` from the repository root. The JSON
report lists public operations and flags missing operation IDs, duplicate
IDs within a document, and duplicate method and path declarations in the
same audience. Exit 1 reports findings and exit 2 indicates unreadable or
invalid input. A declared response media schema and each followed schema
reference must target an object or boolean. Invalid inputs name the owning
file, method and path and emit no partial report. An omitted media schema
remains valid; this is not a general OpenAPI or JSON Schema validator. The audit runs from local files and uses the same audience
rules as SDK generation. With `--baseline`, it also reports named response
model reference changes and closed response object shape changes for review.
The shape inventory records property names, required names and closure at local
schema pointers. It catches a new optional field in an existing closed model,
which named references alone cannot reveal. These findings can also describe a
wire-compatible change that changes generated source types. Property value
constraints, requests and effective composition remain outside this check;
an empty finding list does not establish general schema or behavior compatibility.
It measures the published contracts. Verification
against running handlers remains the responsibility of each API owner.
