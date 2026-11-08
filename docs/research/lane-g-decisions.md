# Lane G decisions and boundaries

## GW-106: Enforce scopes from protected JSON

Bind the control-authorized workspace and authenticated key UUID to an
operator-declared tenant and application. Omit the configuration to retain
the existing path. When configured, deny unmapped keys. Extending the Go
admission contract would provide authoritative IDs and automatic enrollment
but crosses lane ownership and breaks compatibility with strict consumers.
Using an alias or caller-supplied app as identity would group unrelated
applications or let callers choose a new budget. A wrong manual mapping can
misattribute spend or deny a valid key until the operator corrects it.

Use exact rolling seconds and calendar UTC token windows. Commit token
reservations locally before generation and settle from verified final usage.
A shared quota service would coordinate instances but needs a deployment and
ownership decision. Memory-only token counters would reset on restart.
Retaining uncertain usage can overcharge an interrupted request up to its
maximum output but prevents repeat disconnects from avoiding the budget.
See [the design and sources](gateway-limits.md).

### Boundaries

The gateway protocol does not convey authoritative tenant, app or key
metadata and no shared quota store is wired. Product-wide GW-106 remains
open pending control-side metadata, policy management, and enforcement across
gateway instances. The opt-in runtime configuration and descriptive 429
messages need integration into the shared configuration and public API
contracts. Existing no-policy responses keep their prior body shape and text.
Lane G leaves the roadmap index to the coordinator.
