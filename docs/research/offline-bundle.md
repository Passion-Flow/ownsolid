# Offline delivery and host preflight

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
