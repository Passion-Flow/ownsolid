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
