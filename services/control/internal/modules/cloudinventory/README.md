# AWS inventory discovery and import

This owner discovers resources through configured AWS connections and persists scoped import receipts. It does not provision compute, change cloud permissions or turn a discovered instance into an enrolled node.

[provider.go](provider.go) owns signed provider calls and response validation, including account identity and EC2 inventory. Its transport policy constrains timeouts, redirects and endpoint handling. [service.go](service.go) admits List, Import and Receipt through current identity/Workspace authority. The HTTP adapter exposes these operations without allowing arbitrary provider credentials or endpoints from a browser request.

Historical platform migration000022 introduced discovered resources and import receipts.
Current deployments must use their selected complete catalog and role/restore
admission, rather than apply that historical file independently. Connection, account, region, Workspace and idempotency identity must remain bound across provider work and the selected SQL commit. An unsuccessful or uncertain provider/commit outcome is not a completed import; retain the original receipt and reconciliation semantics.

The owning configuration is control AWSConfiguration with protected credential references and [AWS examples](../../../../../config/examples/aws/README.md). Keep [M27 requirements](../../../../../docs/milestones/m27-aws/README.md), [AWS deployment assets](../../../../../deploy/aws), [HTTP contracts](../../../../../contracts/http), provider tests and selected-database integration cases synchronized. UI consumers must use these validated choices rather than introduce their own cloud authority.

Focused native check: `go -C services/control test ./internal/modules/cloudinventory`. Provider doubles and SQL fixtures verify their declared local profile. Real-account identity, permissions, endpoints and regional behavior remain separate qualification; running this package's tests does not authorize cloud mutations or expenditure.

The [native quality gate](../../../../../tools/quality/aws/README.md) now has
three actual PostgreSQL repository leaves accepted across immutable source-bound
runs. The first failed parent is retained; only its corrected fixture leaf was
replayed in a fresh disposable scope. Provider responses and acknowledgement loss
are explicit test doubles around actual SQL. The existing original HTTP root separately passes actual bootstrap/dual TLS
login and disabled/offline-read/refusal behavior; its first failed fixture is
retained, with production unchanged. The distinct enabled HTTP scope below is
separately accepted; full serve/browser, MySQL/native SQL and live-account qualification
remain pending. See
[HTTP evidence](../../../../../docs/execution/mvp/m27-http-native.md). See [execution](../../../../../docs/execution/mvp/resume-current.md).

The distinct [enabled HTTP cases](../../../../../docs/milestones/m27-aws/enabled-http-cases.json)
drive the original dispatcher and unchanged provider with an explicitly scoped
loopback TLS protocol fixture. Only the nonparallel test process replaces its
default transport; logical HTTPS/Host/SNI and ordinary CA/SAN verification remain.
Synthetic certificate/page/audit faults exercise refusal and durable rollback.
All three distinct actual native leaves now pass with independent raw/source/SQL/
cleanup readback; see [exact evidence and limits](../../../../../docs/execution/mvp/m27-enabled-http-native.md).
This does not establish live AWS/full serving/browser qualification. The accepted
repository/disabled cases were not replayed; original receipts retain their exact
source bytes, including the earlier pending README annotation.

Current parallel repairs and evidence boundaries are recorded in the
[M27 checkpoint](../../../../../docs/milestones/m27-aws/parallel-20261103.md).
The STS principal ARN is checked against both configured partition and account.
Unknown selected dialects refuse before License/SQL admission. The current selected
repository source explicitly handles PostgreSQL, MySQL and native Dameng;
[repository.go](repository.go) owns their SQL and the native scoped update/insert.
It borrows the same Identity/Workspace transaction and does not open or migrate
another store. Central exact catalog/runtime-role admission remains mandatory
before composition exposes this owner. Current source availability does not
establish actual MySQL/native execution or whole-platform qualification. After all request
consumers exit, the serving owner should call Provider.Close to release its owned
idle HTTP connections; it creates no scheduler or cloud resource.

## Selected repository SQL verification

The source dispatch is closed over PostgreSQL/MySQL/Dameng. PostgreSQL and MySQL
keep their original nine SQL literal pairs. Dameng uses explicit uppercase native
identifiers, direct CLOB values, numeric BIT projection, UTC microsecond audit and
bounded page parameters. Its exclusive Workspace transaction updates the exact
scoped instance first and inserts only on zero matched rows, retaining existing
resource IDs. Any update/insert failure returns immediately; no fallback or
independent commit is permitted.

The default tests check dispatch, same Identity/pool/executor boundaries, original
License/transaction admission and native upsert failure/argument behavior. They do
not execute vendor SQL. The explicitly tagged actual SQL tests are
[selected_repository_integration_test.go](selected_repository_integration_test.go)
and [native_repository_integration_test.go](native_repository_integration_test.go).
They verify actual scoped CLOB/BIT/page/ID/receipt/audit/rollback/immutability, not
public identity/License/browser/serving or real AWS acceptance. See the current
[run procedure and central prerequisites](../../../../../docs/milestones/m27-aws/selected-integration.md).

Commit classification uses the shared bounded transaction.CommitOutcome helper
before ordinary timeout/storage normalization. A joined timeout/cleanup failure
cannot hide an unknown commit or proven rollback. Joined results expose only the
safe classification; retain the original import key and use receipt reconciliation
rather than automatically create a new mutation identity.
