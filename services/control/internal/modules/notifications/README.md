# Recipient-private terminal notifications

This owner persists and presents internal terminal task notices for the authenticated recipient. It is separate from the email outbox and does not prove external delivery.

[service.go](service.go) owns configuration, caller-private list/read state and availability; [repository.go](repository.go) uses the selected database. [lifecycle.go](lifecycle.go) owns bounded maintenance startup, readiness and joined stop. Notice fields retain Workspace, workflow/run, outcome, error code, occurrence and read state; list cursors and unread counts must preserve recipient isolation.

The historical domain migration15 defines notices/preferences. Current cumulative
`portal` appends account21 after the exact20-entry M14 prefix, at86 total tables.
The same real terminal/audit transaction creates one truthful notice without
backfilling historical runs. Maintenance joins startup/readiness/stop; matched
restore preserves read data while invalidating inherited execution authority.
Current identity, retention and unavailable/error handling remain server authority.
The frontend cannot manufacture a successful run from an unread count or row.
See [cumulative operations](../../../../../docs/milestones/m15-portal/cumulative-operation.md)
and the [current exact-root queue](../../../../../tools/quality/portal/README.md).

Policy comes from the typed control Portal policy. Synchronize [workflow terminal paths](../workflows/README.md), [notifications frontend](../../../../../apps/web/packages/notifications), [HTTP contracts](../../../../../contracts/http), [M15](../../../../../docs/milestones/m15-portal), both database repositories and maintenance integration tests. A new notice kind needs a versioned outcome/source contract rather than arbitrary messages inserted by unrelated modules.

Focused native check: `go -C services/control test ./internal/modules/notifications`. Selected SQL tests verify private rows/read conflicts; lifecycle tests must cover maintenance failure, readiness recovery and joined stop. Neither source review nor package compilation is a fresh notification-delivery acceptance result.

Read and cleanup preserve terminal transaction outcomes before cancellation.
An unknown commit joined with a cancelled request remains unknown and exposes no
DTO/count. Recipient-private Get supports explicit readback; an acknowledged
repeat Read does not add another audit. The [Portal recovery contract](../../../../../docs/milestones/m15-portal/commit-recovery.md)
adds fixed paired acknowledgement-loss controls over real terminal triggers and
synthetic workflow inputs; it does not establish executed workflow delivery.

The Dameng branch supplies explicit native SQL for unread counts, pages, Get,
Read/readback/audit, provenance verification and bounded retention. [queries.go](queries.go)
owns vendor-specific page/cleanup statements. All recipient, tenant and cursor
values are bound; only a validated integer LIMIT enters SQL text. Native cursor
times must be representable in UTC at microsecond precision in years 1970–9999;
unrepresentable values fail as INVALID_CURSOR without truncation. Native UTC
timestamps and retention arithmetic do not require MySQL compatibility mode.

Native source visibility retains active Workspace membership, run ownership and
nondeleted caller-owned conversation predicates. Read locks the selected join;
the transaction retains current identity checks, exact update/audit counts and
unknown-commit handling. A statement failure exits immediately without another
query on a poisoned native connection. The native
[applications schema contribution](../../platform/database/dameng/migrate/contribution_applications.go)
now defines the atomic terminal producer and immutable provenance/read-time
enforcement, with no historical run backfill. It reuses the run's canonical UUID
as the notice UUID, preserving one notice per run without a server UUID function.
The migration owner must register this fragment explicitly. Source adaptation and the fault controls in
[dameng_test.go](dameng_test.go) do not establish real-driver execution; native
schema, dependency composition and target verification remain separate gates.
