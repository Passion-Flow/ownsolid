# Compose topology

`compose.yaml` is the single-node topology: PostgreSQL, the `metrics` and `logs` telemetry
stores, the serving control plane and a `control-operator` service for schema and
first-run tasks. `.env.example` documents its
variables.

`ownsolid-install` renders the variables from the typed configuration and runs this file;
see [Install](../../docs/operations/install.md). Do not run it by hand for an install. To
inspect an installation:

```sh
docker compose --env-file <install_root>/compose.env -f <install_root>/compose.yaml ps
```

Rules a change must keep:

- Every service has `pull_policy: never`.
- Only `control-operator` mounts the operator directory, which holds the privileged
  database identity. Serving uses a restricted role.
- The `state` network stays internal. Only `control` joins `edge`.
- Every store keeps its fixed address on `state`, and clients connect by that address,
  never by name: Docker forwards unknown names to the site resolvers whenever a store is
  stopped ([ADR 0008](../../docs/decisions/0008-zero-egress-harness.md)).
- `control` never depends on `metrics` or `logs`: losing telemetry must not stop the
  platform ([ADR 0012](../../docs/decisions/0012-telemetry-store-boundary.md)). The stores
  have no authentication of their own, so nothing else joins `state`.
- A new variable is added to `.env.example` and to `ComposeVariables` in
  `deploy/offline/internal/install`. A test compares all three.

## Fleet history deployment prerequisites

The shipped topology has one metrics store on the private HTTP state
network. Fleet history's remote reader requires HTTPS. A long retention
deployment needs distinct raw and aggregate stores, private TLS trust,
the recording-rule evaluator and separate persistent data. Preserve the
existing metrics push and readiness checks when adding native TLS to
the raw store. The installer currently configures only metrics and log
retention.

The older monitoring module examples refer to an overlay absent from
this topology. Use the [current installation boundary](../../docs/research/telemetry-retention-tiers.md#current-installation-boundary)
for the precise Control reference, required installation inputs and
remaining deployment acceptance.
