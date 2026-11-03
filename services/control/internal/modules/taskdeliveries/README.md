# Durable delivery of node-task events

This owner bridges existing node-task mutations to the configured event transport. It persists an outbox, receipts, dead letters and current human visibility. Event delivery is not task claim, execution or completion authority; those remain with [nodes](../nodes/README.md).

[service.go](service.go) records a queued event inside the caller's borrowed transaction, binding tenant, Workspace, node, task and initial pending attempt. No broker I/O or independent commit belongs in that callback. [worker.go](worker.go), [consumer.go](consumer.go) and [run.go](run.go) perform later bounded transport work. [lifecycle.go](lifecycle.go), recovery, integrity, quarantine and maintenance paths retain failure/readback and join owned workers.

`Run` checks terminal admission/integrity failures immediately after each stage,
before later expiry, cleanup or consumption may proceed. Earlier committed work
remains retained. [The worker fence contract and actual-fixture cases](../../../../../docs/milestones/m25-task-events/worker-fence.md)
cover pending broker delivery, SQL history preservation and explicit recovery;
execution status remains separate from the test source.
The original fatal code is retained in the lifecycle, so a stale `Run` caller
cannot resume the failed owner. Read-only verification does not reset it;
close/join and full new transport/service admission are required for recovery.
Exhausting the bounded consecutive reconnect policy is terminal as well; a stale
caller cannot obtain a new budget by restarting the same owner. The
[real outage/recovery regression](../../../../../docs/milestones/m25-task-events/worker-recovery.md)
keeps original SQL/broker proof separate from physical task execution.
The [authority and unknown-result regressions](../../../../../docs/milestones/m25-task-events/authority-recovery.md)
cover current role/session checks before retained retry readback, fresh-owner
receipt recovery and duplicate delivery after a physical task becomes terminal.

Migration 000025 and local [schema guards](schema) own SQL outbox/receipt/dead-letter state. The [taskbus adapter](../../platform/taskbus) carries typed events under a configured namespace and transport identity. JetStream, NKey and TLS settings do not relax current node/Workspace authority. Unknown publication, dead-letter, quarantine and a successful receipt remain distinct outcomes.

Synchronize [task-event deployment inputs](../../../../../config/examples/task-events/README.md), [M25](../../../../../docs/milestones/m25-task-events), agent/HTTP contracts, [frontend](../../../../../apps/web/packages/taskdeliveries), node admission and [paired/native harnesses](../../../../../tools/quality/task-events). Broker/storage retries must follow the exact retained event and attempt contract rather than fabricate a new task.

Focused native check: `go -C services/control test ./internal/modules/taskdeliveries`. Real selected SQL plus broker publication/lookup/restart profiles and joined shutdown need their declared isolated fixtures; a recorded queue row is not confirmed broker delivery.
