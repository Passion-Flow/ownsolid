# Agent runtime policy

[policy.go](policy.go) owns restart-required agent deadlines, connection counts, backoff, Docker/systemd/probe budgets and model polling limits. Default supplies the single safe baseline; Validate checks each range and relationships such as header/dial versus total request, stop versus Docker timeout, and model loading versus inference task lifetime.

Runtime loads the typed Policy and injects it into the client and managers. Do not add ambient environment reads or a second fallback inside a workload. Profile identifiers, approved image checks, current lease authority and TLS verification remain fixed contracts, not adjustable disable switches.

When changing a value or default, synchronize [agent examples](../../../../config/examples/agent), runtime parsing, manager-specific budget validation and deployment inputs. The same target binary must support admitted overrides without rebuilding, and the documentation must state units and restart behavior.

Focused check: `go -C services/agent test ./internal/settings`. Add meaningful invalid/nondefault/cross-field cases and exercise the affected operation profile; a policy parser test alone does not verify cleanup completes within its real process budget.


The runtime owner's explicit `--env-file` loader maps every field here through
the same validation and defaults. See [agent policy](../../../../docs/engineering/configuration/agent-policy.md)
for precedence/restart/cleanup behavior and the [commented mapping](../../../../config/examples/agent/lifecycle-policy.env.example).
No ambient environment is read.
