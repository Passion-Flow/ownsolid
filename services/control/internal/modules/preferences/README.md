# Authenticated user theme preference

This small owner persists the current user's fixed light/dark theme. It is not the locale registry, an arbitrary preference JSON store or deployment configuration.

[service.go](service.go) defines Get, Update and VerifyStored. Updates require a valid theme and expected revision, lock/read the existing row, advance the revision and write audit in the selected transaction. [repository.go](repository.go) rechecks current identity; a request cannot select another user's preference. Revision conflict is an explicit outcome for the client to reconcile.

The historical domain migration15 defines preferences; current cumulative
`portal` adds them through account21, preserving the exact M14 prefix. VerifyStored
rejects missing, orphaned or malformed state and never silently recreates defaults.
Matched restore retains theme/revision data while invalidating execution authority.
Portal timeouts come from typed policy and documented protected env overlays.
Light/dark remain fixed protocol values. See the
[cumulative operation](../../../../../docs/milestones/m15-portal/cumulative-operation.md)
and [current exact-root queue](../../../../../tools/quality/portal/README.md).

Synchronize [preferences frontend](../../../../../apps/web/packages/preferences), the identity shell/theme consumer, [HTTP contracts](../../../../../contracts/http), [M15](../../../../../docs/milestones/m15-portal), initialization and both database dialects. A future preference field needs explicit schema, privacy and concurrency decisions; it should not be inserted into this record merely because it is user-facing.

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

Language remains the identity owner's `users.locale`/`UpdateMe` contract and
version, independent of the theme revision. The Dameng identity/account path,
shared schema, serving admission and real-driver tests must be integrated before
claiming native language/theme persistence. [dameng_test.go](dameng_test.go)
provides focused statement/fault controls, not native database qualification.
