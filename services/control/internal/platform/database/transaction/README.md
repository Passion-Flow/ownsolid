# Borrowed transactions and outcomes

[contract.go](contract.go) defines Statement, Executor and Store. A Statement binds one explicit PostgreSQL, MySQL or Dameng query to its arguments; its string representation is redacted. Store.Within lends an Executor so current authority, business mutation, audit and outbox share one transaction outcome. The callback cannot commit or roll back independently.

[postgres.go](postgres.go) and [mysql.go](mysql.go) adapt configured real vendor pools. Session files retain pinned-connection behavior where the owner needs it. This package does not load credentials, own pool closure, migrate a schema or rewrite query syntax. The [database composition](../README.md) owns those choices.

[dameng.go](dameng.go), compiled only with `cgo && odbc_manager`, borrows a
successfully admitted [native local Store](../dameng/README.md). It preserves
signed int32 native codes separately from MySQL's Number, checks commit
uncertainty before cancellation, applies typed operation bounds and releases row
contexts on Close. [session_dameng.go](session_dameng.go) implements SessionStore with one physical
connection, multiple definite transactions, exact autocommit restoration and
physical disposal. Native errors never reconnect or clear poison. Commit uncertainty
outranks cleanup/cancellation. The untagged dameng_session_outcome.go retains
mixed cancellation/cleanup graphs even when the callback returned nil, so a
retained physical owner cannot be classified as ordinary cancellation. This does
not activate the platform selector. Its real target checker and
remaining qualification are in the [local connector contract](../../../../../../docs/milestones/m29-domestic-platform/local-connector-contract.md).

Operations require a bounded context. [errors.go](errors.go), [cancellation.go](cancellation.go) and [outcome.go](outcome.go) sanitize errors while preserving distinctions between cancellation, known rollback and uncertain commit. Commit uncertainty takes precedence even when an error graph also contains cancellation. A caller must reconcile the original mutation after an unknown outcome; retrying with a new idempotency identity is unsafe.

Changes affect every selected-database repository and operator restore callback. Synchronize both adapters, session behavior and caller recovery tests; avoid weakening the contract to satisfy one fake executor. Focused native check: `go -C services/control test ./internal/platform/database/transaction`. Tagged session/repository tests require real isolated vendor fixtures; pure error-graph tests do not verify database commit behavior.

Native Dameng stores, pinned sessions and transaction callbacks also expose the
optional `DamengCapacity() (dameng.ODBCCapacity, error)` method through their
embedded native executor. Domain repositories may type-assert this nonsecret
capability to admit projections and choose bounded pages. Other dialects and the
common Executor contract do not change. The capability is a limit snapshot, not
a live reservation; each page must close its Rows before the next, while preserving
the owning transaction/revision/identity. Native allocation admission may still
refuse concurrent work without retrying a business mutation. See the
[wave6 integration notes](../../../../../../docs/execution/mvp/parallel-wave6-domestic.md).

`DamengTransaction(tx, kernel)` lends a transaction from that same native kernel
without adding connection, completion or retry authority. The trusted caller
must preserve the original binding: `database/sql` does not expose an independent
transaction-to-pool provenance query. The optional
`OwnsDamengExecutor(Executor) bool` capability accepts only an exact native
transaction executor carrying the same kernel and a nonnil `*sql.Tx`. Nil,
foreign, pooled, pinned-connection and wrapped executors refuse. This structural
proof does not establish a live database transaction or a held maintenance lock;
normal native context, binding, capacity and SQL checks still apply.
