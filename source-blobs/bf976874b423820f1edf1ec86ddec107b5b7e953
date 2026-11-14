# Select a signed image from a catalog OCI archive (CAT-107)

The existing platform image reader admits one runnable manifest per asset.
Its wrappers and supported attestations can describe that image, but a second
runnable manifest is refused. The catalog importer has a separate signed
inventory and verifies a flat OCI index against every manifest digest in the
app declaration. That format needs an explicit image selection boundary.

OCI indexes describe several manifests by digest, media type and size. The
standard permits nested indexes, spare blobs and external fulfillment of
missing layout blobs. For this offline catalog path the existing app contract
admits a flat, exact, closed set of Linux amd64 runtime manifests. Image config
binds the ordered uncompressed layer DiffIDs. A compressed blob matching its
descriptor can still have an invalid DiffID or exceed the installation budget.
[OCI image index](https://github.com/opencontainers/image-spec/blob/v1.1.1/image-index.md),
[OCI layout](https://github.com/opencontainers/image-spec/blob/v1.1.1/image-layout.md),
[OCI config](https://github.com/opencontainers/image-spec/blob/v1.1.1/config.md).

Add a factory reader for one image in an already authorized app inventory.
It takes the independently verified archive SHA-256 and size, the exact
selected manifest and config digests, and every approved manifest/config
pair from the app verifier. Reuse the factory's protected file reads, blob
hashing, platform and manifest/config validation, and bounded selected-layer
expansion. Each flat index descriptor must match one approved pair and its
entire local blob closure. The emitted index describes only the selected
image, with original manifest, config and layer bytes. Do not change the
existing platform release reader or resolve a tag or platform preference
against a set of valid signed digests. A list of digests from the source
archive alone is not caller authority.

The consumer opens every required selection before submitting any image to
Docker. Its operation-wide expansion budget can charge each reported expanded
size. Each selection checks the entire archive and expands its selected
image. This deliberately reuses the existing single-descriptor streaming
contract and keeps the archive immutable throughout a bounded install
operation. Publisher trust, installer authorization, audit and workload
admission stay with their existing owners.

Cover exact selection in both directions, missing and extra descriptors and
blobs, duplicate declarations, mismatched or corrupted manifest/config and
layer bytes, unapproved or remote content, and a broken or oversized expanded
layer. Run the existing single-image and attestation tests as compatibility
controls. On Linux amd64 repeat with real Distribution and VictoriaLogs
payloads and a disconnected network namespace.

This change introduces no dependency, model or dataset. The OCI
specification is [Apache-2.0](https://github.com/opencontainers/image-spec/blob/v1.1.1/LICENSE)
and the existing Go standard library is [BSD-3-Clause](https://go.dev/LICENSE).
The real fixture reuses [Distribution 3.1.2](signed-registry-bootstrap.md) and
[VictoriaLogs 1.53.0](https://github.com/VictoriaMetrics/VictoriaLogs/blob/v1.53.0/LICENSE).
The image notice and source obligations recorded for the existing fixtures
apply to any redistribution. The test does not add these images to the
release or the repository. These licences permit commercial and offline use
with their applicable conditions.

## Linux amd64 acceptance and OPS handoff

On `ownsolid-cpu`, the existing catalog verifier admitted a temporary-signed
two-image bundle. Its OCI archive SHA-256 was
`b1f1d229e8272d7b6d05db055bdbde04ac6173ab067c522df58896b41987065e`.
The single-image reader refused the combined archive. The new reader used
the exact manifest/config pairs returned by the app verifier. It checked
65,176,576 expanded bytes for Distribution and 28,251,648 for VictoriaLogs.
Their seven and sixteen runtime blobs matched the original digests and sizes.
Both emitted tagless archives passed the existing single-image reader and
retained their selected manifest and config digests.

The [focused factory tests](../../tools/release/offline/internal/factory/image_selection_test.go)
passed on the same disconnected Linux host, including the single-image
and attestation controls. The IPv4 and IPv6 default routes led only to the
egress recorder. It recorded the three positive probes and no test traffic.
The fixtures used tmpfs and no Docker daemon or published ports.

OPS can pass every `VerifyInstallBundle` archive image as a
`bundle.ImageIdentity` to
[`OpenCurrentImageSelection`](../../tools/release/offline/pkg/bundle/current.go).
Bind each selected `wire.Asset` to the original archive SHA-256 and size.
Retain the opened sources until all selections pass, then use their tagless
transports for the existing local digest import and readback. Authorization,
audit, workload admission and multi-image Docker registration remain in
the installer and catalog consumer flow.
