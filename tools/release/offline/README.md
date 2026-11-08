# Explicit public verification environment — 2026-11-02

`bundle.LoadVerification` restricts explicit env files to public Verification
fields, then delegates values to factory Config. Signing/output/build-only keys
refuse. Kubernetes selects the private file through optional
verification_environment_file and existing immutable input fingerprints. Follow
[the current contract](../../../docs/milestones/m18-kubernetes/verification-environment.md)
and [procedure](../../../docs/runbooks/kubernetes/verification-environment.md).

# Mounted factory capacity amendment — 2026-11-02

The current [capacity contract](../../../docs/milestones/m18-kubernetes/factory-capacity.md) adds explicit minimum_free_bytes
through the existing protected config/explicit env-file boundary:0 preserves the
legacy30percent floor, positive bytes selects allocation+fixed reserve. For the
verified Project-one external volume use32GiB reserve with outer64GiB admission;
internal host/backing/RAM/CPU keep30percent. All mount/ownership/hash/durability
checks remain. The older factory contract/evidence below keeps its dated scope;
no historical refusal becomes a success. See the current contract for new tests.

# Offline release factory and shared bundle verification

This Go module owns the existing `cmd/factory` executable and `internal/factory`
manufacturing, inspection, import and prepared-inventory mechanics. The public
`pkg/bundle` facade is also used by control/agent/deployment consumers. Its source
is a real runtime dependency despite residing under tools; retain it in source
and release manifests. It does not become a second business control plane.

The CLI accepts `validate|build|inspect --config <protected-JSON>` and optional
`--env-file <protected-component-policy>`. Configuration fields, explicit policy
mapping, artifact pins, signatures, licenses, mount identity and resource limits
remain owned by existing `internal/factory/config.go`, release contracts and
`config/examples/release/`. Ambient environment is not policy authority.
Missing or unverified required platform assets fail manufacturing. A fixture
profile is explicitly synthetic and cannot be installed as a complete platform.

`archive_header.go` retains the historical USTAR encoding through its exact
8GiB-minus-one-byte file-size boundary. Larger manifest-admitted assets use
standard PAX size metadata, consumed by the existing archive readers. The typed
asset/total limits, signatures, file namespace, hashing and durability remain
unchanged. Header-only encoder/decoder controls cover the boundary, actual M06
size and maximum admitted asset size without allocating giant synthetic files;
real large-image manufacture and preparation remain separate evidence.
Inspection and unpacking apply `safeAssetHeader` to the exact signed path/size:
only a necessary canonical PAX size record is added to ordinary regular-file
admission. Other PAX fields, path/link/owner/time/sparse/xattr extensions and
unnecessary small-file PAX size records refuse. Manifest/signature metadata
retains the original strict `safeHeader` boundary.

`image-proof --config <protected-JSON> --prepared <private-root>
--archive-sha256 <independent-original-digest> --manifest-sha256 <independent-digest>
--asset-id <signed-image-id>` exports the existing ImageProof JSON on stdout.
It first verifies the entire prepared inventory, original completion journal and
signature through `PreparedImageProof`, then selects the exact image. It needs
public trust, not a private signing key. Missing or tampered unrelated assets
also refuse without emitting a proof or changing the prepared authority. Store
stdout in a protected file and check the exit status before using it. This subset
does not establish original archive availability, running image identity, model
readiness, GPU execution or whole-release qualification. Existing consumers still
verify the proof under their independently selected public trust.

`files.go` owns canonical file admission, the pinned nonblocking descriptor-open
boundary and bounded immutable reads. Existing public/private file permission
semantics and specifically admitted archive hardlinks are preserved; they differ
from the control probe's strict0600 snapshot protocol. Read/open guards include
native change time, so restoring mtime cannot conceal a same-inode equal-length
rewrite when the filesystem reports a changed native time. The actual rewrite
regression first observes that premise within a bounded fixture clock budget;
some Linux filesystems reuse timestamps within one clock tick. A separate
manifest-hash refusal covers equal-length/restored-mtime bytes independently of
that metadata observation. Callers must exclude active writers; snapshots are not
filesystem transactions. Kernel filesystem calls on an unavailable device still require outer
supervision. Follow the [frozen reader repair](../../../docs/engineering/deployment-transport/release-inputs.md)
and its actual execution record before claiming verification.

`prepared.go` and `prepared_snapshot.go` also pin the private prepared root and
each declared directory. Payload reads reuse the nonblocking pinned regular-file
boundary; a final identity/permission/link/ctime pass revisits every observed
payload and directory after authority readback. The preparation journal retains
its own before/after snapshot. A changed tree refuses without deleting or adopting
it. Callers must still exclude active writers and validate later installation
inputs; this readback is not a filesystem transaction. See the
[prepared inventory contract](../../../docs/milestones/m17-offline-compose/prepared-inventory-snapshots.md)
and [current lane handoff](../../../docs/execution/mvp/parallel-20261024-release.md)
for the focused regression commands and their actual execution status.

