# Signed offline application bundles (CAT-107)

App delivery needs to authenticate the manifest, translations, deployer input
and the exact image archive before any artifact reaches a workload engine.
Air-gapped sites carry the public trust and all package bytes into the site.

The repository's [release factory](../../tools/release/offline/README.md)
verifies a signed inventory against local payloads. Its manifest, profile and
signature purpose describe complete platform releases. Keep those contracts
unchanged and use the same detached-signature and content-digest design for an
application's smaller, separately scoped inventory.
[Helm provenance](https://helm.sh/docs/topics/provenance/) signs the chart
package but does not cover a Compose project, application messages or images.
[Sigstore](https://docs.sigstore.dev/cosign/verifying/verify/#local-verifications)
supports verification against locally supplied trust. The first app verifier
uses a preconfigured Ed25519 publisher key and an application-specific signing
purpose. It needs neither a transparency service nor credentials for a
registry. Signing belongs outside the deployed verifier.

For an immutable staging directory, verify the detached signature over the
exact bytes of a strict, bounded bundle index. The index names the digests of
the manifest, both locale files and a single local OCI image archive. The
signed manifest supplies the chart or Compose digest and pins the expected
image manifest identities. Read the metadata from the checked bytes and stream
payloads within the caller's byte budget. Consume only the fixed paths covered
by the index and signed manifest. Skip online resolution and never use a
publisher key from the bundle as trust.

The [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md)
separates the manifest identities from the blob bytes. The deployer still
verifies the referenced OCI closure, platform and image digest after local
import. A signed archive's hash alone cannot show whether it contains the
listed images or whether a workload will attempt egress. OCI import and
rendered-workload admission are separate gates, and the bundle must remain
immutable through use.

Tests must refuse a wrong public key, malformed or tampered index, missing or
modified manifest, locale, engine input and image archive, truncated or
oversized payload and symlink substitution. Verify both engine declarations
with a local public key and no network. Product acceptance also needs an
approved publisher's real archive imported and installed from the console on
a disconnected Linux amd64 target.

## Local Compose prototype

Docker Compose 5.5.1 on Linux arm64 accepted the Compose file after the catalog
verifier checked its signature, the locale documents and a real OCI-format
archive from the local image store. With the pinned image already present,
`compose up --pull never --no-build` ran the fixture under `network_mode: none`
and the workload confirmed that it had no Ethernet interface. The installation
gate still needs an import from the staged archive into an empty local store.

The platform-filtered archive's root index referred to the arm64 image manifest
digest, which differed from the registry's multi-platform index digest for the
same cached image. The packaging and import paths must carry and verify the
specific image identities retained in the archive and render against those
verified identities. Copying a registry index digest into a platform-filtered
package without checking the archive is insufficient.

## Licenses

[Ed25519](https://www.rfc-editor.org/rfc/rfc8032.html) and SHA-256 come from
the [BSD-3-Clause Go standard library](https://go.dev/LICENSE), usable
commercially and offline and redistributable with its notice. The
[OCI image specification](https://github.com/opencontainers/image-spec/blob/main/LICENSE)
and [Helm](https://github.com/helm/helm/blob/main/LICENSE) are Apache-2.0
references. The verifier adds no third-party code or application images. A
release publisher must verify the redistribution rights and retain the
required notices for the chart, application and every image layer.

## OCI preflight for installation

[OCI Image Layout 1.1.1](https://github.com/opencontainers/image-spec/blob/v1.1.1/image-layout.md)
allows references to blobs supplied by an external store. Ownsolid's
installation preflight requires the entire closure in the signed tar.
Follow the [descriptor](https://github.com/opencontainers/image-spec/blob/v1.1.1/descriptor.md),
[manifest](https://github.com/opencontainers/image-spec/blob/v1.1.1/manifest.md)
and [configuration](https://github.com/opencontainers/image-spec/blob/v1.1.1/config.md)
specifications for digests, byte counts and platform identity.

The first installable archive form contains a root index of direct Linux
amd64 image manifests, their configurations and their layers. Its root digests
must exactly match the signed catalog manifest. Reject missing, duplicate,
unreferenced and remote blobs, foreign platforms, secondary Docker wrapper
metadata and unsafe tar entries. Verify the signed archive bytes and each
blob digest with a bounded scan. Read OCI metadata through the same open
file, using bounded sections, so memory use does not depend on layer size.
Existing signature-only callers can still inspect packages with other image
layouts. The importer owns expanded layer limits, DiffID verification,
local image registration and post-import digest checks before execution.

The platform factory's `OpenCurrentImageSource` validates and rewrites a
single platform release asset through its internal release profile and
mount policy. Calling it with fabricated platform metadata would conflate
app trust with platform release trust. This catalog preflight uses the Go
standard library and the Apache-2.0 OCI specification already cited above.
No new code or image dependency is shipped. Test an authenticated offline
archive and its failure cases, including valid signatures over an incomplete
closure and a signed catalog digest absent from the root index.

The preflight also verified a signed archive with the 16 runtime blobs of the
cached VictoriaLogs 1.53.0 Linux amd64 image. The exported Docker archive held
a second, attestation manifest. The fixture selected the runtime image and
repacked its complete closure as USTAR. The verifier ran in a Linux amd64
container with `--network none` and `--pull never`. This exercised the exact
archive accepted by the preflight. The deployment owner still needs the
import and registered-digest checks for this archive.


The same verified single-image archive also passed the release factory's
`OpenCurrentImageSource` and `WriteTagless` on a private tmpfs inside the
disconnected Linux amd64 container. The factory verified 14 layer DiffIDs
and measured 28,251,648 expanded bytes. It emitted 14,006,784 bytes of
tagless transport to a test sink. An importer can use this boundary for a
verified single-image archive and needs an explicit per-image selection
path for multi-image catalogs. Engine registration and digest readback
remain the next integration gate.
