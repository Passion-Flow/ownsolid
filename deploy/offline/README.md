# Offline delivery

`cmd/install` is `ownsolid-install`, the install path; see
[Install](../../docs/operations/install.md) and
[`install.example.yaml`](install.example.yaml). `internal/install` holds its typed
configuration and steps. `backup` and `restore` live there too, in `backup.go` and
`restore.go`; see [Backup and restore](../../docs/operations/backup-and-restore.md).

```sh
go -C deploy/offline build -o ownsolid-install ./cmd/install
go -C deploy/offline test -count=1 ./...
```

## Check a host before installation

Set the `preflight` block in [install.example.yaml](install.example.yaml) to the site's
Linux kernel, storage, memory and clock requirements. Run this on the Linux amd64
Docker host:

```sh
ownsolid-install preflight --config /path/to/install.yaml
ownsolid-install apply --config /path/to/install.yaml
```

The checks require a local Unix Docker socket. They test the configured HTTPS port
and recognize a running control container only if its published address, Compose
project and installed configuration path match. A refused preflight leaves the
installation directory untouched. On a later step failure, fix the reported cause
and run `apply` again with the same configuration to resume.

Set the Docker budget for the loaded and expanded images and the data budget for each
filesystem that holds application data. When Docker and application data share a
filesystem, both reserves must fit. Set `containerd_root` to the actual image store
directory when Docker uses its containerd image store. The checks measure free bytes
at that instant and cannot reserve them. The clock check uses the configured lower
bound and optionally the Linux clock synchronization state.

Use explicit local host mappings or a private DNS address for the two advertised
browser hostnames. The installer uses only those sources for preflight. Check
resolution from a client separately when its resolver differs from the installation
host. Set `require_nvidia` for a host that must have an existing NVIDIA driver and
Docker runtime. The check uses `nvidia-smi` locally. It does not run a model.

Existing configurations without the block retain the original Docker and Compose
checks in `apply`. The standalone `preflight` command requires the block.

### Run the preflight installation with zero egress

Build the normal isolated runner inputs, then substitute the installer module's
preflight test binary for its usual test payload. Use a fresh lane-specific staging
directory on the Linux Docker test host. Supply locally staged Linux amd64 images.

```sh
make e2e-binaries
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C deploy/offline test -c \
  -trimpath -o ../../bin/e2e/e2e.test ./internal/install
```

Copy `bin/e2e/*` and `tests/e2e/run-single-node.sh` into the staging directory.
From that directory on the test host, run:

```sh
OWNSOLID_E2E_RUN='^TestPreflightIsolatedDocker$' bash run-single-node.sh "$PWD" \
  "$CONTROL_IMAGE" "$POSTGRES_IMAGE" "$METRICS_IMAGE" "$LOGS_IMAGE" "$DIND_IMAGE"
```

The runner starts Docker with `--network none` and makes the recorder its only
default route. The test checks the refusal before file preparation, a busy port,
installation from the image archive, repeat apply with the original credential,
and readiness. Host and workload probes must both be observed and blocked. Any
other recorded destination fails the test.
