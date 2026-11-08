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
default route. The test checks refusal before file preparation, a busy port,
an explicit local DNS resolver, installation from the image archive, repeat
apply with the original credential, and readiness. Host and workload probes
must both be observed and blocked. Any other recorded destination fails the test.

The preflight test kills the installer after the administrator step and
before the control service starts. It reruns the same apply command and
checks that both entrances are ready and the administrator identity,
tenant, workspace, and credential bytes match the committed state.
The same run kills restore around the PostgreSQL exchange, during rollback
and after control starts. It invokes `recover-restore` in a new process and
checks both database OIDs and their data before reporting readiness.

### Check a bundled model with a cold runtime cache

Prepare the signed bundle with `prepare-bundle`. On a disposable Linux amd64
Docker host with the vLLM CPU image already cached, run as root with the
repository egress recorder and the pinned model catalogue:

```sh
bash deploy/offline/test-model-egress.sh "$PREPARED_MODEL_DIR" "$MODEL_CATALOG" \
  "$EGRESS_RECORDER" "$VLLM_CPU_IMAGE_BY_DIGEST" "$CPU_LIMIT" "$MEMORY_LIMIT"
```

This test verifies the catalogue file closure and runs one completion in a fresh
container with the model and tokenizer mounted read-only. It requires working
IPv4, IPv6 and DNS probes and fails if the runtime adds any packet. The
standalone CPU test pins the container's own name to loopback so startup never
needs to query the site resolver for itself.

## Verify PostgreSQL point-in-time recovery

