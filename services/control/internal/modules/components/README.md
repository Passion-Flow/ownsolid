# Managed component state

This owner requests admitted node component changes, records their observations/results and serves bounded component artifacts. Current node-exporter catalog/archive and maintenance behavior are concrete configured capabilities, not a generic remote-command service.

[service.go](service.go) connects to the nodes owner; [requests.go](requests.go) constructs admitted requests; [repository.go](repository.go) and [observation_repository.go](observation_repository.go) persist current state; [artifacts.go](artifacts.go) bounds installed artifact access. [monitoring.go](monitoring.go) supplies observations to the monitoring projection.

Migrations 000004–000005 retain managed component and log-snapshot state. Desired configuration/version is distinct from observed/applied state. Failure, stale observation, rollback and task result must remain tied to the exact node task; do not mark desired state as actual installation.

The current stored node status gates observations: an active node may report
component state, and a disabled node may report passive heartbeat observations
with its retained credential. Disabled state stays stale and grants no task,
renewal or completion authority. Removed credentials cannot report state. The
observer rechecks stored scope/profile/status in the caller's selected SQL
transaction; recovery observations preserve desired intent and prior task history.

Coordinate with [nodes](../nodes/README.md), agent/components, contracts/agent, [M04](../../../../../docs/milestones/m04-components), [M05](../../../../../docs/milestones/m05-maintenance), deploy/components and [component frontend](../../../../../apps/web/packages/components). Source/catalog/image changes need their exact checksums and deployment projections updated.

Focused native check: go -C services/control test ./internal/modules/components. Actual service-manager/node-exporter maintenance and selected SQL require the owning fixtures; model/component observations do not establish target host qualification.

The optional native Dameng repository path, five-table schema contribution,
transaction/type boundaries and executable target profile are documented in
[the native nodes contract](../nodes/NATIVE_DAMENG.md). Its source is prepared; native
SQL execution and whole-platform selection remain separate integration gates.
The component native upsert owner aborts on a SQL error without reusing the
poisoned transaction. No native support is inferred from the PostgreSQL/MySQL
fixtures or an ODBC calling-layer build.

The minimal cumulative `components` profile composes this owner with M01-M03.
The ninth forward migration supplies all fields used by current queries without
activating M05. HTTP admits exact state reads and Admin lifecycle actions only;
configure/restart/upgrade/rollback/logs and later descendants receive structured
refusal. Remove skips later log storage because it does not exist in this catalog.
Missing and corrupt artifacts keep their distinct bounded prerequisite errors.


The cumulative maintenance composition activates this existing M05 owner with both approved recipes and a separate Admin-only log route. Entry10evolves the fixed M04checks and creates log storage; existing entry9bytes remain immutable. Remove deletes the fetched snapshot only in profiles whose schema contains it. Portal state never carries message bodies.
