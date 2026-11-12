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

## Configure license import capacity

Set the Control policy in the authoritative install.yaml:

```yaml
control_settings:
  runtime:
    licensing:
      maximum_imports: 128
```

Apply that same configuration through the normal installer path:

```sh
ownsolid-install apply --config /path/to/install.yaml
```

Control validates a value from 1 to 1024; the shared default is 64. The installer
preserves this runtime setting in both serving and operator configuration on
repeated apply. Edit install.yaml to change it. Generated control.yaml follows
that input on the next apply.

The license trust file remains the separate license.trust_file reference.
This setting does not change the signed license format. Issuer-signed lifecycle,
renewal history and deployment migration remain part of DEL-109.

## Deploy private fleet-history stores

`fleet-history` installs an independent raw store, long store and
recording-rule evaluator. The existing platform process-metrics store
keeps its current configuration. Load the approved VictoriaMetrics and
vmalert images and retain their notices before running this command.
Both image values must be immutable local image IDs or repository digests.

Create a protected, root-owned site YAML file. Replace the image values
with the exact approved digests. Select unused private ports and a
nonoverlapping private subnet. The evaluator listener must use a loopback
IPv4 address. The two store listeners accept loopback or private IPv4.

```yaml
format_version: 1
root: /srv/ownsolid-fleet-history
project: ownsolid-fleet-history
uid: 10001
gid: 10001
subnet: 172.29.248.0/28
metrics_image: sha256:<approved-local-metrics-image-id>
evaluator_image: sha256:<approved-local-vmalert-image-id>
evaluator_listen: 127.0.0.1:18880
raw:
  listen: 10.20.30.40:18428
  retention: 2d
  tls:
    ca_file: /private/history/raw-ca.pem
    certificate_file: /private/history/raw.pem
    private_key_file: /private/history/raw-key.pem
long:
  listen: 10.20.30.40:28428
  retention: 90d
  tls:
    ca_file: /private/history/long-ca.pem
    certificate_file: /private/history/long.pem
    private_key_file: /private/history/long-key.pem
store_resources: {memory: 256m, cpus: "1", pids: 128}
evaluator_resources: {memory: 128m, cpus: "1", pids: 128}
rules_file: /private/history/fleet-rollup-rules.yaml
rules_sha256: <approved-rule-file-sha256>
monitoring_file: /private/history/monitoring.json
control_ca_file: /private/history/control-ca.pem
request_seconds: 120
```

All referenced inputs must be protected regular files outside the
deployment root. Use mode 0600 and canonical absolute paths. The raw
certificate must cover the published IP and the subnet's second host
address, `172.29.248.2` in this example. The long certificate covers the
published IP and third host address, `172.29.248.3`. The CA files contain
current CA certificates only. `control_ca_file` must verify both published
endpoints. Keep its applicable roots separate from the evaluator's raw
and long CA files.

Use the approved local
[fleet recording rules](../../services/control/internal/modules/monitoring/alerting/fleet-rollup-rules.yaml)
and their SHA-256. The protected monitoring JSON uses the existing module
policy. Its `fleet_history` object must select exactly the two configured
HTTPS origins, a raw horizon inside raw retention, a maximum range inside
long retention, and a settling delay from 30 to 300 seconds.

```sh
ownsolid-install fleet-history validate --config /private/history/site.yaml
ownsolid-install fleet-history apply --config /private/history/site.yaml
```

Apply runs as root on Linux amd64. It verifies local images, validates the
local rules with vmalert and starts the services with pulling disabled.
The selected Docker endpoint must be a local Unix socket.
An internal network carries evaluator-to-store traffic. A separate bridge
publishes the configured private ports; Docker does not publish ports for
containers attached exclusively to internal networks. The evaluator's health
port is loopback-only.
Each store retains its own data directory. JSON success identifies the
checked monitoring file and Control CA file under an immutable input
generation. The operator log records the deployment attempt and outcome.
Inspect runtime state with `docker compose -f
/srv/ownsolid-fleet-history/compose.yaml ps`.

To rotate issuers, provision old and next roots first, apply, replace the
leaf and key, then apply again. After removing old roots, apply the new
trust. Apply stops the evaluator and both stores before restart, closing
connections established under old trust. Failed input validation leaves
the running generation active. After an interrupted stop or start, repeat
apply using the same root, project and endpoint assignment. The prior
input generations and store data remain available for recovery.

