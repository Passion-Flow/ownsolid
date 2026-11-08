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
The existing chain binds the metadata digest and retains the prior export
format. A readable assistant actor in audit search or JSONL needs its own
projection. See the [implementation research](../../../../../docs/research/product-assistant-audit-writer.md).
