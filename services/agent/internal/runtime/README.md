# Profile dispatch and agent lifecycle

[runtime.go](runtime.go) loads protected typed configuration, enrolls the node and composes clients/managers for one declared execution profile. It rejects unsupported profiles, contradictory manager configuration and overlapping identity/workload roots. The absent profile resolves to the explicit read-only baseline; it does not enable every manager.

[lease.go](lease.go) validates complete operation/profile/task/token/attempt/payload bindings before dispatch. Training and preparation runtime files coordinate lease renewal, events, transfers and terminal outcomes; recovery files retain fenced work and operator cleanup obligations. Registry operations are separately selected, not arbitrary background image downloads.

The run loop sends heartbeats and claims through one [client session](../client/README.md) per process, the session stream with an HTTPS long-poll fallback ([ADR 0007](../../../../docs/decisions/0007-agent-session-protocol.md)). A claim waits up to one interval for work, and that wait counts toward the interval, so an idle agent reports once per interval and a busy one starts its next cycle at once. The loop logs `agent_session_transport` when the transport changes. Before it runs a lease, the cycle saves the task cursor and acknowledges the lease, and it runs nothing control did not confirm; a lease older than the cursor is refused, and a resync recovers local work and adopts control's cursor ([ADR 0011](../../../../docs/decisions/0011-task-delivery-semantics.md)).

Runtime borrows [state](../state/README.md) for identity and process locking, [settings](../settings/README.md) for bounded policy, and [client](../client/README.md) for control transport. Managers own their local journals and resource checks. A failed enrollment acknowledgement or lost lease must not erase identity/workload evidence or silently create a replacement attempt.

[component_recovery.go](component_recovery.go) invokes the same component manager
at startup and before each cycle's discovery, observation, heartbeat and claim.
Recovery uses the existing typed `component_task_seconds` budget and preserves a
shorter caller deadline. Failure blocks that cycle; the existing bounded run loop
may reconcile the retained journal on its next attempt without a process restart.
The read-only profile has no component manager or component recovery effects.

Change the corresponding [agent contract](../../../../contracts/agent), cmd/agent options, protected [configuration examples](../../../../config/examples/agent), control/nodes hooks and manager/client interfaces together. New active work needs explicit startup validation, cancellation, joined shutdown and exact cleanup ownership.

Focused check: `go -C services/agent test ./internal/runtime`. Container lifecycle/recovery fixtures, actual control enrollment and native target operations are separate gates; mocked lease dispatch does not prove real process ownership or hardware execution.


[environment.go](environment.go) maps only explicit protected `--env-file` values
to this same typed configuration and its policy owners. Valid JSON is checked
before overrides; complete profile/root/resource validation runs afterward.
Unknown, duplicate, empty or contradictory inputs fail. Run/enrollment/fenced
cleanup use the identical file. See the [commented mapping](../../../../config/examples/agent/lifecycle-policy.env.example)
and [closed key contract](../../../../contracts/configuration/v1/agent-environment/schema.json).
