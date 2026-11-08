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

## Collect support information

Run this on the installation host with access to the local Docker socket. Use an
unused archive path in a directory writable only by its owner.

```sh
ownsolid-install support-bundle --config /path/to/install.yaml \
  --output /private/diagnostics/ownsolid-support.tar.gz
tar -xOzf /private/diagnostics/ownsolid-support.tar.gz support.json
```

The private archive contains one JSON snapshot of the host, available space,
Docker, Compose service states, and the database schema version when available.
It works while the console is stopped. Unavailable collectors report fixed codes.
The archive contains no configuration files, environment values, logs, credentials
or database records. The installer writes it locally and never uploads it.

## Verify and install a signed single-node release

Use a trusted copy of `ownsolid-install` on the Linux amd64 Docker host with
Docker's containerd image store enabled. Copy the release archive to a mounted staging volume with room to hold the archive and
the expanded inventory. Keep the archive, policy file and independently
obtained release trust file private and owned by the operator. The verification
JSON needs the absolute `mount_root` and `trust_file` paths. See the
[factory instructions](../../tools/release/offline/README.md#current-single-node-release).

```sh
ownsolid-install verify-bundle --bundle /mnt/release/ownsolid.tar.gz \
  --verification /private/release-verification.json
ownsolid-install apply-bundle --bundle /mnt/release/ownsolid.tar.gz \
  --verification /private/release-verification.json --config /private/site.yaml
ownsolid-install status --config /private/site.yaml
```

Use [install.example.yaml](install.example.yaml) as the site configuration,
omitting `compose_file` and the entire `images` block. The release supplies
those inputs. The installer's signed path checks the local Compose input,
imports only tagless verified images by content ID, and confirms the database
schema against the signed manifest. It binds the install root to that manifest
before it prepares services. Re-run `apply-bundle` with the same archive to
resume. A different release and an existing legacy installation require an
upgrade procedure. The TLS, entitlement trust and operator credentials are
site-owned.

Use `prepare-bundle` with the same verification inputs and `--output` pointing
to a new directory under a private parent on the staging volume to make the
signed model, chart, plug-in, font or documentation files available for their
own import procedures. This command verifies the entire archive first and
writes the preparation journal beside the output directory.

For the signed zero-egress test, stage a bundle with
`TestManufactureCurrentRelease` on the test host and use the installer test
binary from the preflight instructions. The factory test expects Docker saves
named for the four image IDs and files named `compose`, `installer`,
`operator-docs` and `test-notice` under the private staging `assets` directory.
It reads `OWNSOLID_CURRENT_FACTORY_STAGE`, `OWNSOLID_CURRENT_FACTORY_KEY_DIR`
and `OWNSOLID_CURRENT_FACTORY_SCHEMA`. Put the resulting `ownsolid.tar.gz` and
`release-trust.json` in a staging directory and pass that directory as
`OWNSOLID_E2E_MODEL_ARCHIVE` to the existing isolated runner. Set
`OWNSOLID_E2E_RUN='^TestSignedBundleIsolatedDocker$'`. The test starts with an
empty Docker daemon and checks that a truncated bundle cannot load an image.
