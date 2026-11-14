# Transactional audit persistence

[events.go](events.go) and [repository.go](repository.go) record named audit events for authenticated or anonymous operations. This owner is reused by identity, licensing, SSO and platform mail/retention.

Audit data belongs to the same business transaction when exposed through WriteWithin. It must not independently commit a borrowed transaction, leak credentials/request bodies or silently convert failed audit persistence into accepted business state. Existing PostgreSQL adapters and selected-dialect paths are compatibility surfaces.

The persistent table originates in migration 000001; operation lookup changes have their own migration. An audit event records the declared operation/outcome, not proof that a remote mail recipient or provider accepted an external side effect.

The platform → audit imports are an explicit layering exception. Any future shared audit port must preserve same-transaction behavior and all owned vendor queries; a second generic logger is not a replacement.

Coordinate changes with identity, platform/mail, platform/retention, [database migrations](../../../migrations) and [repository_integration_test.go](repository_integration_test.go). Real selected-SQL tests are required for persistence changes; static event-shape inspection alone is insufficient.

Wave5 adds explicit native Dameng inserts and requires exactly one affected audit
row. A failed or unconfirmed audit write propagates to the borrowed business
transaction. The account/mail schema contribution rejects event UPDATE while
preserving the existing retention DELETE authority. Native audit persistence is
exercised by the opt-in account repository chain, including rollback and operation
readback. See [the handoff](../../../../../docs/execution/mvp/parallel-wave5-account.md);
these sources have not yet passed native target execution.

PostgreSQL appends each new event to an installation-wide hash chain in the
same transaction as its insert. A shared database tail orders commits across
connections and time partitions. Existing events remain readable and precede
the integrity coverage boundary. See the [operator export and offline verifier](../../../../../tools/audit-verify/README.md)
and the [serialization and threat model](../../../../../docs/research/audit-chain.md).

The PostgreSQL serving role can append and read audit events. The audit parent
rejects its UPDATE, DELETE and TRUNCATE operations, including row mutations
against attached partitions. The database owner retains authority to operate
the database and expire partitions through retention.
[Audit mutation authority](../../../../../docs/research/audit-immutability.md)

## Product assistant append

Use `WriteProductAssistantWithin` in the PostgreSQL transaction that rechecks the
user session, resolves the workspace and completes the operation. Pass the
authenticated user's ID as `SubjectUserID`, the resolved tenant and workspace,
the entrance, the server request ID, the registered command or tool ID and the
outcome. Return an append error from the transaction callback before releasing a
result. The append checks the local tenant and subject's workspace authority in
its insert. The operation owner remains responsible for the session, operation
authorization and retries.

The writer stores `actor_kind=product_assistant` and
`actor_id=ownsolid.product-assistant` in fixed metadata, with an empty human
actor and a separate user subject. The action is `product_assistant.<tool_id>`.
The existing chain binds the metadata digest. The operator can
[disclose and verify the assistant actor offline](../../../../../tools/audit-verify/README.md#verify-a-disclosed-assistant-actor).
The [opt-in Admin projection](../../../../../docs/research/audit-list-assistant-projection.md#admin-http-representation)
exposes verified assistant attribution while retaining the existing default
representation. See the [writer research](../../../../../docs/research/product-assistant-audit-writer.md).

## Diagnostic access consumers

The license diagnostics reader already uses `WriteWithin` before returning its
bounded download and clears it on transaction failure. This package owns no
diagnostic HTTP reader or support collector. Identity audit reads and installer
support collection must account for access at their actual entry points.
`WriteWithin` resolves the installation tenant and default workspace; it does
not authorize a supplied workspace or provide arbitrary workspace attribution.
See the [consumer inventory and integration contract](../../../../../docs/research/audit-diagnostic-access.md).

## Exact workspace append

`WriteScopedWithin(ctx, tx, ScopedEvent)` records a human Admin event against
an exact installation tenant, workspace and local site/cell. Supply these IDs
and `ActorUserID` only from current server authority in that same transaction.
Resolve site/cell from the database, not the default constants or request data.
Keep the existing session/epoch and operation checks; the SQL attribution guard
is not authentication. Disabled workspace history remains readable for Admins.

Use a canonical nonzero server-generated `RequestID`, including when the caller
has no HTTP request. `Action` and optional `TargetType` are lowercase ASCII codes
of at most 64 bytes. A target requires both type and canonical nonzero UUID.
`Reason` permits at most 500 UTF-8 bytes without control characters and must be
bounded server-generated selection facts. Do not pass raw queries, cursors,
credentials or returned records. Subject, identifier digest and metadata retain
their empty defaults; assistant events still use their dedicated contract.

This first path requires a borrowed PostgreSQL transaction and an active Admin.
Malformed events return `ErrScopedEventInvalid`; a well-formed missing or foreign
actor/scope returns `ErrScopeDenied`. Propagate all other append errors. A pool
is not a transaction. The writer commits and retries nothing. An unknown commit
must release no response and cannot be treated as a confirmed append outcome.

The least-privilege caller needs INSERT on `audit_events` and SELECT on
`users(id,status,is_platform_admin)`, `workspaces(id,tenant_id)`,
`installation_state(id,bootstrap_complete,tenant_id)` and
`cells(id,site_id,is_local)`. It needs no private chain-head access or mutation
privilege on those authority tables. Existing triggers assign positions and
hashes in the caller's transaction. IAM owns the real workspace read consumer;
Delivery owns support collection and its Control-down admission. The complete
AUD-108 item remains open until those paths are accepted.

Run the scoped contract tests with the real isolated PostgreSQL input documented
in the development environment:

```sh
go -C services/control test -tags repository_integration \
  ./internal/modules/audit -run '^TestScopedAudit' -count=1 -v
```

The tagged root requires a fixture instead of accepting a skipped database test.
See the [design and acceptance](../../../../../docs/research/audit-diagnostic-access.md#scoped-append-contract).
