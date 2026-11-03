# Native Dameng middleware metadata

Wave6 source contribution, 2026-10-24. Dameng stores the existing middleware
registration, request intent, lease, evidence, restore fence and audit state.
Existing PostgreSQL health/backup/isolated-restore owners and real external MySQL
TLS health client retain their declared targets. This does not make Dameng an
external maintenance target or qualify a generic Dameng backup/restore operator.

## Repository and configuration

Explicit native statements use string CLOB JSON, canonical UUIDs, UTC
microseconds, writable `FOR UPDATE` transactions and typed bounded literal LIMIT
values. Expiry uses ordinary native row locks and its existing bounded deadline;
native contention may wait/fail, without assuming SKIP LOCKED behavior. Policy
page size still enters through `Configuration.Policy`; its existing maximum
bounds the SQL representation. No new environment key, schema selector, optional
authorization bypass, native fake result or additional client is introduced.

Current Identity/session/Workspace checks precede intent lookup and repeat in
each final mutation/claim/renewal/publication transaction. Immutable snapshot and
profile fingerprints, idempotency conflicts, zero DTO on failed/unknown commit,
and explicit original-operation reconciliation remain intact. SQL errors exit
immediately; no readback is attempted on the poisoned native transaction.
An unknown renewal commit cancels and joins the external owner, then returns
uncertainty without another terminal write. Expired running operations become
unknown and cannot automatically reenter the executable queue.

`middlewareFragment(current)` in `contribution_task_middleware.go` contributes
`middleware_v2`, requiring `foundation_v1`. Two tables, three indexes and two
native triggers preserve scoped foreign keys, immutable input/actor CLOB bytes,
terminal immutability and retained operation history. The coordinator alone
registers this fragment, verifies actual catalog/current grants, selects the
native Store, and preserves its shared maintenance/epoch boundary. Reuse the
existing stopped-worker restore entrypoint; clearing SQL leases is not evidence
that a physical operation stopped. The module does not invent a parallel epoch,
database configuration loader or session-lock policy.

Native `DBMS_LOB` is required for immutable CLOB comparison. Its operator-approved
availability and privileges, native UTC/charset/empty-string profile, full schema
readback and runtime DML grants are central admission gates. Driver text/result
budgets must cover configured intent/evidence limits; oversized results fail
instead of returning partial successful evidence.

## Prepared native acceptance

Pure tests cover statement/limit argument selection, failed SQL abort and
unknown commit with no successful DTO. The actual native test
`TestActualDamengMiddlewareIntentAndRecovery` additionally requires the real
native Identity/Workspace/License repositories and a fresh full schema. It imports
a synthetic signed license, registers one actual synthetic external TLS health
endpoint, checks original intent lookup/replay, performs real health, verifies
CLOB evidence/hash, reconciles a scripted lost claim acknowledgement against real
SQL, expires its original attempt to unknown, checks pagination and restore
invalidation, rejects terminal mutation, and fences a revoked actor before any
external call. It does not use a scripted endpoint or claim actual wire loss.

Test-only inputs, read once at entry, have no defaults:

- `PROJECT_ONE_DAMENG_MIDDLEWARE_CONFIGURATION`: protected native connection
  document passed to the existing loader, schema matching
  `PO_MIDDLEWARE_[A-F0-9]{16}_TEST`; secrets remain file references.
- `PROJECT_ONE_DAMENG_MIDDLEWARE_PROFILES`: protected existing middleware
  configuration document, exactly one `customer_external` profile, real synthetic
  TLS endpoint, request/probe at most10 seconds and operation at most60 seconds.
  This test overrides page size to1 to exercise continuation; it does not modify
  the source document or production defaults.

After central integration, formatting and coordinator resource admission, from
`services/control`:

```sh
GOMAXPROCS=1 GOMEMLIMIT=512MiB go test -p 1 ./internal/modules/middleware -run '^TestNativeMiddleware' -count=1
GOMAXPROCS=1 GOMEMLIMIT=512MiB CGO_ENABLED=1 go test -p 1 -tags 'odbc_manager,repository_integration,dameng_repository_integration' ./internal/modules/middleware -run '^TestActualDamengMiddlewareIntentAndRecovery$' -count=1 -timeout=4m
```

No Go tools/tests/builds/services/Git were run by this lane. The full native
DDL/runtime, fresh/upgrade schema, grant revocation, maintenance epoch and physical
cancellation profiles remain pending. End-to-end platform-owned M17 native
metadata plus actual backup/restore must use its original separate acceptance;
successful external health cannot establish that result. The native fixture
drains physical owners and retains exact synthetic database evidence for bounded
coordinator cleanup; no shared or uncertain state is silently removed.

Official [DM JSON](https://eco.dameng.com/document/dm/zh-cn/pm/json.html)
and [native DDL](https://eco.dameng.com/document/dm/zh-cn/pm/definition-statement.html)
were consulted. Source review does not establish executed vendor compatibility.