Use the same Linux amd64 images and installer test binary as the preflight
run. Select `OWNSOLID_E2E_RUN='^TestPITRIsolatedDocker$'` when invoking
`tests/e2e/run-single-node.sh`. The test runs one Compose installation,
archives real WAL, verifies full and incremental bases, and replays to an
earlier UTC time. It withholds a required segment, kills an installer during
activation, and checks retained dependencies, later writes and foreign files.
See the [operator procedure](../../docs/operations/backup-and-restore.md#postgresql-point-in-time-recovery).

## Verify encrypted logical backup and restore

Use the same Linux amd64 fixture and egress recorder as the preflight test.
Set `OWNSOLID_E2E_RUN='^TestEncryptedBackupIsolatedDocker$'` when invoking
`tests/e2e/run-single-node.sh`. The test checks the real PostgreSQL dump and
restore, rejects an unrelated identity and changed or truncated ciphertext,
interrupts backup and restore, and checks the unencrypted commands.
See the [operator procedure](../../docs/operations/backup-and-restore.md#encrypt-an-operator-run-backup).

## Schedule verified backups

Configure a dedicated backup directory and keep count. The
[systemd timer instructions](../systemd/README.md) show how to enable it and
how to run the same job on demand.

## Collect support information

Run this on the installation host with access to the local Docker socket. Use an
unused archive path in a directory writable only by its owner.

```sh
ownsolid-install support-bundle --config /path/to/install.yaml \
  --output /private/diagnostics/ownsolid-support.tar.gz
tar -xOzf /private/diagnostics/ownsolid-support.tar.gz support.json
```

The private archive contains one JSON snapshot of the host, available space,
Docker, Compose service states and the database schema version when available.
It also includes selected configuration such as TLS modes, resource limits,
timeouts and retention, and up to 100 recent backup and restore events.
Events contain a UTC timestamp, operation and outcome. The installer excludes
operation details, container output, raw configuration files, environment values,
addresses, credentials and database records.

The collector works while the console is stopped. An installation with no
operation history has an empty event list. Unavailable collectors report fixed
codes. Review the archive locally before sharing it.

## Verify and install a signed single-node release

Use a trusted copy of `ownsolid-install` on the Linux amd64 Docker host with
Docker's containerd image store enabled. Copy the release archive to a mounted staging volume with room to hold the archive and
the expanded inventory. Keep the archive, policy file and independently
obtained release trust file private and owned by the operator. The verification
JSON needs the absolute `mount_root` and `trust_file` paths. See the
[factory instructions](../../tools/release/offline/README.md#current-single-node-release).

When the archive is the only file delivered, authenticate its SHA-256 against
the value supplied through the trusted release channel before extracting its
installer. Obtain the installer member path through the same channel. Keep the
archive read-only during bootstrap and use a private executable temporary directory.

```sh
set -eu
umask 077
test "$(sha256sum -- "$ARCHIVE" | cut -d ' ' -f1)" = "$EXPECTED_ARCHIVE_SHA256"
installer_dir=$(mktemp -d)
tar -xOzf "$ARCHIVE" -- "$INSTALLER_MEMBER" >"$installer_dir/ownsolid-install"
chmod 700 "$installer_dir/ownsolid-install"
"$installer_dir/ownsolid-install" verify-bundle --bundle "$ARCHIVE" \
  --verification "$VERIFICATION_POLICY"
```

Use that verified executable for the following commands. Provision the public
verification root separately from the archive.

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

## Upgrade or roll back the signed application release

Keep the previous archive and use a separate private backup directory. The
candidate must name an accepted predecessor schema and keep the same PostgreSQL
and telemetry image identities. Review the plan before starting maintenance:

```sh
ownsolid-install upgrade-plan --bundle /mnt/release/next.tar.gz \
  --verification /private/release-verification.json --config /private/site.yaml
ownsolid-install upgrade-bundle --bundle /mnt/release/next.tar.gz \
  --verification /private/release-verification.json --config /private/site.yaml \
  --backup-output /private/recovery
```

The plan reads the installed schema and lists the service stop, recovery point,
and target schema. Applying the candidate loads its signed images first, stops
the application writer, and produces a recovery point verified by restoring it
into a scratch database. It then migrates and starts the candidate. Keep the
backup location and archive unchanged when retrying a pending upgrade.
Ordinary backup and restore require the signed upgrade to be resolved first.

```sh
ownsolid-install rollback-bundle --bundle /mnt/release/previous.tar.gz \
  --verification /private/release-verification.json --config /private/site.yaml
```

The rollback accepts the exact previous signed release and checks that the live
database is at the schema that release expects. It changes the application
configuration and image, leaving the database and telemetry stores running.
When the schema has advanced, retain the recovery point. The ordinary
`restore` command adopts data on the active application's schema, which may
apply migrations again. In-place recovery of both the prior database schema
and application after a schema-changing upgrade is outside this procedure.

The isolated upgrade test uses the same runner with
`OWNSOLID_E2E_RUN='^TestSignedUpgradeIsolatedDocker$'`. Pass three extra signed
files, `upgrade.tar.gz` with a newer control schema, `patch.tar.gz` with a different
control image at the same schema, and `delta.tar.gz` for the incremental transfer
between them. They must use the fixture's independent release trust. In the
factory fixture, set
`OWNSOLID_CURRENT_FACTORY_RELEASE` and
`OWNSOLID_CURRENT_FACTORY_PREDECESSOR` for each candidate. For the incremental
candidate, pass the newer complete archive in `OWNSOLID_CURRENT_PATCH_BASELINE`.

### Upgrade from an incremental transfer

Keep the exact previous archive, including its signature. Copy the incremental
transfer into the same mounted workspace and apply it through the existing
upgrade procedure:

```sh
ownsolid-install upgrade-patch --baseline /mnt/release/previous.tar.gz \
  --bundle /mnt/release/delta.tar.gz --verification /private/release-verification.json \
  --config /private/site.yaml --backup-output /private/recovery
```

The installer compares the signed baseline digest with the installation's
release binding, reconstructs the complete candidate from the two local
files, and verifies every candidate payload before starting the upgrade.
The staging volume must hold the retained archives and reconstructed payloads.

### Reapply after a database restore

After a verified restore, run `apply` with the same site configuration. For
a signed installation, use `apply-bundle` with its active signed release.
Restore records the adopted PostgreSQL database after checking its schema,
runtime grants and restored authority. A later apply verifies that record and
preserves the restored administrator. A failed restore keeps its record with
the failed database when the previous database is put back.

Restores performed by older installers lack this record. To preserve changes
made after such a restore, first take a verified backup of the database
currently serving, then restore that backup with the updated installer.

The isolated `TestPreflightIsolatedDocker` run takes a database-aware backup,
restores it into a new PostgreSQL database, applies twice and checks that both
entrances and the original administrator still work.

### Read the notice inventory

```sh
ownsolid-install bundle-notices --bundle /mnt/release/ownsolid.tar.gz \
  --verification /private/release-verification.json
```

The command verifies the entire archive before printing its release digest,
notice paths and checksums, and the payload IDs referring to each notice.
Use `prepare-bundle` to read the actual signed files. The inventory reflects
the notice files selected and signed by the release manufacturer.
