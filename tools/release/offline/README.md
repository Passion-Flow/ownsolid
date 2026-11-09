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

The prepared verification and image proof APIs select the current or legacy
inventory policy from the exact signed manifest. A current single-node release
uses the same trust and manifest rules as `prepare-bundle`, including the
signed site Registry group. The image proof format remains version 1.

For a Registry consumer, call `bundle.PreparedImageProof` with the private
prepared root, independent archive and manifest digests, and the exact signed
asset ID. Use the verified `ImageProofResult.Profile` to select
`bundle.OpenCurrentImageSource` for `bundle.CurrentProfile` and
`bundle.OpenImageSource` for legacy assets. Cache readback can call
`bundle.VerifyImageProof` under the site's public trust and compare the
returned manifest digest and asset to its recorded identity. Catalog listing
and recovery can use `bundle.VerifyPrepared` for the full prepared tree.

For a verified catalog app archive, call `bundle.OpenCurrentImageSelection`
with its independently verified byte count and archive SHA-256 in the
`wire.Asset`, the selected manifest and config in `ImageID` and
`ImageConfigDigest`, and every approved pair as `[]bundle.ImageIdentity`.
Populate that list from the authenticated app bundle verification result.
The reader checks the exact flat OCI inventory and complete local closure.
It verifies the selected image's expanded layer sizes and DiffIDs.
`WriteTagless` emits the original runtime blobs and one selected manifest
descriptor. The platform release reader keeps its single-image contract.

