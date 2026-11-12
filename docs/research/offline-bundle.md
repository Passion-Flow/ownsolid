# Offline delivery and host preflight

## Installed acceptance window and capture lifetime

Validate the complete, immutable client and fixture inputs before starting a
bounded installation. Match the client ELF architecture, source and test hashes,
and the self-test report to the admitted binary. A cross-compiled executable and
Mac self-tests remain separate from executing the client against Linux services.
Network transfer is preparation, not part of a running installation window.

The repository TUN recorder writes each captured packet directly to its log and
closes the device on SIGTERM. Store that log in a protected host bind from its
first packet. Copying a container-local log in an EXIT trap loses it when the
parent forcibly removes the container. Docker documents that
[stop escalates from SIGTERM to SIGKILL](https://docs.docker.com/reference/cli/docker/container/stop/);
an exit trap alone cannot establish complete capture.

Stop the external relay and all inner workloads before stopping the recorder.
Record their actual shutdown results and the recorder exit status, then bind the
closed log's digest and length to the window. The host finalizer checks the
retained log against the validated probe baseline. Missing shutdown confirmation,
a truncated line, a different window or a missing final marker means
`capture-incomplete`, even when an earlier snapshot contained only five probes.
A complete capture with any added packet fails. Test normal exit, a late packet,
forced deadline termination, missing capture and damaged final metadata.

Use the existing host Python 3.12 standard library for these test-only guards:
[fsync and atomic replacement](https://docs.python.org/3.12/library/os.html),
[file locks](https://docs.python.org/3.12/library/fcntl.html), and
[bounded child processes](https://docs.python.org/3.12/library/subprocess.html).
The [PSF v2 licence](https://docs.python.org/3.12/license.html) permits commercial
use, redistribution with its notices and offline execution. Existing Go and
Docker licences remain unchanged. No additional runtime dependency, model or
dataset is introduced.

Keep one predeclared, protected metadata and handoff channel under the actual
parent lease. OBS alone claims and runs the history client; DEL can acknowledge
only a specifically pending hook. Enforce generation checks, one driver claim,
ordered hooks and fail-closed phase transitions. This is test coordination, not
server authorization or a privileged-action audit. Reject hot injection into a
live parent, invented inherited lock descriptors, duplicate initialization and
overlapping counter probes. Test competing claims, stale handoffs, wrong roles
and recovery after a hook failure without invoking any product service.

Select every participating phase and its exact owner input report before
launch. Run UI, independent SQL and CLI first, then enter the unique OBS driver
and quiet-counter segment. A window that excludes history does not require its
client or stores. Installed readiness uses the candidate's own database check
and CatalogSQL in one read-only repeatable-read transaction: record its exact
non-partition names and migration checksums, then classify the actual legal
monthly children separately. Never compare a raw physical table count with the
source's non-partition TableCount or relabel an earlier source after a migration.

The native recorder lifecycle fixture exercised real TUN traffic in two private
Linux amd64 network/PID/mount namespaces, without Docker or product services.
Each used a three-packet IPv4/IPv6/DNS batch and a second IPv4-only two-packet
batch. Normal producer exit preserved all five closed records. Killing the
namespace parent also preserved all five host-file records, but absent graceful
closure correctly yielded capture-incomplete. This is recorder lifetime evidence;
the installation, Docker workload and authenticated application flows still need
their own complete run.

The Docker lifecycle fixture additionally exercised the actual parent close
method with a real protected host bind, two sequential `network=none` probe
containers and no inner daemon or product services. Normal exit retained five
records and passed final capture. Deadline cleanup retained five records but
correctly returned capture-incomplete without a closure marker. Both owned
containers were removed. This verifies capture lifetime through Docker cleanup;
it does not establish a successful installed product or history request.

### Installed relay admission

The current [single-node end-to-end origins](../../tests/e2e/single_node_install_test.go)
and installed overlay use HTTPS port 8443. The opaque relay must target that same
listener. Accepting an unrelated positive port cannot produce a working private
TLS connection, even though no socket error is visible until an owner connects.
Validate the current fixture's fixed endpoint before any Docker command or lease.

Keep this one proven installed path. Partially parameterizing the relay alone
was rejected because it does not change the application's origins, certificates,
helpers or client dial inputs. A future transport variant must change those
inputs together. This is a test fixture boundary, not a restriction on the
product's configured entrance matrix. No dependency, model or dataset is added;
the existing Python PSF and Go BSD-3-Clause terms above remain unchanged.
Test a mismatched port against an otherwise admitted configuration and prove
that the launcher never invokes Docker.

## DEL-101 release bundle

K3s prepares architecture-specific archives on a connected system and preloads them
into the disconnected runtime. NVIDIA NIM similarly stages a complete model cache
before transfer. Helm packages a local chart and its dependencies. The product
follows ADR 0036 for model selection and connected acquisition. A selected model
must carry its pinned revision, weights, tokenizer and runtime inputs. The product
can start with an empty model store.

- [K3s air-gap installation](https://docs.k3s.io/installation/airgap)
- [NVIDIA NIM air-gap deployment](https://docs.nvidia.com/nim/large-language-models/2.0.0/deployment/air-gap-deployment.html)
- [Helm chart packaging](https://helm.sh/docs/helm/helm_package/)
- [Docker image load](https://docs.docker.com/reference/cli/docker/image/load/)
- [Ed25519 specification](https://www.rfc-editor.org/rfc/rfc8032)
- [SPDX specification](https://spdx.github.io/spdx-spec/v2.3.1/)

The existing factory signs the manifest with Ed25519 and covers all asset bytes
with SHA-256. Reuse that authority when a current production profile is available.
Keep the release public trust file outside the archive, supplied by an independent
channel. Package the image closure and the release SBOM together. Preserve the
manifest checksums and separately publish the archive digest. The SPDX specification
is CC-BY-3.0. Go implements the existing primitives under BSD-3-Clause. No new
runtime dependency is selected here. Verify redistribution rights for each pinned
image, model, plug-in, and font before admitting its bytes. A model registry entry
alone cannot establish redistribution rights.

The production profile in the shared contract requires the inherited M01–M30
inventory. The current signed path below is independent of that policy.
DEL-107's chart and a release-qualified plug-in are still needed for full
DEL-101 acceptance. A fixture release cannot qualify production payloads or
substitute for the component SBOMs and notices.

Acceptance requires a newly built release for the supported architecture. Refuse
an unknown signer, modified bytes, undeclared paths, missing required assets and
the wrong architecture before installation. In a clean Docker host with no network
uplink, start from the transferred file, run both entrances and exercise any
included model from its local assets. A working egress recorder must record zero
product attempts. Release signing and entitlement signing remain separate.

### Current Compose release profile

The existing Ed25519 envelope, tar/gzip archive, OCI descriptor closure, and
protected publish and preparation routines can also carry the current Compose
release. Add an explicit profile to the factory and installer tools under lane
H. Existing consumers keep their original profile policy. The current profile
requires the installer, Compose input, four local platform images, release
documentation and an SPDX SBOM. It carries pinned optional chart, model, plug-in
and font files as named assets. This implements the single-node delivery path
while DEL-107 and the catalog owners complete the inputs for full DEL-101
qualification.

Use Docker's architecture-filtered image save on a connected build host.
Validate the OCI descriptor closure before packaging. Docker Engine with the
containerd store can add attestation referrers to the archive index. Admit
locally closed in-toto metadata only when its export subject matches the exact
signed image. Keep those bytes outside the runtime transport. At installation use the
existing tagless writer, match the loaded image by its signed OCI manifest digest, and
keep the current Compose pull policy. A separate connected release process
supplies SPDX package metadata. Check that the signed SPDX document binds the
SHA-256 of each payload, including signed licence texts. This check binds the
SBOM to the distributed files. It cannot infer transitive dependencies that a
component SBOM failed to report.

- [Docker image save and platform filtering](https://docs.docker.com/reference/cli/docker/image/save/)
- [Docker Engine containerd inspect identity](https://github.com/moby/moby/blob/master/daemon/containerd/image_inspect.go)
- [Docker image load and platform filtering](https://docs.docker.com/reference/cli/docker/image/load/)
- [SPDX package checksums](https://spdx.github.io/spdx-spec/v2.3.1/package-information/)
- [Docker attestation storage](https://docs.docker.com/build/metadata/attestations/attestation-storage/)
- [containerd archive exporter](https://github.com/containerd/containerd/blob/main/core/images/archive/exporter.go)
- [OCI image layout](https://github.com/opencontainers/image-spec/blob/v1.1.1/image-layout.md)

No new library, image, model or dataset is required by this implementation.
The installed images, fonts, and model files remain release inputs and must
carry their applicable redistribution terms. Reject a second signature format,
archive paths outside the signed inventory, any non-amd64 image, and repackaging
a fixture as production. Test the current and legacy profile boundaries, absent
payloads, duplicate paths, checksum and signature mismatch, and an isolated
install from a signed archive. Record the exact assets and image versions used
by the isolated test.

### Model payload cold start

Use the pinned Qwen3-0.6B revision from the model catalogue and verify all nine
files before signing. The [upstream model licence](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
is Apache-2.0. Ship the unchanged licence and model card with the weights and
tokenizer. For the CPU validation, use the cached
[vLLM 0.29.0 CPU build](https://docs.vllm.ai/en/v0.29.0/getting_started/installation/cpu/).
The [vLLM source licence](https://github.com/vllm-project/vllm/blob/v0.29.0/LICENSE)
is Apache-2.0. The runtime used in this check is a test dependency on the CPU
host. Its redistributed image dependencies need their own release inventory
if that image is selected for distribution.

Run vLLM with both the model and tokenizer set to the extracted local directory
and an empty writable cache. Put the existing egress recorder into the isolated
runtime's network namespace before starting the Python process. Demonstrate
that the recorder can see IPv4, IPv6 and DNS packets before starting vLLM, and
verify that cold start and a real generation add no packets. An offline flag
or a successful HTTP response alone would miss attempted network fallbacks.

The first run generated eight tokens and failed the egress check because
startup queried the site resolver for the container's own name. The name
resolution check still applies with offline model flags. A fixed hostname
and a loopback hosts entry solved it for this single-container profile.
The repeat cold start generated eight tokens with exactly the three recorder
probes and zero runtime attempts. The fixture was a 1,770,134,760-byte signed
archive with manifest SHA-256
`2439cbf95303b405eb6b7c101c4f4526df4f372862be66b40fa67a8fa68a2f55`.
All nine extracted files matched the catalogue. The signed notices linked
the unchanged Apache-2.0 licence to all eight model payloads. This test
qualifies the local CPU launch, without exercising agent-managed GPU serving.

- [Docker container hostname and hosts entries](https://docs.docker.com/reference/cli/docker/container/run/#add-host)

## DEL-102 host preflight

K3s documents platform, disk, memory and network requirements. Docker exposes
the daemon's operating system, kernel, memory and storage root through its local
API. Linux provides available filesystem blocks and kernel clock state without
network access. NVIDIA's driver check runs locally with `nvidia-smi`.

- [K3s requirements](https://docs.k3s.io/installation/requirements)
- [Docker daemon information](https://docs.docker.com/reference/cli/docker/system/info/)
- [Docker context endpoints](https://docs.docker.com/reference/cli/docker/context/inspect/)
- [Docker bridge and port bindings](https://docs.docker.com/engine/network/drivers/bridge/)
- [Linux filesystem capacity](https://man7.org/linux/man-pages/man3/statvfs.3.html)
- [Linux clock state](https://man7.org/linux/man-pages/man2/adjtimex.2.html)
- [DNS message format and UDP transport](https://www.rfc-editor.org/rfc/rfc1035.html#section-4)
- [Static host mappings](https://man7.org/linux/man-pages/man5/hosts.5.html)
- [NVIDIA Container Toolkit sample workload](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/sample-workload.html)

Use the installer’s existing typed YAML, operation lock and resumable steps. Add
a read-only preflight command and run the same checks from apply when an operator
sets a preflight policy. The policy sets the kernel and capacity floors and a
trusted lower bound for UTC. Sites may require the kernel's synchronized-clock
state. Check the local hosts file and optionally an explicitly selected private
site DNS resolver. Never fall back to the system resolver or contact NTP servers.
Use a local Unix Docker socket so the port and storage checks observe the machine
being installed. For CPU installations there is no accelerator prerequisite. A
site that enables the NVIDIA check must have the driver and Docker runtime already
installed. The check does not install drivers or execute a GPU workload.

No new dependencies or datasets. Go and its syscall bindings are BSD-3-Clause.
The existing [YAML parser](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE)
uses Apache-2.0 and MIT license terms. Docker Engine and
[Compose](https://github.com/docker/compose/blob/v2.40.3/LICENSE) are existing
Apache-2.0 host prerequisites. NVIDIA's proprietary host driver is invoked only
when already present. This work does not redistribute the driver or container
toolkit. The existing isolated test installation uses already staged test images.

Do not use online package managers, public DNS, NTP, remote Docker, a mutable
progress database, or a separate install workflow. Treat inability to observe
the configured check as failure. Verify low resources, occupied ports, invalid
clock state, unresolved names and a missing driver. Exercise refusal before
materialization, repair, install, and repeat apply on the zero-egress harness.
To check durable resume, stop the real installer after it confirms the
administrator step and before it starts the control service. Reuse the
same command and configuration. Compare the credential file and the
committed administrator, tenant, and workspace in PostgreSQL across the
retry, then check that both entrances are ready.
The isolated Linux amd64 run at schema 52 reached that checkpoint,
confirmed the control service was stopped and PostgreSQL contained one
active platform administrator, and resumed with the identical command.
The administrator, tenant, workspace, and credential bytes matched and
both entrances became ready. The recorder observed only the host and
workload probes. This qualifies the CPU single-node flow. The optional
NVIDIA check has automated coverage and still needs real hardware evidence.
The OS or daemon can change after checks complete. Docker remains the authority
at the point it binds ports and starts containers.

## DEL-111 diagnostic support bundle

Replicated Troubleshoot separates host, cluster and application collectors, records
collection failures, and provides local archive inspection and redaction. For the
currently supported single-node installation, the local installer can observe the
same layers through the host and Docker socket even when control is stopped.

- [Replicated support bundle architecture](https://docs.replicated.com/vendor/preflight-support-bundle-about)
- [Troubleshoot host OS collector](https://troubleshoot.sh/docs/host-collect-analyze/hostos/)
- [Troubleshoot redactors](https://docs.replicated.com/reference/custom-resource-redactor)
- [Docker Compose ps](https://docs.docker.com/reference/cli/docker/compose/ps/)

Collect a fixed set of typed, bounded values. Include host platform and available
storage, local Docker availability, Compose service states, and the schema version
when PostgreSQL answers. Report unavailable collectors with fixed codes. The
application state comes from Compose, so collection does not require an API
session or running control plane. Use a private archive published only after it
has been closed successfully. Preserve an existing file. Use the Go standard
library for JSON and tar/gzip. There is no new dependency or dataset and no
Troubleshoot runtime to redistribute.

Reject copying configuration, logs, full inspect output, command diagnostics, and
database dumps. Pattern-based redaction cannot cover arbitrary application text or
encoded credentials. Keep the raw data out of the archive by construction. Verify
that secrets in command output do not appear, that a stopped control service is
represented correctly, that Docker failures still yield a readable host report,
and that an occupied destination is preserved. Exercise the collector with no
external route and a stopped control service.

## DEL-104 and DEL-105 database upgrade boundary

PostgreSQL's custom-format dump is a consistent, portable application database
snapshot. For an upgrade recovery point, stop the platform writer before taking
the snapshot. Reuse the installer's verified backup, which restores into a
scratch database and records its schema and table fingerprints. A PostgreSQL
engine major upgrade has a different format and compatibility procedure. Keep
its image fixed during application upgrades in this path.

- [PostgreSQL 18 pg_dump](https://www.postgresql.org/docs/18/app-pgdump.html)
- [PostgreSQL 18 pg_upgrade](https://www.postgresql.org/docs/18/pgupgrade.html)
- [K3s offline upgrades](https://docs.k3s.io/installation/airgap)

A read-only plan compares the live schema with the verified candidate release's
signed predecessor interval and target version. The installation lock keeps that
snapshot consistent with this installer's operations. The apply phase needs to
recheck that plan, load candidate images before stopping service, and keep a
verified backup outside the install root. Persist the pending transition before
changing application configuration. A retry must use the same signed target and
the same recovery point.

A separate application rollback may use the previous verified release only when
the live schema is accepted by that release. Never run reverse migrations as a
side effect of an image rollback. Use the explicit restore procedure for a
database recovery. There are no new dependencies or datasets. These steps reuse
the staged Docker, Compose and PostgreSQL binaries and their existing licenses.

Tests need two actual release images and cover a refused predecessor, backup
failure before the migration, successful migration, interruption and retry,
post-migration health failure and explicit rollback admission. A plan alone
establishes none of the apply or rollback outcomes.

## DEL-106 incremental archive

OSTree offline deltas identify both the source and the target revision. OCI
descriptors bind each file's byte length and content digest. Use the same
principles at whole-asset granularity. The signed candidate manifest records
one predecessor manifest digest. The patch carries the candidate's existing
signature and only the payloads that differ from that exact predecessor.
The customer supplies the previously retained signed archive. Every reused
or transferred file must match its signed size and SHA-256.

- [OSTree offline static deltas](https://ostreedev.github.io/ostree/copying-deltas/)
- [OCI content descriptors](https://github.com/opencontainers/image-spec/blob/main/descriptor.md)

The installer compares the old archive with the installed release binding and
rechecks that binding under the upgrade lock. Reconstitute and verify a normal
current release before running the existing upgrade procedure. Reject a patch
for the wrong baseline, duplicate or missing members, an untrusted target,
changed baseline bytes, and extra data after the archive. Use existing Go
archive and cryptographic code. There is no new dependency or dataset.
Retaining a baseline archive costs local storage. It permits a small transfer
without introducing binary delta tooling or a mutable shared cache.

## DEL-112 offline trust rotation

The Update Framework rotates a pinned root one version at a time. A successor
requires distinct signatures meeting both the old and the new root thresholds.
The final trusted root has an expiry. Use those rules for an explicitly
transported local file. Keep the current Ed25519 release signature envelope
and delegate its signing keys from the versioned root.

- [TUF root update rules](https://theupdateframework.github.io/specification/latest/#update-root)
- [Ed25519 specification](https://www.rfc-editor.org/rfc/rfc8032)

Keep root signing separate from release signing. Store the independently
provisioned public root in a private local file. Rotate under a local exclusive
lock and persist by atomic rename. One transition accepts only the immediate
successor. Operators with an older pinned root bring the signed successors in
sequence, with only the final unexpired root published. Rotation may renew an
expired root through this authenticated chain. Release verification always
rejects the expired active root.
The legacy public key map remains a compatibility input for sites that have
not provisioned versioned root metadata. These rules are local and use the Go
standard library without adding dependencies or a TUF client.

Verify duplicate, unknown, missing and bad signatures, thresholds on both
sides, replay or version skips, expiry, preservation on failed writes, and a
bundle verified with the post-rotation delegated key on an isolated host.

## DEL-110 notice inventory

The SPDX package information section distinguishes package identity, declared
licence terms, and copyright text. Keep the actual applicable notices and
licence files as signed payloads, with explicit references from each asset.
The existing manifest and SBOM checks bind their presence and bytes. Provide
an offline inventory view from a fully verified archive so an operator can
find the applicable notice files for each payload before installing.

- [SPDX package information](https://spdx.github.io/spdx-spec/v2.3.1/package-information/)
- [SPDX License List](https://spdx.org/licenses/)

No new dependencies. The view must have stable ordering and include the
path and checksum for each declared notice. Reject inferring licence
permissions from filenames or supplying a transitive inventory from only
image tags. A production release still needs the exact notices from its
selected dependency, model, and plug-in inputs.

### Read-only Agent notice inventory

Go embeds the linked module versions in the finished executable. Use
[Go build information](https://pkg.go.dev/debug/buildinfo) from the exact
binary with the build toolchain's license and pinned source archives. The
manifest alone does not describe packages linked into a binary. The notice
inventory command should verify the dedicated Agent profile with the existing
trust policy and use the same stable release, path, checksum and reference
output as the Compose notice view. Refuse a wrong profile, untrusted signer
or any changed notice or payload before output. The command needs neither an
Agent install nor Control access.

The Go 1.27.3 Linux amd64 fixture links 13 external modules. The exact
module archives supply the following first-level terms and notices:

| Terms | Linked modules |
| --- | --- |
| Apache-2.0 | [nftables v0.3.0](https://github.com/google/nftables/tree/v0.3.0), [Prometheus client model v0.6.3](https://github.com/prometheus/client_model/tree/v0.6.3), [Prometheus common v0.72.1](https://github.com/prometheus/common/tree/v0.72.1), [Google RPC definitions ac3dfec99bb1](https://github.com/googleapis/go-genproto/tree/ac3dfec99bb1/googleapis/rpc) and [gRPC Go v1.85.0](https://github.com/grpc/grpc-go/tree/v1.85.0) |
| MIT | [netlink fbb4dce95f42](https://github.com/mdlayher/netlink/tree/fbb4dce95f42) and [socket v0.5.0](https://github.com/mdlayher/socket/tree/v0.5.0) |
| BSD-3-Clause | [goautoneg a7dc8b61c822](https://github.com/munnerz/goautoneg/tree/a7dc8b61c822), [Go x/net v0.61.0](https://go.googlesource.com/net/+/refs/tags/v0.61.0), [Go x/sync v0.24.0](https://go.googlesource.com/sync/+/refs/tags/v0.24.0), [Go x/sys v0.49.0](https://go.googlesource.com/sys/+/refs/tags/v0.49.0), [Go x/text v0.43.0](https://go.googlesource.com/text/+/refs/tags/v0.43.0), and [protobuf v1.36.12](https://github.com/protocolbuffers/protobuf-go/tree/v1.36.12) |

These terms allow commercial and offline use and permit redistribution with
their respective license, attribution and notice conditions. Retain
applicable NOTICE and PATENTS files and review package-specific terms
against the final linked package set. The site supplies systemd, which the
Agent archive does not redistribute. There are no new dependencies, models
or datasets in this change. This is a fixture inventory. The production
signer, first-party distribution terms and approved inventory for the exact
production artifacts remain prerequisites to closing DEL-110.

The Linux amd64 notice drill used the same private mount and network
namespaces as the [Agent delivery check](agent-offline-enrollment.md#disconnected-linux-amd64-signed-media-check).
The temporary-signed archive SHA-256 was
`ee49466a2f1296532b9a70349a1d0760c0e03e4a63a95c2eb34ec83be68c1e33`.
The native `agent-notices` command returned 23 sorted notice paths, hashes
and signed asset references. An independent read of the mounted notice
files and manifest matched the output. An unrelated trust root and changed
signed payload produced errors and no inventory on stdout. The recorder
saw only the three IPv4, IPv6 and DNS probes.

### Prepared directory membership on Linux

The Linux factory suite can add an entry while the directory still has the
same inode, size and timestamps. The
[Linux inode interface](https://man7.org/linux/man-pages/man7/inode.7.html)
reports metadata, while
[directory enumeration](https://man7.org/linux/man-pages/man2/getdents.2.html)
reports the names that are present. Keep the existing inode, ownership and
payload checks. Re-enumerate each directory from its pinned
[Go root](https://pkg.go.dev/os#Root.OpenFile) and compare the exact names and
entry types. At the end, compare membership with the complete signed
inventory already observed. Continue streaming in bounded batches.

Waiting for a timestamp to advance in the test would hide a real Linux
case. Replacing the pinned root with a fresh pathname walk would introduce
a separate path substitution risk. No dependency or license changes are
needed. Run the existing added-entry, changed-directory, unchanged inventory,
cancellation, and special-file tests repeatedly on Linux as a non-root user.


### Shared-host full I/O pressure admission

The [Linux PSI interface](https://docs.kernel.org/accounting/psi.html)
reports separate some and full pressure rows, each with avg10 in percent.
The agreed host gate uses full avg10. The former installed-window runner
compared every row to one threshold, conflating partial stalls with simultaneous
stalls of all non-idle tasks. It also accepted an empty pressure document because
all over an empty sequence is true.

Select exactly one full row and one finite avg10 percentage between 0 and 100.
Apply the explicit runtime threshold to that observation, and retain the raw
PSI document and selected value in the existing parent facts. Refuse missing,
duplicate, malformed or out-of-range full observations. Do not change memory,
disk, lock or other host gates, and do not bake a machine's thresholds into the
fixture. A more restrictive operator threshold remains an explicit runtime
input. No dependency, model, dataset or copied kernel code is added; the
existing Python standard library retains the [PSF license](https://docs.python.org/3/license.html) and permits commercial,
offline use and redistribution with its notices.

Verify resource admission with upstream-format PSI examples where some is above
the threshold and full is below it, full is above it, or full is unavailable.
Also cover duplicate rows/avg10 values, malformed numbers, NaN, infinity and
out-of-range percentages. These deterministic parser tests start no workload
and do not prove a real host passed a resource gate.

The 33 lightweight fixture tests passed on Linux amd64, including all new PSI
admission cases. The pre-fix regression reproduced both the partial-stall false
refusal and missing/invalid full-observation admission. The tests substitute
resource documents and filesystem counts. They did not take heavy-job leases,
start Docker or services, or qualify a real host resource snapshot.

### Confirming owned namespace cleanup

The installed-window runner previously interpreted every failed container
inspection as absence. A disconnected Docker socket or an inspection error can
therefore be mistaken for successful removal after the suite and capture have
completed. The [Docker container listing contract](https://docs.docker.com/reference/cli/docker/container/ls/)
provides a separate all-container query and a name filter. Include stopped
containers. Require a successful query with no matching name before declaring
absence; a failed query or a remaining match leaves cleanup unconfirmed. Keep
the existing exact run label before stopping or removing a returned container.
The name filter can match substrings, so an unexpected neighboring name causes
a conservative refusal. Do not infer absence from diagnostic text, a client
exit code or an unavailable daemon, and do not remove a matching foreign name.

Reuse the same bounded Docker invocation and existing private result file.
No workload, dependency, model or dataset is added. The Python standard library
uses the [PSF license](https://docs.python.org/3/license.html); the existing
[Docker CLI](https://github.com/docker/cli/blob/v29.1.3/LICENSE) uses Apache-2.0.
Both permit commercial use, offline operation and redistribution with required
notices. This does not change the image inventory or its licence boundary.

First reproduce a suite exit of zero and closed passing capture with inspect
and list unavailable, and with inspect failing while a matching name remains.
Neither may produce a passing window or a stop/remove command. Cover confirmed
absence, initial occupied or unavailable name queries, and missing/foreign
ownership labels. Deterministic command-response tests start no Docker workload;
the installed Linux window retains its independent native qualification.

The pre-fix Linux test reproduced a passing result despite unavailable inspection
and a remaining name, plus a missing-label exception that prevented writing the
result. After the correction, all 36 lightweight fixture tests passed on Linux
amd64. These tests substitute Docker responses and closed capture results; no
container, namespace, service or port was started by this regression suite.
