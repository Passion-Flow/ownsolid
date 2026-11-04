# Workspace authority

This owner resolves the installed tenant's Workspaces, current memberships/roles, Workspace lifecycle and notes. It supplies scoped authorization to downstream modules.

[scope.go](scope.go) resolves Workspace through installation/tenant state, obtains the required Workspace lock and then reads current membership. [authority.go](authority.go) carries authority identity; [service.go](service.go), [mutations.go](mutations.go), [notes.go](notes.go) and [lists.go](lists.go) implement the public operations. [reconciliation.go](reconciliation.go) handles retained mutation outcomes.

Never resolve authority from a client tenant ID or from a membership snapshot obtained before the lock wait. Platform-admin audience, active state, membership and owner-only operations remain distinct conditions. Business writers borrow the selected transaction and recheck identity; mutation/audit and revision changes share the outcome.

Workspace/member state starts in migration 000001; notes and subsequent Workspace changes use their owning migrations and explicit PostgreSQL/MySQL queries. Preserve revision conflicts, membership-removal behavior, no cross-Workspace disclosure and commit-unknown readback.

Changes must synchronize [M02](../../../../../docs/milestones/m02-workspaces), [Workspace frontend](../../../../../web/README.md), HTTP contracts, config policy/env mappings and dependent module scope tests.

Focused native check: go -C services/control test ./internal/modules/workspaces. Real SQL mutation/reconciliation checks are retained in the repository integration tests, including [commit acknowledgements](commit_ack_repository_integration_test.go), and require their declared database fixture.

The wave5 native Dameng contribution preserves actor/source -> installation ->
tenant -> exact Workspace locking, followed by current membership reads. Native
shared-read fences use the stronger `FOR UPDATE` in a writable transaction.
Notes bind Unicode CLOB text; empty notes use `EMPTY_CLOB()` and are read back by
exact key. Workspace/member/note pages bind scope and cursor values and reject
precision loss. Native membership creation supplies its own authority UUID.

The isolated native account harness exercises role restrictions, optimistic
versions, removal, scope guards, pagination and real rollback. Its post-commit
acknowledgement fault is an identified wrapper, not a network-loss experiment.
See [wave5 account delivery](../../../../../docs/execution/mvp/parallel-wave5-account.md)
for source status and coordinator-only execution. The lane's schema contribution
adds scope-page indexes and immutable membership/note identities; the central
migration owner registers the contribution and verifies fresh/upgrade catalogs.

`http/disabled_routes_test.go` retains authenticated PostgreSQL controls for the
minimal runtime's exclusive route ownership, disabled descendants and original
platform dispatch. The selected native delivery test also checks the shipped
executable before/after restart.

Current PostgreSQL actor/session/membership fences use `FOR NO KEY UPDATE` on
immutable user identities. This still serializes status/epoch/profile/session
mutation but permits the foreign-key `KEY SHARE` checks of concurrent node result
writes. A full `FOR UPDATE` user fence inverted with the machine-owned Workspace
lock during deployment removal. User IDs are never changed by these operations;
MySQL/Dameng statements retain their existing supported locks. The deterministic
`deployments/authority_lock_test.go` observes the actual server wait graph, verifies
foreign-key progress and continued epoch-write blocking, then executes the actual
stop/removal result path. See [PostgreSQL18 row lock conflicts](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS).
