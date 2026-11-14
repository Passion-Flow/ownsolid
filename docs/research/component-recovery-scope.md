# Component recovery fixture scope

ARC-004 made site and cell part of every node request. The Linux component
recovery gate at `aa5e712f` fails when the first recovered heartbeat reaches its
TLS server at `/agent/v1/sites//cells//nodes/<id>/heartbeat`. The fake client is
built directly from the test server's transport and the node credential. It
omits the enrolled identity's scope. The production `Run` entry point loads
that identity and sets both credential and scope before running any cycle.

Use the identity's scope when constructing the test client, as production does.
Keep the fixture's exact TLS route, credential, recovery order and disabled-claim
assertions. Accepting an unscoped path in the handler would hide the fault.
Defaulting missing scope in production would defeat the ownership contract in
[ADR 0006](../decisions/0006-cell-and-site-scope.md).

The existing [Go TLS test server](https://pkg.go.dev/net/http/httptest#NewTLSServer)
exercises the actual HTTP path over loopback. Its Go standard library
[BSD-3-Clause license](https://go.dev/LICENSE) permits commercial use,
redistribution with notice and offline use. The fix adds no dependencies or
datasets. The Linux amd64 run must cover a failed recovery that sends no
heartbeat, the recovered disabled heartbeat without a claim, and the recovered
active heartbeat followed by one claim.

On Linux amd64, in a network namespace with only loopback, the focused test
passed 20 consecutive runs. All 43 top-level tests in the runtime package
also passed using the repository's environment schema fixture.