Manufacturing never overwrites occupied output or treats an interrupted journal
as success. Signature/hash/source identity, durable publication and explicit
recovery remain with their existing owners. Prepared bytes are not installed
services. Preserve unknown outputs and sole diagnostics; remove only exact
inventoried disposable fixtures after consumers exit. Do not copy live databases,
prune shared storage or invoke real provider operations through these tools.

`stream_io.go` checkpoints descriptor-local output writeback at a fixed4MiB
algorithm quantum and advises consumed page-aligned ranges during source/hash,
archive readback/unpack and image import. Darwin uses F_NOCACHE; Linux amd64/arm64
uses fsync before DONTNEED for output. The existing file identity/ctime/custody,
complete hashing, cancellation, final fsync/readback and exclusive publication
remain mandatory. Kernel advice is advisory; retain external resource guards.
See the [streaming contract](../../../docs/milestones/m17-offline-compose/factory-stream-io.md)
for exact interfaces, measured small diagnostics and current qualification limits.

Use the admitted [release-input gate](../../quality/deployment-transport/README.md)
for changed input controls and four affected tiny archive/preparation tests.
The tests use an actual verified external TMPDIR and explicitly synthetic keys,
images and payloads. Native race/vet/build, a synthetic archive and template
rendering do not establish a complete offline package or installed Compose/Helm.


## Current cumulative CPU selection

The explicit `m17-postgresql-cpu-linux-amd64` signed inventory requires the closed cumulative selection, account22, all M01–M17 owners, factual installed-locale counts and the named current runtime/input/dependency/source/license assets. Historical profiles cannot carry that selection. The profile assumes explicitly installed Engine/OS prerequisites and performs no host installation. See the cumulative-inventory contract; actual complete package/readback remains separate.

`frontend_metadata.py` is an import-safe, standard-library-only supplementary
dependency packer/stager. It binds pnpm12 registry metadata to a versioned lock,
excludes auth/global/success-cache state, and preserves bounded private file and
archive custody. It neither fetches packages nor changes pnpm policy. Its explicit
CLI paths/budgets, selected component source recipes, actual Go/pnpm write-path
mapping and five boundary controls are documented in
[offline source inputs](../../../docs/milestones/m17-offline-compose/offline-source-inputs.md).
The Go factory remains the signed release authority; a metadata snapshot or a
successful extraction is not a passed source rebuild or installed release.

The [current local source profile](../../../docs/execution/mvp/m17-offline-source-rebuild-20261028.md)
uses104 full official packuments and proves a fresh unchanged-policy offline
frontend plus18 source-bound binaries. Online metadata preparation alone can take
upstream shortcuts; only the fresh offline check binds its actual input closure.
Final signed asset binding and denied-egress cold operation remain separate.

The [final current43-asset local inventory](../../../docs/execution/mvp/m17-final-signed-toolkit-20261029.md) now passes actual
factory build, separate inspect and independent Ed25519/all-byte/namespace
readback with20 executable component variants and exact per-component source.
The original pre-journal swap interruption remains failed; a distinct validated
output succeeds with unchanged budgets. Cold runtime and final external return/
cleanup remain separate. The standard deployment-license issuer stays distinct
from the additive restore-compatibility certificate issuer.

## Current single-node release

The `validate-current`, `build-current` and `inspect-current` commands select
`ownsolid-single-node-linux-amd64`. They use the same protected JSON options,
archive format, Ed25519 signing envelope, and publication checks as the existing
factory. Current commands accept only the current profile. Existing commands
keep their original profile admission.

Stage exact payloads and their SHA-256 digests in the private `artifact_root`.
Export one Linux amd64 image per archive with
`docker image save --platform linux/amd64`. Record the OCI platform manifest and
config digests as `image_id` and `image_config_digest` respectively. The factory
verifies the local descriptor closure and accepts uncompressed or gzip layers
for the current installer. It rejects external OCI layer URLs.

The signed inventory requires these IDs and kinds:

| IDs | Kind |
| --- | --- |
| `control-image`, `postgres-image`, `metrics-image`, `logs-image` | `image_archive` |
| `compose` | `compose` |
| `installer` | `operator` |
| `operator-docs` | `runbook` |
| `release-sbom` | `sbom` |

The manifest uses format version 1, an empty `capabilities` array, the control
image's schema version, and the actual source digest. Extra `chart`, `model`,
`plugin`, `font`, `dependency_lock`, and `license` assets can be included.
Every payload other than a licence text references its applicable signed
licence texts. Every payload other than the SPDX document itself must have its
SHA-256 represented by a package checksum in that SPDX 2.3 JSON document.
Generate component inventories and notices on the connected release machine
before signing. Outer asset checksums do not replace component SBOMs.

```sh
factory validate-current --config /path/to/release.json
factory build-current --config /path/to/release.json
factory inspect-current --config /path/to/verify.json
```

The signing key stays on the manufacturing host outside the release mount.
Send the archive as the delivery file and establish the release public trust
through an independent channel. The product license issuer has a separate
trust file. `TestManufactureCurrentRelease` uses ephemeral test trust and a
fixture SBOM with real Docker exports for the isolated installer test. Those
inputs do not qualify a production release.
