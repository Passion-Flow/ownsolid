# Compose topology

`compose.yaml` is the single-node topology: PostgreSQL, the serving control plane and a
`control-operator` service for schema and first-run tasks. `.env.example` documents its
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
- A new variable is added to `.env.example` and to `ComposeVariables` in
  `deploy/offline/internal/install`. A test compares all three.
