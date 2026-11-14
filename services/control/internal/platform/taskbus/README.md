# Private task-event transport

This package carries the existing task intent over a configured NATS JetStream profile. [Task deliveries](../../modules/taskdeliveries/README.md) owns SQL outbox state, task authorization, receipt/dead-letter persistence and reconciliation. A broker message never grants permission to execute a task.

## Configuration and connection admission

[Configuration](configuration.go) is the sole typed transport policy. It binds the TLS endpoint, explicit private dial addresses, CA/client certificate/key, distinct publisher/consumer NKey seed references, namespace, stream/consumer identity, deadlines, retry limits, storage/retention bounds and shutdown budget. Strict JSON/YAML and explicit `ApplyEnvironment` reject unknown/duplicate/null fields and contradictory policy. Disabled configuration is explicitly empty apart from its false selector; there is no ambient environment lookup.

[Task-event examples](../../../../../config/examples/task-events/README.md) document the protected root `--env-file`/YAML mapping and pre-provisioned broker seam. [Server task-event composition](../../../cmd/server/task_events.go) resolves the installed tenant and owns transport lifecycle. Credentials are private file references; [credentials.go](credentials.go) bounds and validates material and reopens the pinned signing identity for nonce signing.

[NewJetStream](jetstream.go) opens actual restricted publisher/consumer connections, uses verified TLS and pinned dial addresses, disables library automatic reconnection/discovered-server switching, and requires explicit anonymous/unknown-key authentication denial. It loads existing stream/consumer resources and verifies their exact [admission profile](admission.go). It never provisions broker resources or starts a background task worker. The caller owns `Close` after successful construction.

## Publication, consumption and uncertain outcomes

[contract.go](contract.go) defines the closed canonical envelope, stable logical digest and namespace/tenant/subject relation. `Publish` disables hidden publish retries, checks the real broker acknowledgement and independently reads back the exact subject/body/message ID/sequence. A duplicate acknowledgement alone is insufficient. `TASK_BUS_PUBLISH_UNKNOWN` preserves uncertainty; `Lookup` reconciles the original immutable envelope instead of creating another logical event.

`Next` performs a bounded pull and validates the admitted profile and message metadata. `Message.Confirm` uses the broker's confirmed acknowledgement only after the SQL owner has confirmed receipt or dead-letter/audit commit. If SQL commit is unknown, the consumer must not acknowledge it as complete. `Delay` requests bounded redelivery; acknowledgement uncertainty remains explicit. Transport delivery counts do not replace the application's delivery/quarantine policy.

[Reconnect](reconnect.go) rebuilds and re-admits connections without replaying publication. Stream/consumer creation identity and both public NKeys must match the original. A broker restart must preserve these identities; recreating a lookalike stream is not recovery. Retry scheduling and lifetime remain with taskdeliveries, bounded by injected policy. `Inspect` exposes bounded operational facts and does not authorize a new task.

## Modification and verification

Keep the envelope, exact broker profile, root env schema, consumer SQL lifecycle and restart/readback contract synchronized. [wire_fixture.go](wire_fixture.go) is a controlled forwarding fixture used by explicit acceptance paths; it is not a substitute production broker or a delivery-success stub.

With the pinned toolchain and admitted cache/temp roots:

```sh
go -C services/control test ./internal/platform/taskbus
```

Controlled checks cover canonical envelopes, configuration, private credentials and profile refusal. [native_transport_integration_test.go](native_transport_integration_test.go), gated by `task_events_native_integration`, requires the real private fixture; the [native runbook](../../../../../docs/milestones/m25-task-events/native-transport.md) and paired SQL/root-runtime runbooks own broader acceptance. Never classify a timeout, failed negative authentication probe or protocol fixture pass as actual broker/SQL acceptance.
