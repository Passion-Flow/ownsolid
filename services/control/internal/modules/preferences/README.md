# Authenticated user appearance preference

This small owner persists the current user's theme (`system`, `light` or `dark`), density (`comfortable` or `compact`), language and whether one-character keyboard shortcuts work, which follow the account across browsers. `system` leaves the choice to the operating system of each browser and is what a new account starts with. It is not the locale registry, an arbitrary preference JSON store or deployment configuration.

[service.go](service.go) defines Get, Update and VerifyStored. An update names the fields it changes and keeps the others, so browsers changing different fields do not overwrite each other. Updates require valid values and an expected revision, lock/read the existing row, advance the revision and write audit in the selected transaction. [repository.go](repository.go) rechecks current identity; a request cannot select another user's preference. Revision conflict is an explicit outcome for the client to reconcile.

The historical domain migration15 defines preferences; current cumulative
`portal` adds them through account21, preserving the exact M14 prefix. VerifyStored
rejects missing, orphaned or malformed state and never silently recreates defaults.
Matched restore retains theme/revision data while invalidating execution authority.
Portal timeouts come from typed policy and documented protected env overlays.
Themes and densities are fixed protocol values. See the
[cumulative operation](../../../../../docs/milestones/m15-portal/cumulative-operation.md)
and [current exact-root queue](../../../../../tools/quality/portal/README.md).

Synchronize [preferences frontend](../../../../../web/README.md), the identity shell/theme consumer, [HTTP contracts](../../../../../contracts/http), [M15](../../../../../docs/milestones/m15-portal), initialization and both database dialects. A future preference field needs explicit schema, privacy and concurrency decisions; it should not be inserted into this record merely because it is user-facing.

Focused native check: `go -C services/control test ./internal/modules/preferences`. Selected database tests must exercise current identity, stale revisions, audit/commit outcomes and restart integrity. Browser appearance and independent locale-resource loading are separate checks.

Updates require one confirmed audit row in the same transaction as the revision
change. Non-single audit acknowledgement rolls back and returns no preference.
Commit uncertainty takes precedence over joined cancellation/deadline failures;
explicit authenticated Get reconciles the persisted value/revision without
resending a write. See [the recovery contract](../../../../../docs/milestones/m15-portal/commit-recovery.md)
for the real paired wire-fault, audit-report fault and current-identity checks.
Source presence does not establish execution of those controls.

The native Dameng repository branch uses explicit positional SQL, UTC microsecond
timestamps and a checked UPDATE followed by locked readback; it does not depend
on PostgreSQL RETURNING. The installation/tenant authority fence uses stronger
FOR UPDATE locks in the same existing write transaction. Unknown dialects do not
fall back to PostgreSQL. The native schema must seed one preference per existing
user, initialize future users atomically, and preserve owner/revision constraints.
Missing seed state remains an error, never a lazy default insert.

Saved views ([saved_views.go](saved_views.go)) keep a list's filters and columns under a name
for one person (UX-103). The client chooses each view's ID, so a create is safe to retry; a
retry of a create that already landed returns the view it made. Every read and write is scoped
to the session's user; another person's view answers not found. They are personal interface
state, so they are not audited and work without a license. Names are unique per person and
list, at most 50 views per list, and a view's state at most 4096 bytes.

The MySQL and Dameng chains have no density column and accept only light and dark
(CMP-201, CMP-202): they read as comfortable and refuse a value they cannot store with
`PREFERENCE_UNAVAILABLE`, before any write.

Language follows the account too (`auto`, `en` or `zh-CN`, ADR 0025). A choice also updates the
identity owner's `users.locale`, which emails read, in the same transaction, and moves that
account's version. [tenant_settings.go](tenant_settings.go) owns the tenant's default language,
which the public runtime configuration serves so pages before sign-in can use it. MySQL and
Dameng have no column for either: they read as `auto` and refuse a choice. The Dameng identity/account path,
shared schema, serving admission and real-driver tests must be integrated before
claiming native language/theme persistence. [dameng_test.go](dameng_test.go)
provides focused statement/fault controls, not native database qualification.
