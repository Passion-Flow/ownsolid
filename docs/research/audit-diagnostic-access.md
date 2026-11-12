# AUD-108 diagnostic read access

## Sources and chosen boundary

[OWASP logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
places event capture with the application that knows the actor and operation,
requires protection and monitoring of log reads, and excludes tokens, passwords
and keys from log payloads. [Replicated Troubleshoot redactors](https://troubleshoot.sh/docs/redact/redactors/)
separate collection and disclosure rules. ownsolid already uses typed support
projections rather than copying arbitrary configuration or subprocess output.
Keep that existing approach; access accounting and redaction are separate
requirements. This is a source inspection and integration specification, not a
new runtime acceptance result.

## Existing main paths

| Path and owner | Present controls | Remaining AUD-108 work |
| --- | --- | --- |
| [License Diagnostics](../../services/control/internal/modules/licensing/diagnostics.go), `licensing.Service.Diagnostics` | Current Admin authorization, bounded typed state JSON, `license.diagnostics_exported` through `audit.WriteWithin` in the same transaction, and an empty download on any transaction error | Preserve this implemented path. Its read consumer is outside the SCI-owned audit directory. |
| [Admin audit HTTP projection](../../services/control/internal/modules/identity/http/audit_list.go), `Handler.listAuditEvents`, and [listForActor](../../services/control/internal/modules/identity/lists.go) | Session/Admin recheck, bounded list plan and optional verified assistant projection. Workspace reads use `ListWorkspaceAuditForActor` and its installation-tenant workspace check | Installation-wide reads now append in the identity transaction. Exact workspace reads still need the scoped writer and IAM consumer pairing. Preserve the current public representation and filters. |
| [Local support bundle](../../deploy/offline/internal/install/support.go), `SupportBundle` and `publishSupportBundle` | Typed configuration/state projection, a private archive, fixed collection issues and collection while Control is down | Admit and durably record the diagnostic access through the installer's existing operator mechanism before protected collection. The current path collects and publishes without such an access record. |
| [Managed support bundle](../../deploy/offline/internal/install/support_cluster.go), `ClusterSupportBundle` and `collectManagementSupport` | Protected independent credentials, bounded named HTTPS reads, response scope checks, fixed issues and typed projection | The installer must account for this collection; Control and Kubernetes owners retain authorization and source access accounting. A collector credential file does not identify a browser actor. |
| [Audit package](../../services/control/internal/modules/audit/repository.go), `WriteWithin` and `WriteAnonymousWithin` | Same-transaction append and exactly-one-row confirmation for the owning SQL dialect | This package owns no diagnostic reader, HTTP handler or collector lifecycle. An additional unused writer or local access ledger would not account for any of the reads above. |

The existing `TestM20LicenseUnknownHTTPAndExplicitRecovery` in
[the license HTTP repository tests](../../services/control/internal/modules/identity/http/license_repository_integration_test.go)
includes diagnostic COMMIT-not-forwarded and suppressed-ACK cases. It requires
no successful diagnostic payload on uncertainty and an additional access event
on an explicit later download. The existing license integration tests also
cover the ordinary user's refusal and a revoked Admin session. These are
existing test sources; this review does not claim a new execution of them.

[Support operation history](../../deploy/offline/internal/install/support_operations.go)
is a bounded projection of existing backup/restore completion records. It is
not an access receipt for creating a support bundle. The current CLI dispatch
calls the support functions directly. Existing redaction tests and the
[retained disconnected support evidence](support-diagnostics.md) remain valid
for their recorded scope and do not prove diagnostic access accounting.

## Required owner integration

Identity now places its installation-wide read-access event in `listForActor`'s
current transaction after authorization and before the result leaves the service.
The exact workspace consumer must add its scoped event at the same call point.
Use a fixed action and only the necessary server-derived scope, actor,
request ID and bounded read-selection metadata. Do not copy result rows,
reason text, credentials, raw query strings or driver errors into that event.
Keep unauthenticated/denied attempts distinct from completed diagnostic reads.
A failed append or unknown commit must release no sensitive response.

`audit.WriteWithin` currently resolves the installation tenant and default
workspace itself. It does not accept a requested workspace. An owner must not
use it to claim arbitrary workspace-read coverage. Exact workspace accounting
needs the real consumer's server-resolved transaction and scoped append
contract. `WriteProductAssistantWithin` is specifically an assistant actor
contract, not a diagnostic-user replacement. A new scoped writer should land
with that actual consumer rather than exist without a call site.

The installer must retain the Control-down collection path. Extend its existing
protected operator operation mechanism at `SupportBundle` and
`ClusterSupportBundle`, with a durable admitted-read record before collection
and a terminal outcome bound to the produced archive. The current
[`operation.record`](../../deploy/offline/internal/install/backup.go) is a
post-operation warning-only append, with no returned error or durability sync.
Calling it unchanged cannot enforce the required pre-read receipt. The delivery
owner must provide a failure-propagating durable admission operation. Use the actual local
operator identity; do not fabricate a SQL event or browser session. The owner
must define explicit reconciliation into the existing audit trail when Control
returns, including duplicate/retry behavior. A receipt write failure must stop
protected collection. No SCI-owned side ledger is selected.

For each consumer, first verify current authorization and resource scope, then
project the permitted fields before publication. Preserve private file handling,
size/time limits, cancellation and partial-source collection issues. An explicit
retry is a new read attempt with its own correlation, not evidence that an
uncertain prior access never occurred.

## Scoped append contract

The [identity consumer](identity-audit-read-access.md#chosen-approach) now
accounts for the installation-wide Admin read. Its explicit workspace reader
already holds current Admin/session and installation-workspace authority in
one transaction and needs an append bound to that exact workspace.

Add `audit.WriteScopedWithin(ctx, tx, ScopedEvent)` for PostgreSQL human Admin
operations. The server supplies actor, installation tenant, authorized workspace,
local site/cell, request ID, fixed action/outcome, optional target and bounded
server-generated reason. This is a trusted internal operation, not an HTTP input
or authentication provider. The caller retains current session/epoch and
operation authorization locks through commit and propagates every append error.
The first consumer remains `identity.ListWorkspaceAuditForActor`; IAM owns its
registration and its permitted result representation.

Use one guarded `INSERT ... SELECT`, following
[PostgreSQL INSERT](https://www.postgresql.org/docs/18/sql-insert.html).
Match the bootstrapped installation's tenant, actual workspace ownership,
active platform Admin, and the single local cell's site and ID. Insert the
matched database identities explicitly. The snapshot behavior described by
[PostgreSQL transaction isolation](https://www.postgresql.org/docs/18/transaction-iso.html)
does not replace the consumer's current authority locks. Keep disabled workspace
history readable under the existing Admin policy; readiness for new jobs is
not the policy for this historical read.

Return the same `AUDIT_SCOPE_DENIED` for a well-formed missing or foreign scope
or actor. Malformed fields return `AUDIT_SCOPED_EVENT_INVALID` before SQL.
Require canonical nonzero UUIDs, the existing scope-name syntax, bounded ASCII
action/target codes, a valid outcome and at most 500 UTF-8 reason bytes without
control characters. Refuse a pool in place of a borrowed transaction and refuse
unsupported dialects. No independent connection, commit, retry, new table,
migration, metadata, credential or captured content is introduced.

The serving role needs only append and the named authority-column reads, not
private chain-head access or source mutation privileges. Existing database
triggers assign positions, hashes and V2 commitments and enforce local head
scope. Exactly one inserted row confirms append; no match denies, and an
unexpected affected-row count remains unconfirmed. Cancellation and database
errors propagate to the transaction owner. An append success is not a commit
acknowledgment; a wire-level uncertain commit must remain uncertain and must
not automatically replay.

Verify the real PostgreSQL path with a non-default workspace under the current
production default/default schema gate. A separate operator-seeded empty-genesis
SQL fixture tests non-default site/cell attribution without claiming that
non-default installations are supported. Independently check stored scope,
minimal payload and chain continuity. Cover foreign/missing actor, tenant,
workspace, site and cell with identical denial, an inactive Admin, a non-Admin,
restricted column grants without private-head access, cancellation, insert
refusal and repair, explicit rollback, concurrent appends and real dropped
COMMIT/ACK outcomes. Keep the caller's session/forged-context tests and the
actual scoped read pairing with IAM. No standalone append test completes
AUD-108, and no unchanged audit58 or model qualification is repeated.

No new dependency, model or dataset is added. Existing Go BSD-3-Clause, pgx MIT,
Google UUID BSD-3-Clause and PostgreSQL License terms permit commercial use,
redistribution and offline operation with their existing notices. PostgreSQL
and OWASP are design references. No upstream implementation is copied.

## Acceptance and unblock conditions

Exercise the real diagnostic download or collector, not a direct append probe.
Verify redaction with sentinel credentials and private source values; audit
failure and unknown commit must emit no protected response. Cover revoked
sessions, denied callers, foreign tenant/workspace selections, bounded selection
metadata, cancellation, concurrent reads, explicit retry and restart readback.
For the installer, include Control and Docker down, an unwritable access receipt,
a failed archive write and reconciliation after recovery. Check both the private
archive and existing operation/audit records independently.

No dependency, model or dataset is introduced. Existing Go standard-library
BSD-3-Clause terms, offline use and redistribution notices remain unchanged;
OWASP and Troubleshoot are references, not embedded code. The audit module has
no missing owned read consumer to implement within the current SCI allocation.
Identity owns the exact workspace read integration; delivery owns both support
collection entry points and the offline receipt/reconciliation. Those owner
changes and their actual acceptance unblock AUD-108. Keep it unticked.

### Fixture boundary

The first scoped PostgreSQL attempt stopped at migration initialization with
`SCHEMA_CELL_IDENTITY_INVALID`, before calling the new writer. The production
schema gate intentionally fixes the local identity to default/default until
ARC-202. Its source and assertion remain unchanged. Main-flow tests therefore
use the production migration path. A separate disposable SQL fixture may seed
a non-default local cell and matching head only after independently confirming
that the chain is empty at zero-hash genesis. This checks that the append reads
actual database attribution; it does not qualify an installation, change
retained history or rerun audit58 acceptance. The original failed run remains
retained with its exact source and logs.

A later negative fixture initially changed only `bootstrap_complete`, leaving
the required bootstrap fields populated. PostgreSQL correctly refused that
inconsistent tuple with a CHECK conflict before append. The corrected fixture
uses the schema's valid unbootstrapped tuple and rolls it back after asserting
`AUDIT_SCOPE_DENIED`. The CHECK and writer are unchanged; the failed fixture
run is retained separately from its correction.

## Scoped PostgreSQL qualification

Three new contract unit roots and tagged `go vet` passed on Linux amd64. The
new `TestScopedAuditPostgreSQL` root passed in 3.28 seconds against PostgreSQL
18.6 and pgvector 0.8.7. It verified the exact non-default workspace, thirteen
uniform missing/foreign attribution denials, absent installation/local identity,
minimal payload, the original installation writer's default workspace, a role
with only the named column reads and INSERT, private-head read denial, append
refusal and repair, rollback, cancellation before commit and during a genuinely
blocked insert, twelve concurrent committed appends and contiguous hashes.
Real PostgreSQL wire faults verified zero events when COMMIT was withheld and
one when the server ACK was suppressed, one callback and COMMIT attempt each,
and a separate explicitly retried read. No automatic retry was observed.

The main path used the production default/default migration gate. The separate
operator-seeded empty-genesis SQL case verified non-default site/cell attribution
and refusal of nonlocal defaults. It supplies no non-default installation claim.
The original installation and assistant append implementations stayed byte-identical.
IAM's actual scoped read and complete current-session authorization remain a
separate same-source consumer pairing.

The unit, build and PostgreSQL jobs each used the pinned cached PostgreSQL image
as a Linux execution environment, one CPU, a 1.5 GiB cgroup and network mode
`none`. The final PostgreSQL run had only loopback and no routes or published
ports. Its preflight recorded 6,034 MiB free and 19,463 MiB available memory,
746 GiB available disk and full I/O PSI 0.01/0.94 over ten/sixty seconds.
All three successful job containers exited zero without an OOM event and were
removed; temporary credential directories were removed. The two initial fixture
failures described above retain their source and raw logs.

The complete retained manifest SHA-256 is
`1ca8259b60df5cd22c78e1647d807e46c0b7147925be312ad67d6356b425563d`.
The unit inputs remained byte-identical while only the negative PostgreSQL
fixture was corrected, so the passed unit roots were retained rather than
replayed. The corrected test binary and tagged vet were rebuilt before the
final PostgreSQL root. AUD-108 remains open for IAM consumer pairing, DEV/Web
publication and Delivery's offline collection admission and reconciliation.

## Current-main integration qualification

Implementation `8531226d8f16cf27de2d577b5ed99bfa34cdeb25` was privately
rebased onto `8b17a295` before a fast-forward publication. Its three scoped
Go files still match the native qualification manifest above. The audit
writer, fixture, database/migration, API, dependency and COMMIT-fault inputs
were unchanged from that qualification. Unrelated agent-cost and HTTP-client
test helper changes were inspected and are outside this root's execution path.

The current Control production tree passed `go -C services/control build ./...`
in an offline container with one CPU and a 1.5 GiB memory limit. It covered the
new scoped writer and the intervening scheduling source changes. The build
exited zero without OOM and its container was removed. Preflight recorded
4,956 MiB free, 19,428 MiB available, 745 GiB available disk and full I/O PSI
0.05/0.69 over ten/sixty seconds. It used network mode `none` and exposed no host port.

Current layout, roadmap and diff checks passed. The check command was
`unshare --net make -o test-tools -o check-web check`. Tool and Web checks and
non-Control builds were explicitly retained from the qualified shared baseline
recorded by `c2d1d4c4`; their source inputs match. The complete test gates linked
by that baseline remain prior evidence for their covered source versions.
This was not a fresh complete `make check`, `make build` or `make test` run.
The scoped unit and PostgreSQL roots were not repeated after matching their
unchanged inputs. The retained integration-manifest SHA-256 is
`a0dae386454dd47f1763bc4076b5510f6283f79879aeda32f1a597adab33cd2e`.

This integration check does not qualify IAM's actual workspace read, its
current-authority locks, DEV/Web publication or Delivery's support entry points.
AUD-108 remains unticked.
