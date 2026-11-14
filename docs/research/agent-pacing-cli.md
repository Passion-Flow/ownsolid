# Cell agent pacing from the Admin CLI (DEV-102, ARC-007)

[Kubernetes Node Status](https://kubernetes.io/docs/reference/node/node-status/)
separates heartbeat leases from node status and relates update cadence to
liveness. [Nomad's heartbeat configuration](https://developer.hashicorp.com/nomad/docs/configuration/server#client-heartbeats)
applies jitter and a grace interval to avoid declaring healthy clients down.

[ADR 0013](../decisions/0013-agent-pacing.md) and the registered
[Nodes contract](../../api/openapi/v1/nodes/openapi.json) already implement
cell pacing. The read returns the four stored values, version, update time,
and online window. The platform Admin write requires the complete setting,
the current version, and a reason. The server compares the version, checks
the liveness window, and persists one audit event in the same transaction.
Agents consume the retained settings in their next heartbeat. Follow the
existing site and cell selection, Admin session, TLS, CSRF and mutation
error path.

Require the operator to supply each setting explicitly, including zero
jitter, and confirm the cell-wide change. Accept the version from a read.
Keep policy validation on the server so the CLI uses the installation's
current window. A client-side merge followed by a silent retry could change
the operator's intent after another Admin updates the cell. With a lost
response, read the current settings and compare their version before
issuing another update.

The existing CLI, test Go module, PostgreSQL and Python fixture dependencies
are documented in [Admin audit CLI licenses](admin-audit-cli.md). Those
licenses permit commercial use, redistribution under their notices and
offline use. No dependency, model or dataset is added.

Verify a compiled CLI over Admin HTTPS and PostgreSQL with only loopback.
Check the returned window, missing confirmation and required fields, unsafe
settings, a reply lost after commit, and a stale retry with no duplicate
event. Confirm two writes with one version have one winner, and the observed
read returns that version. Verify a demoted Admin cannot read or write.
Use the existing Admin test harness and leave the Distribution fixture in
its separate package.

The Linux amd64 Admin fixture ran with PostgreSQL 18.6 and only loopback.
It refused omitted confirmation, omitted zero-capable jitter, unsafe
pacing and a demoted administrator. After a committed reply was lost, the
CLI told the operator to read current state. The read saw version two,
the stale replay returned VERSION_CONFLICT, and simultaneous replacements
at version two left one version-three result and one refusal. The two
successful writes left two audit events. The existing Admin, alias and
managed-gateway workflows passed in the same run. Archive-dependent
exporter and model import cases used their normal missing-fixture skips.
