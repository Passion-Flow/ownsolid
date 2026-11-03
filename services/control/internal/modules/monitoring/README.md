# Scoped Admin monitoring snapshots

A caller cancelled immediately after a known SQL commit receives a zero snapshot
and a cancellation error. The diagnostic audit remains committed exactly once;
unknown COMMIT still takes precedence and is never replayed. Current selected-SQL
controls cover role/user/Workspace/tenant revocation and real authenticated TLS
HTTP/2 diagnostics independently from the raw metrics SSE/process controls.

This module composes bounded Admin observations from existing owners. It adds no telemetry listener, independent worker, metrics database or second connection pool. [service.go](service.go) requires matching identity, Workspace, license, component/node and distribution owners plus the configured HTTP recorder.

The Control root wraps its already selected primary and optional machine HTTP
chains with the recorder. Their installation-lease admission checks remain in
the chain when monitoring is enabled; a lost lease returns service-unavailable
before entering business code and the recorder observes that refusal. The
composition regression is `TestMonitoringPreservesInstallationLeaseAdmission`
in `cmd/server`.

Get checks Admin audience/platform authority and current installed tenant/Workspace in the selected transaction. The returned snapshot includes license state, process/HTTP and runtime observations, gateway window and paged node observations. Diagnostic-read audit and trusted license-clock updates share the commit; a lost commit acknowledgement is not a successful read. Missing measurements remain distinguishable from a measured zero.

Monitoring requires the locked Workspace to be active, even when the ordinary
Admin directory permits inspecting a disabled Workspace. Disabled monitoring
reads return `ACCESS_DENIED` and a zero snapshot before license-clock/metrics
collection or diagnostic audit. Re-enabling that scope permits a later explicit
read using the same current authorized Admin session.

[config.go](config.go) owns snapshot concurrency, deadlines, limits and the matching recorder policy. The control root loads MonitoringConfigFile. This module uses existing persistent authorities rather than adding a migration. Cursor format, bounded admission and sanitized error codes must stay consistent with the HTTP contract.

Synchronize [monitoring configuration](../../../../../config/examples/monitoring/README.md), [M30](../../../../../docs/milestones/m30-reliability), platform/monitoringmetrics, component/distribution observation sources, HTTP/frontend contracts and [reliability harnesses](../../../../../tools/quality/reliability). Changes to a metric's source or null semantics require consumer and capacity-report updates; a dashboard must not invent an observed value.

Focused native check: `go -C services/control test ./internal/modules/monitoring`. Native SQL, real full-server observations and capacity workloads are separate acceptance profiles. Capacity reports describe their actual process, request and dataset sizes; they do not establish unmeasured production scale.