Open each required selection before submitting any image to Docker. Keep
the archive immutable, carry one installation timeout across the selections,
and charge each reported `Inventory().ExpandedBytes` against the installer
operation's expansion budget. The importer owns authorization, audit and
workload admission. See the [CAT-107 handoff](../../../docs/research/catalog-image-selection.md).

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
For the Linux installer, include the compiled age, hpke, x/crypto and x/sys
BSD notices. Read the selected versions from `go version -m` on the
packaged binary. See the [dependency inventory](../../../docs/research/encrypted-backups.md#licensing).


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

### Optional site Registry inventory

Add `ownsolid-site-registry:distribution-v3.1.2` to `prerequisites` and a
`registry-image` asset of kind `image_archive` and architecture `amd64`.
The factory requires these Docker Official Image identities:

| Field | Digest |
| --- | --- |
| `image_id` | `sha256:52b489ad8e6f40d1a676702e3ce398ec796ce38f08fbb786e1f324baa12d84b8` |
| `image_config_digest` | `sha256:39a2a88344598be51c03d6829b40dd29fcda04ce1e01ef1f7596b5af744276ce` |

On the manufacturing host, export that exact platform image. Sign the archive
SHA-256, include its component and notice evidence and add the archive checksum
to the current release SPDX. The site installer verifies the complete bundle,
starts the Registry from the packaged image and preloads the selected
Linux amd64 manifests and blobs of the other signed image archives.

For an isolated test fixture, set `OWNSOLID_CURRENT_FACTORY_REGISTRY=1` in
`TestManufactureCurrentRelease` and stage `assets/registry-image` and
`assets/registry-notice`. See the
[Registry installation and acceptance instructions](../../../deploy/offline/README.md#bootstrap-a-site-registry).

### Optional RKE2 inventory

To carry the approved Linux amd64 RKE2 release in a current bundle, add
`rke2-linux-amd64=v1.36.6+rke2r1` to the signed prerequisites and include
the RKE2 assets below. Use the exact upstream bytes and digests in the
[approved inventory](../../../docs/research/rke2-signed-delivery.md#pinned-upstream-bytes).

| Asset ID | Path | Kind | Architecture |
| --- | --- | --- | --- |
| `rke2.linux-amd64.tar.gz` | `rke2/rke2.linux-amd64.tar.gz` | `rke2_binary` | `amd64` |
| `rke2-images-core.linux-amd64.tar.zst` | `rke2/rke2-images-core.linux-amd64.tar.zst` | `rke2_image_set` | `amd64` |
| `rke2-images-canal.linux-amd64.tar.zst` | `rke2/rke2-images-canal.linux-amd64.tar.zst` | `rke2_image_set` | `amd64` |
| `rke2-sha256sum-amd64` | `rke2/sha256sum-amd64.txt` | `dependency_lock` | Omit |
| `rke2-component-spdx` | `rke2/components.spdx.json` | `sbom` | Omit |
| `rke2-component-notices` | `rke2/component-notices.tar` | `license` | Omit |

Each asset needs the applicable signed licence references and an SPDX
SHA-256 checksum. Keep the upstream archives byte-for-byte and omit Docker
image identity fields for them. The RKE2 image sets contain multiple images
for the RKE2 runtime to import. Every RKE2 payload other than the notice
archive references the signed notice archive in its `licenses` list.

The main SPDX document includes the checksum of both companions and an
`externalDocumentRefs` entry for `DocumentRef-RKE2`. Set `spdxDocument` to
`urn:ownsolid:spdx:rke2:v1.36.6-rke2r1:linux-amd64:1` and its `checksum`
to the component SPDX's SHA-256. Add a `DESCRIBES` relationship from
`SPDXRef-DOCUMENT` to `DocumentRef-RKE2:SPDXRef-RKE2`. Use the
[reviewed evidence pins](../../../docs/research/rke2-component-inventory.md#reviewed-companion-assets).

New RKE2 manufacturing requires the companions. Verifiers can require them
with `require_rke2_components: true`. The compatible `require_rke2: true`
policy still admits earlier signed RKE2 releases. If either component asset
is declared, both and their main SPDX link must verify. The separate KWOK
scale fixture is outside this inventory.

For an isolated test with real staged upstream bytes, set
`OWNSOLID_CURRENT_FACTORY_RKE2=1` in `TestManufactureCurrentRelease`.
Put the four upstream files and both reviewed companion assets under its
`assets/rke2/`. Put the binary tarball's `share/rke2/LICENSE.txt` at
`assets/licenses/rke2.txt` before manufacturing.
The fixture uses an ephemeral key and a fixture SPDX document.
See the [site preparation and handoff](../../../deploy/offline/README.md#prepare-rke2-artifacts-for-agent-delivery).

### Incremental release transfer

Include exactly one `baseline-manifest-sha256=<digest>` prerequisite in the
signed target manifest. Use the manifest digest from the exact previously
shipped archive. Then build the transfer with both complete signed archives
available on the factory mount:

```sh
factory build-current-patch --config /private/patch-factory.json \
  --baseline /mnt/release/previous.tar.gz --target /mnt/release/next.tar.gz
```

The protected config selects the mounted workspace, public trust and an
unused `output_file`. The command reuses the target signature. It writes only
changed or new payloads. Its reported archive digest refers to the transfer
file and its asset count counts transferred payloads.

## Versioned release trust

The current commands accept a private public-trust JSON file with
`format_version: 2`, `version`, `expires` as an RFC 3339 timestamp,
`root_threshold`, `root_keys` and `release_keys`. Each key map is keyed by
the SHA-256 of the raw Ed25519 public key and stores that key in standard
base64. Root keys and release signing keys are distinct. Provision the
initial trust file through the independent release-trust channel.

On each offline root signing host, sign the same successor file:

```sh
factory root-signature --candidate /private/root-2.json --key-file /private/root-signer.key
```

Collect the detached signature objects into one JSON array per successor.
Bring the protected files into the isolated installation and apply the
rotation to the existing pinned file:

```sh
factory rotate-root --trust-file /private/root.json \
  --candidate /private/root-2.json --signatures /private/root-2-signatures.json
```

For more than one step, repeat the `--candidate` and `--signatures` options
in version order. Every transition needs both root thresholds. The tool
publishes only the final, unexpired root. Bundle verification refuses an
expired active root and a signer that is absent from its delegated keys.
The host clock must be correct. Legacy public key maps remain available for
existing unversioned trust provisioning.

## Manufacture signed read-only Agent media

Use the same protected factory configuration and independently provisioned public
trust as for other releases. Keep the private signer off the mounted volume.

```sh
offline-factory validate-agent --config /private/agent-factory.json
offline-factory build-agent --config /private/agent-factory.json
offline-factory inspect-agent --config /private/agent-factory.json
```

Set the manifest profile to `ownsolid-agent-linux-amd64`. Use format, schema
and both predecessor versions of 1, an exact release source SHA-256, empty
capabilities, and the Linux amd64 systemd prerequisite. Pin these asset IDs:

| Asset ID | Kind | Source |
| --- | --- | --- |
| `agent-binary` | `operator` | `services/agent/cmd/agent`, built for Linux amd64 with `CGO_ENABLED=0` |
| `agent-unit` | `operator` | `deploy/systemd/ownsolid-agent.service` |
| `release-sbom` | `sbom` | SPDX 2.3 document with SHA-256 checksums for all other payloads |

The executable declares architecture `amd64`. Each of these assets references
the signed license notices that apply to it. Supply the approved first-party
and third-party notices for the exact build as `license` assets. The
[host installation procedure](../../../deploy/offline/README.md#install-a-read-only-agent-from-signed-media)
requires only the verified Agent inventory and protected site inputs.

## Tokenizer wheelhouse fixture

Run this on Linux amd64 from a protected checkout with CPython 3.12 and a
network namespace that has no external route. Supply a local wheelhouse
containing one allowed wheel per projected pin and the manifest-pinned pip
wheel. The model path may be the retained approved Qwen snapshot. The checker
copies the five tokenizer files and the catalogue-pinned `LICENSE`.

```sh
python3 -IB tools/release/offline/tokenizer_wheelhouse.py \
  --wheelhouse "$wheelhouse" --model "$qwen_snapshot" --output "$new_fixture"
PYTHONPATH=runtimes/training:runtimes/data-processing \
  OWNSOLID_TEST_QWEN_TOKENIZER_DIRECTORY="$new_fixture/model" \
  "$new_fixture/venv/bin/python" -B -m unittest discover -v -s runtimes/training/tests
```

The output path must be new. A completed `manifest.json` identifies the
private staged copies, projected lock, source contracts, wheel metadata
and notice bytes. It follows hash-required installation and `pip check`.
Retain the protected runtime manifest, full training lock and catalogue at
their existing repository paths for the smoke. The venv stays at its
original path. Run the focused checker tests with
`python3 -B -m unittest discover -s tools/release/offline -p tokenizer_wheelhouse_test.py`.

An incomplete wheelhouse reports missing wheels and embedded notices and
leaves no fixture. The output profile is for the disconnected build check.
Release provenance, binary redistribution approval, image publication and
the optional service configuration use separate gates. See the
[Linux replay and open inputs](../../../docs/research/training-runtime-delivery.md#disconnected-release-side-replay).
