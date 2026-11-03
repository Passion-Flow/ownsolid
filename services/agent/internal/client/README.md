# Authenticated control transport

[client.go](client.go) owns the configured HTTPS client, current node credential and bounded control requests. It validates the control origin and fixed agent API paths, rejects redirects and uses typed [settings](../settings/README.md) for connection/header/request budgets. It does not grant a task lease or choose business scope.

Artifact transfers bind the current lease and approved recipe length/digest rather than trusting a downloaded filename. [models.go](models.go), [training.go](training.go), [model_preparation.go](model_preparation.go) and [registry.go](registry.go) own their versioned file/event/upload interfaces. [enrollment_outcome.go](enrollment_outcome.go) preserves enrollment outcome distinctions; a lost reply must not be presented as confirmed absence or successful enrollment.

Credentials come from the runtime/state owner and stay out of command arguments and diagnostics. Responses, remote errors and transfer sizes must remain bounded. Client.Close releases its HTTP transports; it does not delete durable node or workload state.

Change [contracts/agent](../../../../contracts/agent), the matching control handlers, manager transport interfaces and timeout policy together. Focused check: `go -C services/agent test ./internal/client`. Controlled HTTPS and transfer tests are separate from real enrollment against the installed control service, current lease revocation and target workload acceptance.