Set the ordinary install configuration's
`telemetry.fleet_history_control_directory` to the emitted files' parent
directory and run the normal `apply` or signed `apply-bundle`. The installer
copies both protected files, selects the existing `monitoring_config_file`
and process CA bundle, and restarts Control. Repeat this step after every
history policy or trust change, including issuer removal. For containerized
Control, store origins must use private host IPs reachable from its network;
loopback-only listeners are suitable for host clients, not this integration.
The existing HTTP process-metrics client has its own store. Public history dispatch
and installed Admin/Portal acceptance follow the
[OBS and DEV integration](../../docs/research/telemetry-retention-tiers.md#installer-continuation).
The local-only coordinated backup profile refuses this optional integration;
it does not cover the separate history stores. Ordinary database backups
remain logical PostgreSQL exports. Protect and back up the separate history
store data using the stores' supported snapshot procedure.

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

When the administrator transaction has already committed, Apply uses the
retained credential file to verify the same account. Keep that file protected
and retain it through recovery. A credential write failure before publication
leaves the final path absent and can be retried. If an older installer left an
invalid final file, investigate the database and preserve that file before
attempting recovery. Apply also checks that a generated HTTPS certificate
matches its private key. An interrupted leaf update is repaired under the
existing installation CA on the next Apply.

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
The same test covers loss of the installer after scratch loading, around the
PostgreSQL exchange, during rollback and after control starts. It invokes
`recover-restore` in a new process and checks both database OIDs and their data
before reporting readiness.

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

## Compare upgrades before and after a logical restore

Build the Linux amd64 control binaries from the historical repository worktrees
and the candidate checkout. Use a private staging directory and keep the published
migration file byte-for-byte. The fixture verifies its checksum against the
historical ledger.

```sh
git worktree add --detach "$SCHEMA55_TREE" 3414559f7aa1a1124841807dc345b45382a1e8b8
git worktree add --detach "$SCHEMA56_TREE" f98a160dcdfb39ec5f841e4a854753d8c20a25be
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
go -C "$SCHEMA55_TREE/services/control" build -trimpath -o "$STAGE/control-schema55" ./cmd/server
go -C "$SCHEMA56_TREE/services/control" build -trimpath -o "$STAGE/control-schema56" ./cmd/server
go -C services/control build -trimpath -o "$STAGE/control-candidate" ./cmd/server
go -C deploy/offline test -c -trimpath -o "$STAGE/install.test" ./internal/install
go -C tests/e2e build -trimpath -o "$STAGE/egress-recorder" ./cmd/egress-recorder
cp "$SCHEMA56_TREE/services/control/migrations/postgresql/000056_cell_clusters.up.sql" "$STAGE/000056.up.sql"
```

On the Linux amd64 Docker host, supply that directory and the staged PostgreSQL
image digest from [install.example.yaml](install.example.yaml). Run as root.

```sh
bash deploy/offline/test-restored-schema-upgrade.sh "$STAGE" "$POSTGRES_IMAGE" blocked
```

Use `blocked` to reproduce the current candidate failure, or `current` to
require that a repaired candidate upgrades the restored schema and can repeat
the command. The historical 000056 comparison runs in both modes. The installer
test uses its shipped custom-archive dump and load functions with the real
PostgreSQL clients, compares the migration ledger and row fingerprints, and
prints the check constraint differences. One disposable database container
holds all test databases and publishes no ports. The runner blocks and records
IPv4, IPv6 and DNS probes before starting PostgreSQL, then checks for any further
outbound attempt.

## Verify encrypted logical backup and restore

Use the same Linux amd64 fixture and egress recorder as the preflight test.
Set `OWNSOLID_E2E_RUN='^TestEncryptedBackupIsolatedDocker$'` when invoking
`tests/e2e/run-single-node.sh`. The test checks the real PostgreSQL dump and
restore, rejects an unrelated identity and changed or truncated ciphertext,
interrupts backup and restore, and checks the unencrypted commands.
See the [operator procedure](../../docs/operations/backup-and-restore.md#encrypt-an-operator-run-backup).

## Take a coordinated local backup

Use the shipped single-node Linux Compose configuration and a root-owned
private output directory outside `install_root`. This maintenance command
keeps control offline until it has copied and verified the backup. It closes
PostgreSQL to ordinary database connections and checks that the copied published model files and
installation keys belong to the same interval as the verified SQL dump.

```sh
ownsolid-install backup-coordinated --config /private/site.yaml --output /srv/ownsolid-backups
```

Use the ordinary restore commands for the resulting logical backup.
This profile covers generated Compose and control settings, the local
PostgreSQL database, published local models and installation keys. It
refuses custom control settings, PITR and rows that refer to uncovered
object, dataset, training or preparation state. Keep privileged database
clients and direct filesystem maintenance out of the window. The backup
still requires enough space for the complete verified model copy. Keep the
output private because it contains keys.

The command restores the original database connection limit and control
container state on success, failure or cancellation. If the installer is
killed or reports `INSTALL_BACKUP_RECOVERY_REQUIRED`, run the following
with the original site settings. A partial directory is not a backup.

```sh
ownsolid-install recover-backup --config /private/site.yaml
ownsolid-install status --config /private/site.yaml
```

The isolated fixture uses `OWNSOLID_E2E_RUN='^TestCoordinatedBackupIsolatedDocker$'`.
Supply a directory holding the approved USTAR `model.tar` via
`OWNSOLID_E2E_MODEL_ARCHIVE` and use the installer test payload and the
isolated runner described above.

## Restore a signed release onto an empty site

Use a trusted installer that supports `restore-bundle`, the exact signed
release that produced the backup, and its independently provisioned public
verification policy. Put the release on a mounted volume with space for
verification and extraction. Keep the backup on private storage outside the
installation. Use an absolute, root-owned mode 0700 directory for a plaintext
backup. For an encrypted archive, supply the protected age identity as
described above. Prepare the site's TLS, license trust and other local files.
Choose a fresh install root on a filesystem that supports hard links and a
Compose project unused by other installations.
Omit `compose_file` and `images` from the site YAML.

```sh
ownsolid-install restore-bundle --config /private/site.yaml \
  --bundle /mnt/release/ownsolid.tar.gz --verification /private/release-verification.json \
  --from /srv/ownsolid-backups/ownsolid-backup-20261104T080000Z
ownsolid-install status --config /private/site.yaml
```

For an encrypted backup, use the archive as `--from` and add
`--identity-file /mnt/recovery/ownsolid-identity.txt`. Begin with the restore
command so the site inherits the source keys. The installer verifies the release
and checks the backup before loading images or preparing PostgreSQL. An invalid
encrypted input can leave an operation lock file and a private staging marker.
New signed backups record the manifest digest. Older backups require matching
control and PostgreSQL image IDs and schema, and the command reports this
compatibility path. Use the backup-producing release's recovery installer for
new signed backups.

If the writer fails to start during a cold restore, the original
unbootstrapped database is restored and the writer stays stopped. Fix the
reported issue and rerun `restore-bundle` with the same inputs. If a restore
record remains, follow the
[interrupted restore procedure](../../docs/operations/backup-and-restore.md#recover-an-interrupted-restore).
After recovery, trust the configured site CA, confirm both entrances, sign in with your
current administrator credentials and verify the data you recovered.

## Verify a cold restore of a signed release

Stage one single-node Linux amd64 archive with its independently provisioned
public trust file. For a development fixture, use the release factory's
`TestManufactureCurrentRelease` with real image exports and an ephemeral
signer. Put `ownsolid.tar.gz`, `release-trust.json` and an executable
`legacy-ownsolid-install` in the delivery directory. Build the legacy binary
from a revision that supports signed installs and predates release digests
in backup manifests, such as `33095551`.

Build the installer test payload as shown for preflight. Select
`OWNSOLID_E2E_RUN='^TestSignedRestoreIsolatedDocker$'` and point
`OWNSOLID_E2E_MODEL_ARCHIVE` at the delivery directory for
`tests/e2e/run-single-node.sh`. The test creates legacy plaintext and current
encrypted backups of one installed release, then removes the four images
from its private daemon before restoring onto separate empty targets. It
rejects corrupt inputs and an unrelated identity, injects a writer startup
failure after the PostgreSQL exchange, retries the legacy restore, and
checks both browser entrances with the recovered administrator. Host and
workload probes must reach the egress recorder. Any unexpected destination
fails the test. A native namespace runner can optionally provide an empty
disk-backed mount with `OWNSOLID_E2E_RELEASE_MOUNT` in place of the test's tmpfs.

To include a model, stage `model.tar` beside the release archive. Use a USTAR
archive of the exact files in the [approved model catalogue](../../api/agent/v1/model-catalog.json).
The drill uses a temporary test entitlement to publish the model through Admin
before backing up. It verifies the restored model is ready through Admin and
checks every restored file's size and SHA-256 on both targets.

### Qualify the exact production release

Use the same isolated runner and choose
`OWNSOLID_E2E_RUN='^TestProductionSignedRestoreIsolatedDocker$'`. Supply the
exact published archive as `ownsolid.tar.gz` and the independently trusted
public root as `release-trust.json`. Put the independently approved lowercase
archive SHA-256 in a root-owned mode 0600 `expected-archive.sha256` file
alongside the staged inputs. The test compares that digest with the fully
verified archive before accessing the Docker daemon. Use an isolated,
capacity-checked disk-backed staging mount for large release media.

Build the installer from the candidate release. This qualification uses it
to create both plaintext and age backups, and verifies the candidate manifest
binding on both. The legacy regression still uses the historical installer.
The production gate passes only after restoring both backups onto empty sites
and completing the failure, retry, readiness and egress checks.

## Schedule verified backups

The host timer and the local `backup-history` JSON query are documented in
[scheduled backup operations](../systemd/README.md#read-scheduled-backup-history).

Configure a dedicated backup directory and keep count. The
[systemd timer instructions](../systemd/README.md) show how to enable it and
how to run the same job on demand.

## Collect support information

Run this on the installation host. Docker state is included when the local
socket is available. Use an unused archive path in a directory writable only
by its owner.

```sh
ownsolid-install support-bundle --config /path/to/install.yaml \
  --output /private/diagnostics/ownsolid-support.tar.gz
tar -xOzf /private/diagnostics/ownsolid-support.tar.gz support.json
```

The private archive contains one JSON snapshot of the host, available space,
Docker, Compose service states and the database schema version when available.
It also includes selected configuration such as TLS modes, resource limits,
timeouts and retention, and up to 100 recent backup and restore events.
Events contain a UTC timestamp, operation and outcome. If a protected restore
record is pending, the report includes its validated phase even when Docker
cannot be reached. The record identifies the local recovery phase. Use
`recover-restore` to verify PostgreSQL placement. A validated coordinated
backup intent reports `recovery_required` or `fence_outcome_unconfirmed`.
These values describe the recorded state. Use `recover-backup` to verify
the current writer and database fence. An unreadable or unknown intent
produces a fixed collection issue. The installer excludes operation details, container output, raw configuration files, environment values,
addresses, credentials and database records.

Before collecting, the installer durably records a diagnostic attempt in the
installation's protected operator/operations.log. It uses the executing
process's real and effective numeric user IDs. A completed record includes the
archive's SHA-256 and byte length. A failed or incomplete attempt remains in the
log; an explicit retry creates a new attempt. Keep this log with the installation
and do not truncate it to bypass a recording error. It contains local operator
records and has not been imported into the Control audit trail.

The operator directory must belong to the executing user at mode 0700, and
operations.log must be an owned regular file at mode 0600. An unsafe or
unwritable log stops collection. If the terminal record cannot be confirmed,
the installer withdraws its new archive and returns an error. Preserve the
operator log and inspect the selected output path if cleanup is unconfirmed.

### Collect managed cluster information

On a workstation with access to the private management endpoints, use
`--cluster-config` in place of `--config`. The protected JSON input selects
one or both sources and a total collection budget of 1 to 60 seconds.

```json
{
  "format_version": 1,
  "timeout_seconds": 10,
  "management": {
    "origin": "https://admin.site.invalid",
    "site_id": "default",
    "cell_id": "default",
    "ca_file": "/private/diagnostics/admin-ca.pem",
    "credential_file": "/private/diagnostics/admin-session"
  },
  "helm": {
    "origin": "https://kubernetes.site.invalid:6443",
    "namespace": "ownsolid",
    "release": "ownsolid",
    "ca_file": "/private/diagnostics/kubernetes-ca.pem",
    "credential_file": "/private/diagnostics/kubernetes-token"
  }
}
```

Run as the owner of the files and keep the JSON and referenced files at
mode 0600. The JSON input's directory also holds this collector's
operations.log; it must belong to the executing user and must not be writable
by other users. The same durable admission and archive-result rules apply. Use private HTTPS origins with their own trusted CA files. The
management credential is the value of an active platform Admin session.
You can point `credential_file` directly at the protected session file from
[the operator CLI](../../tools/cli/README.md#configure-and-sign-in).
The CLI names the file `session-<sha256>` using the SHA-256 of the canonical
Admin origin. Use the same origin for login and diagnostics.

For Kubernetes, use a short-lived service account token for the API
audience. Bind the account to a namespaced Role whose two rules grant
`get` on `apps/deployments` and on core `services`. Set each rule's
`resourceNames` to the exact `<release>-control` name. Write the token
directly to a mode 0600 file with the cluster's existing operator tools.

```sh
ownsolid-install support-bundle --cluster-config /private/diagnostics/cluster.json \
  --output /private/diagnostics/cluster-support.tar.gz
tar -xOzf /private/diagnostics/cluster-support.tar.gz support.json
```

The report identifies Control's cluster and server states as recorded,
with member counts. It separately includes Helm Deployment rollout state
and replica counts, and the named Service's HTTPS configuration state.
A configured Service does not establish ingress or endpoint health.
Permission denial, timeout and unavailability appear as fixed collection
issues while successful reads remain available. Site, cell, release and
namespace names, endpoints, credentials and raw response details stay
outside the archive.

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

## Bootstrap a site Registry

On a Linux amd64 host with local Docker Engine and its containerd image store,
stage a signed release that includes the approved Registry image. Reuse the
protected verification policy and separately provisioned public trust used by
`verify-bundle`. Allow space for the staged archive, the verified extracted
assets, the loaded Registry image and its persistent content store.

Create a root-owned mode 0600 `registry.yaml`. Use a dedicated empty root under
an existing root-owned directory. Select a private IPv4 address assigned to
the host, an available port, a Docker container name reserved for this
Registry and a dedicated unprivileged runtime user and group.

```yaml
root: /var/lib/ownsolid-registry
name: ownsolid-site-registry
listen: 10.42.0.10:5443
uid: 18080
gid: 18080
request_seconds: 300
tls_ca_file: /etc/ownsolid/registry/ca.pem
tls_certificate_file: /etc/ownsolid/registry/site.pem
tls_key_file: /etc/ownsolid/registry/site-key.pem
credential_file: /etc/ownsolid/registry/pull-credential.json
```

Provide the CA, the server certificate chain with an IP subject alternative
name for the listen address, the matching key and the credential as separate
root-owned mode 0600 files. The credential file has JSON fields `username`
and `password`. Supply a generated password of 20 to 72 bytes. Keep the
certificate and credential inputs outside the Registry root.

```sh
ownsolid-install registry-bundle --bundle /mnt/release/ownsolid.tar.gz \
  --verification /etc/ownsolid/release-verification.json \
  --config /etc/ownsolid/registry.yaml
```

The JSON output lists immutable `ownsolid/release/<asset-id>@sha256:...`
references at the private endpoint. Install the public site CA under
`/etc/docker/certs.d/<host>:<port>/ca.crt` on each Docker node. Use the node's
private Docker credential store and supply the password to `docker login
--password-stdin` from its protected credential file. Pull one of the exact
references reported by the installer.

The command stages an authenticated writer on loopback and makes the
private address available after content verification. To resume a failed
install, correct the reported cause and repeat the command with the same
archive, configuration and root. A repeat checks the existing content and
retains the bound container. The Registry serves one signed release per root.
The release reader can pull and has no upload rights. Its local operation
records live in `operations.log` in the Registry root.

For Kubernetes delivery, use the signed image values overlay and the
[offline Helm chart](../helm/ownsolid/README.md).

### Exercise the isolated Registry path

On a disposable Linux amd64 host with Docker, containerd, OpenSSL, curl, jq,
iproute2 and Python 3, run the installer and the repository egress recorder
against a current signed Registry release and a current signed legacy release
with the same public trust. The runner requires a fresh mode 0700 work
directory and privileges to create network and mount namespaces and an ext4
loop mount.

```sh
bash deploy/offline/test-registry-egress.sh "$PRIVATE_WORK_DIR" "$INSTALLER" \
  "$EGRESS_RECORDER" "$REGISTRY_RELEASE" "$LEGACY_RELEASE" "$PUBLIC_TRUST"
```

The two empty Docker engines share only a private veth. Each namespace routes
external IPv4, IPv6 and DNS to the recorder. The test requires the deliberate
probes and fails on any other external attempt. It checks refusal before
initialization, repeat installation, TLS and authenticated pulls from the
second node, missing digest refusal and read-only serving. The work directory
contains disposable test credentials, media and logs.

## Prepare RKE2 artifacts for Agent delivery

Use a protected verification policy with the independently supplied release
trust file and the RKE2 requirement:

```json
{
  "mount_root": "/mnt/release",
  "trust_file": "/etc/ownsolid-release/public-trust.json",
  "require_rke2_components": true
}
```

Prepare to a new directory on that mount, under a root-owned mode 0700
parent. The command verifies the signature, approved RKE2 inventory and
all asset bytes before writing the prepared files. This policy requires
the linked RKE2 SPDX and notice archive. Use `require_rke2: true` to
prepare an earlier signed RKE2 release with the compatible inventory policy.

```sh
ownsolid-install prepare-bundle --bundle /mnt/release/ownsolid.tar.gz \
  --verification /etc/ownsolid-release/rke2-verification.json \
  --output /mnt/release/private/prepared
```

From `prepared/rke2`, install the binary, core archive and Canal archive
into a new directory for this release. Give only the control and Agent
gateway runtime identities read access. Keep the directory and its files
protected from writes by those identities. Recheck the copied files using
the three exact rows of the prepared `sha256sum-amd64.txt` before exposing
the new directory to the services. The full upstream checksum file also
lists other release variants that are outside this group.

Mount the completed artifact directory read-only for the control and Agent
gateway services and set these two fields in their existing control
configuration. Use paths as seen by each process:

```yaml
kubernetes_cells:
  artifact_directory: /var/lib/ownsolid/kubernetes/rke2/v1.36.6+rke2r1
  join_token_key_file: /run/secrets/cell-join-token.key
```

Provision the same independently generated key file, at least 32 random
bytes, with mode 0600 and owned by the service identity on each host that
runs a process that seals or opens join tokens. Keep the key outside the release archive.
Restart those services after updating their configuration. Kubernetes-profile
Agents receive the three approved files over the existing authenticated
artifact transport and install them locally when assigned a cluster join.
The signed single-node Compose apply continues to install the control
plane. This preparation hands the RKE2 inputs to the cell services.

## Preflight the joint RKE2, Registry and Helm release

Use one signed archive for preparation, the cold worker check and the Helm
image values. Keep its independently recorded archive and manifest SHA-256
with the public trust policy. Select `require_rke2_components: true` for the
joint fixture, and retain the signed Registry group and Chart asset. The
fixture signer, site private keys and pull credentials live outside the archive.

Compile the lightweight installer test for Linux amd64:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C deploy/offline test -c \
  -trimpath -o "$INSTALLER_TEST" ./internal/install
```

In a disposable namespace with only the repository egress recorder as its
IPv4 and IPv6 route, mount the signed archive read-only under the verification
policy's mount root. Set `TMPDIR` to a private writable directory on that
mount. The protected JSON file in `OWNSOLID_SIGNED_HELM_PREFLIGHT` names
`installer`, `helm`, `archive`, `verification`, `archive_sha256`,
`manifest_sha256`, `chart_asset`, `registry_config`, `site_values`,
`browser_ca`, `browser_certificate`, and `browser_key`. Use the actual
site Registry configuration and Helm site values with their private
certificate files. Run the existing installer and local Helm 4.3.1 through
the fixture:

```sh
OWNSOLID_SIGNED_HELM_PREFLIGHT="$PREFLIGHT_INPUTS" "$INSTALLER_TEST" \
  -test.v -test.run '^TestSignedHelmBundlePreflight$' -test.timeout "$PREFLIGHT_TIMEOUT"
```

The preflight checks prepared release identity and the Chart bytes, verifies
the Registry address certificate and both browser hostnames, and matches
the overlay to the signed OCI manifest. The render checks the init and
serving image references, pull Secret, exact SNI routes and existing PVC
claim. Confirm the recorder captured the three deliberate IPv4, IPv6 and
DNS probes and no workload traffic.

### Qualify the cold RKE2 worker first

Prepare the same archive with the strict RKE2 policy. Give the ARC cold
diagnostic the prepared artifacts and keep the original archive and its
digests for Registry bootstrap. Use a disposable, empty RKE2 data directory
and the existing managed enrollment and CNI diagnostic. Retain the node
readiness, kubelet and containerd diagnostics, CNI Pod events and recorder
output on a failure. Require the worker and its CNI and DNS path to pass
before scheduling Helm. Use the private site CA on each pulling worker
and the explicit signed Registry authority. Configure fallback denial
for any mirror and use the namespace-local pull Secret from the chart
procedure.

Record the cold diagnostic node and CNI state with the cell's own kubeconfig.

```sh
kubectl --kubeconfig "$CELL_KUBECONFIG" get nodes -o wide
kubectl --kubeconfig "$CELL_KUBECONFIG" -n kube-system get pods -o wide
kubectl --kubeconfig "$CELL_KUBECONFIG" wait --for=condition=Ready \
  "node/$RKE2_WORKER" --timeout "$INSTALL_TIMEOUT"
```

For the actual Helm drill, bootstrap the Registry and regenerate the overlay
from the same archive. Use a cold eligible worker for the first pull of the
Control image. Before applying the chart, record that worker's CRI inventory
and check that the signed Control manifest reference and config ID are absent:

```sh
ownsolid-install registry-bundle --bundle "$RELEASE_ARCHIVE" \
  --verification "$VERIFICATION" --config "$REGISTRY_CONFIG"
ownsolid-install helm-images --bundle "$RELEASE_ARCHIVE" \
  --verification "$VERIFICATION" --registry "$REGISTRY_AUTHORITY" >"$VERIFIED_IMAGE_VALUES"
CONTROL_DIGEST=$(jq -er '.images.control' "$VERIFIED_IMAGE_VALUES")
CONTROL_REF="$REGISTRY_AUTHORITY/ownsolid/release/control-image@$CONTROL_DIGEST"
CONTROL_CONFIG=$(jq -er '.assets[] | select(.id == "control-image") | .image_config_digest' "$PREPARED/manifest.json")
"$RKE2_CRICTL" --runtime-endpoint "$CRI_ENDPOINT" images -o json >"$COLD_IMAGES"
jq -e --arg digest "$CONTROL_DIGEST" --arg config "$CONTROL_CONFIG" \
  '(.images | type == "array") and all(.images[];
    .id != $digest and .id != $config and
    all((.repoDigests // [])[]; (split("@") | last) != $digest))' "$COLD_IMAGES"
```

Run the CRI command on the selected worker. Apply the [chart](../helm/ownsolid/README.md#install-the-signed-release)
with the prepared Chart, verified overlay, existing claim and protected site
Secrets. Retain kubelet pull events, Registry digest reads and the resulting
Pod image IDs. Check both HTTPS entrances through the private ingress before
and after replacing the disposable Control Pod. Record the existing PVC
identity and a known model artifact read from that claim:

```sh
k() { kubectl --kubeconfig "$CELL_KUBECONFIG" -n "$NAMESPACE" "$@"; }
selector="app.kubernetes.io/name=ownsolid-control,app.kubernetes.io/instance=$RELEASE"
pod_name() { k get pod -l "$selector" -o json | jq -er '.items | select(length == 1) | .[0].metadata.name'; }
claim_identity() { k get pvc "$MODEL_CLAIM" -o json | jq -cer '[.metadata.uid,.spec.volumeName] | select(all(.[]; type == "string" and length > 0))'; }
model_hash() { k exec "$1" -c control -- sha256sum "$MODEL_PROBE_FILE"; }
pod_images() {
  k get pod "$1" -o json | jq -e --arg image "$CONTROL_REF" \
    '[.spec.initContainers[].image,.spec.containers[].image] == [$image,$image]'
}
check_entrances() {
  for host in "$ADMIN_HOST" "$PORTAL_HOST"; do
    test "$(curl --fail --silent --show-error --max-time "$PROBE_SECONDS" \
      --cacert "$BROWSER_CA" --resolve "$host:443:$INGRESS_IP" \
      --output /dev/null --write-out '%{http_code}' "https://$host/sign-in")" = 200
  done
}
before=$(pod_name)
before_uid=$(k get pod "$before" -o jsonpath='{.metadata.uid}')
claim_before=$(claim_identity)
data_before=$(model_hash "$before")
pod_images "$before"
check_entrances
k delete pod "$before" --wait --timeout "$INSTALL_TIMEOUT"
k wait --for=create pod -l "$selector" --timeout "$INSTALL_TIMEOUT"
k rollout status "deployment/$RELEASE-control" --timeout "$INSTALL_TIMEOUT"
after=$(pod_name)
k wait --for=condition=Ready "pod/$after" --timeout "$INSTALL_TIMEOUT"
test "$(k get pod "$after" -o jsonpath='{.metadata.uid}')" != "$before_uid"
test "$(claim_identity)" = "$claim_before"
test "$(model_hash "$after")" = "$data_before"
pod_images "$after"
check_entrances
```

Use a known published model file under the Pod's model mount for
`MODEL_PROBE_FILE`. The commands replace only the disposable application's
Pod and verify its existing claim. The installed drill also checks sign-in
through both entrances and requires zero unexpected recorder traffic.

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
and target schema. If a restore or coordinated backup is pending, run its
recovery command with the original site configuration before planning an
upgrade. Applying the candidate loads its signed images first, stops
the application writer, and produces a recovery point verified by restoring it
into a scratch database. It then migrates and starts the candidate. Keep the
backup location and archive unchanged when retrying a pending upgrade.
Ordinary backup and restore require the signed upgrade to be resolved first.

```sh
ownsolid-install rollback-bundle --bundle /mnt/release/previous.tar.gz \
  --verification /private/release-verification.json --config /private/site.yaml
```

Before reading rollback media, a pending restore or coordinated backup returns
its recovery instruction. Run `recover-restore` or `recover-backup` with the
original site configuration, then retry. Keep the pending record intact.

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

## Install a read-only Agent from signed media

Use a trusted Linux amd64 `ownsolid-install` on a systemd host. Stage the signed
`ownsolid-agent-linux-amd64` archive on a private writable mounted volume with
room for the verified extraction. Use public trust supplied independently of
the archive. The protected release verification JSON needs `mount_root` and
`trust_file`. The node needs private HTTPS access to the Control origin.

Create a root-owned mode 0600 site JSON and supply the Control CA and one-use
join token as separate root-owned mode 0600 files. Write the token using a
protected input method that keeps it out of shell history and process arguments.

```json
{
  "format_version": 1,
  "control_origin": "https://control.site.example",
  "ca_file": "/root/agent-input/control-ca.pem",
  "join_token_file": "/root/agent-input/join-token"
}
```

The optional `disk_path` selects the filesystem used for disk facts and
defaults to `/`. Run as root:

```sh
ownsolid-install install-agent \
  --bundle /mnt/release/ownsolid-agent.tar.gz \
  --verification /root/agent-input/release-verification.json \
  --config /root/agent-input/site.json
```

The installer verifies all signed bytes, creates the dedicated system account,
and enrolls the Agent as that account. The service is enabled and started only
after the installer confirms private identity state. Site inputs stay in place.
The installer removes its own token copy after enrollment. If service startup
fails, inspect the journal and repeat with the same signed inputs. Once the
identity is saved, the original token can be absent and the site JSON can omit
`join_token_file`.

If Control definitively rejects the token, use a newly issued token. If the
installer reports `INSTALL_AGENT_ENROLLMENT_UNRESOLVED`, preserve the private
state directory and installed token copy at `/etc/ownsolid-agent/join-token`.
Check the token and node records in Control using the request ID when reported.
Remove the installed copy and issue a fresh token only after confirming that no
enrollment committed. If it did commit and no valid identity was persisted,
reconcile the node in Control before attempting another enrollment. This command
is for initial delivery and replay. A conflict with existing installed files
requires investigation before any replacement.

Inspect the signed notice paths, checksums and payload references before
installation:

```sh
ownsolid-install agent-notices --bundle /mnt/release/ownsolid-agent.tar.gz \
  --verification /root/agent-input/release-verification.json
```

The command verifies the entire Agent archive against the site's independent
trust and prints the signed inventory without changing the host.


## Qualify the scheduled timer on Linux

The [timer fixture](internal/install/testdata/scheduled-backup-timer/resume.sh)
builds Linux amd64 inputs from a clean checkout containing the accepted
Audit migration. Select an existing private evidence directory outside the
checkout and pass the exact accepted SQL commit. The default test host is
`ownsolid-cpu`; set `OWNSOLID_TIMER_HOST` for another prepared Linux host.

```sh
OWNSOLID_TIMER_EVIDENCE_DIRECTORY=/private/timer-evidence \
  bash deploy/offline/internal/install/testdata/scheduled-backup-timer/resume.sh \
  "$PWD" "$accepted_arc_commit"
```

The host needs systemd, Docker Compose, TUN and the pinned PostgreSQL image
already present. It must use the documented shared lock paths. The fixture
checks memory, disk and I/O pressure under both locks, creates its own
network-isolated PostgreSQL service and starts the shipped timer under
private runtime unit names. It verifies logical dump/restore, expiry,
retention refusal and retry. The original database and ordinary backup
sentinel must remain intact. Only the deliberate recorder probes are
allowed. No product services or other lane resources are stopped.

Input hashes and the Linux transcript stay in the selected evidence
directory. Disposable units, database container and private scratch files
are removed. This fixture does not establish Console configuration or
backup-history UI acceptance.

## Prepare a joint installed acceptance window

The [installed acceptance fixture](internal/install/testdata/installed-acceptance/host_runner.py)
owns one Linux amd64 Docker-in-Docker namespace. Its source and packet records
are test evidence; they are not a production release inventory or a server audit.
Before launch and after failed inspection, a successful all-container name query
must confirm absence. An unavailable Docker daemon leaves cleanup unconfirmed;
it cannot produce a passing window. Returned containers still require the
exact run label before stop or removal.
Prepare every selected owner's scripts, compiled clients, source mapping and
toolchain before reserving a running installation window.

Compile the three Go fixture overlays from the clean, final candidate checkout
into a new directory outside Git:

```sh
bash deploy/offline/internal/install/testdata/installed-acceptance/build_fixtures.sh \
  "$CANDIDATE_SOURCE" "$EMPTY_FIXTURE_STAGE"
```

The helper compiles Linux amd64 test binaries and changes no candidate files.
The Control and installer binaries, bundled Web assets and immutable images must
also come from the admitted candidate. Keep their build and image-source proof.
Copy `inside.sh` and `tls_relay.py` from this fixture into the final input stage,
using `tls-relay.py` as the relay's staged filename.

Create protected `inputs.json` with exactly `format_version: 1`, `candidate`
(the full Git revision), `inputs` (basename to SHA-256), `phases` and
`owner_inputs` (selected phase to report filename). The admission guard lists
the required input names. Each selected owner report contains `status: ready`,
the same `candidate`, its `phase`, and a nonempty `files` map matching the
admitted hashes. The supported order is `web`, `sql`, `cli`, `obs`; select only
ready phases and pair Web with independent SQL verification. History-only and
CLI-only windows are valid. Excluded history never blocks an independent flow.

For history, stage the complete Linux amd64 `installed-history` ELF plus its
exact `client-source.go`, `client-tests.go` and protected `client-proof.json`.
The proof must bind all seven self-tests, vet, target and admitted file hashes.
Keep Mac self-tests labelled as Mac; cross-compilation does not establish a
successful Linux request. A partial upload cannot pass admission.

The protected `runtime.json` selects a fresh absolute `output_directory`, a
canonical `run_id` UUID, an unused `lane-del-` container name, a local Unix
`docker_socket` and the two ordered `heavy_locks`. Set positive integer
`hard_timeout_seconds`, `cleanup_seconds`, `startup_seconds`, `suite_seconds`,
`operation_seconds`, `cpus`, `memory_bytes`, `docker_tmpfs_bytes`, `pids_limit` and `relay_port`.
Set `relay_port` to 8443 to match this fixture's installed HTTPS endpoint.
Reserve startup and cleanup time outside the suite budget. Set the shared-host
`minimum_free_memory_bytes`, `minimum_available_memory_bytes`,
`minimum_disk_bytes` and `maximum_io_avg10` to the agreed window gate.
`maximum_io_avg10` limits the full I/O stall percentage over ten seconds;
missing or invalid full readings refuse admission. The existing parent facts
retain the selected value, raw PSI text and observed free disk bytes. The
`images` map requires immutable local IDs for `dind`, `control`, `postgres`,
`metrics`, `logs`, and `evaluator` when history is selected.

Run admission before requesting the explicit installation slot, then invoke
the parent only in that slot:

```sh
python3 deploy/offline/internal/install/testdata/installed-acceptance/acceptance_guard.py \
  preflight --root "$FIXTURE_STAGE"
python3 deploy/offline/internal/install/testdata/installed-acceptance/host_runner.py \
  --root "$FIXTURE_STAGE"
```

The parent owns both actual locks, records their descriptors and inodes in
`parent.json`, starts one labelled namespace and publishes `control.json`.
Use the guard's `handoff` command with `--root`, `--role`, `--action` and the
current `--generation`; `status` is read-only. Web, independent SQL and CLI
finish before OBS claims the sole driver. OBS requests each ordered hook;
DEL acknowledges only that pending hook. Bind the fresh scope, CA files,
raw window and a new session filename before the claim. Stop other history
probes, UI polling and scope changes for the counter/fault segment. Failed,
stale, duplicate and wrong-role transitions are refused. This channel runs
no arbitrary shell command and does not impersonate product authentication.

Packets append directly to protected host `egress-live.jsonl`. The inner
runner stops its workloads before the recorder and publishes the actual
closed length, digest and shutdown status. Normal and deadline cleanup both
retain the host file. Missing or inconsistent closure gives
`capture-incomplete`; a complete capture with an added packet fails. The
parent returns success only after both the suite and final capture pass.

The native capture self-test uses existing recorder/probe binaries, two
short-lived Linux network/PID/mount namespaces and no product services:

```sh
python3 deploy/offline/internal/install/testdata/installed-acceptance/native_capture_selftest.py \
  --recorder "$EGRESS_RECORDER" --probe "$NEGATIVE_PROBE" --output "$NEW_EVIDENCE"
```

It verifies normal retention and forced-exit `capture-incomplete` using real
TUN records. Its two host-probe batches do not qualify Docker workloads,
application flows or a complete offline installation.

The Docker variant uses a cached immutable image, no inner daemon and no product
services. It exercises the actual parent's close method and the Docker host bind:

```sh
python3 deploy/offline/internal/install/testdata/installed-acceptance/docker_capture_selftest.py \
  --recorder "$EGRESS_RECORDER" --probe "$NEGATIVE_PROBE" --output "$NEW_EVIDENCE" \
  --image "$LOCAL_IMAGE_ID" --docker-socket "$DOCKER_SOCKET" \
  --cpus "$PROBE_CPUS" --memory-bytes "$PROBE_MEMORY_BYTES"
```

Both probe containers use `network=none`, a private TUN, explicit resource limits
and owned run labels. Both are removed after checking the retained host records.
This verifies capture and parent cleanup only; it does not qualify a product
installation or a history-client request.
