# Outgoing Agent session transport

This package owns the physical lifetime of outgoing gRPC Agent channels. The Agent
client and development simulator use the same implementation. Node authentication,
task state, pacing and exchange retries remain with their existing owners.

`New` retains the caller's verified TLS, authority, address and message budgets. It
adds gRPC connection observations without reading or logging request metadata,
credentials or payloads. `ReadAge` is called only after the first successful
authenticated heartbeat. It validates the optional version-1 duration hints and
binds the committed stream's real peer addresses to a verified TLS transport.
Missing or invalid hints and missing transport binding retain legacy behavior.

After authenticating a replacement between exchanges, its owner calls `Retire` on
the unused old channel. A worker waits for that original physical transport to end
or its existing hard-close deadline, then closes the channel outside gRPC's callback.
`Retirement.Close` releases the channel immediately and joins the worker. The
session owner keeps at most one retirement, defers preference probes while it is
pending, and joins it during shutdown or a simulated fault reset.

The package changes no lease, acknowledgement cursor or protocol frame and retries
no application request. It creates no second task executor. Warm replacement uses
temporary headroom within the configured connection and session budgets; it never
raises either budget. A failed probe can still require the existing reconnect path.
See [the design and acceptance boundary](../../../docs/decisions/0082-authenticate-a-replacement-before-agent-connection-age.md).

`Exchange` sends one frame and receives one response or final status. A send-side
EOF can represent an early remote refusal, so it reads the final receive status
before its caller classifies authentication, admission or transport failure. It
never repeats the send and does not turn an empty stream termination into success.

Focused checks are `go -C services/agent test -race ./sessiontransport` and the
Agent client's session tests. Real gateway age, certificates, leases and fleet
resources require the separate installed simulator drill.
