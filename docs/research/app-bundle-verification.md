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

## Verified input for the importer

The current factory input contract consumes the archive digest and byte size
plus the selected image manifest and config digests. Return those facts
directly from the catalog preflight, using the same signed byte stream and
verified OCI graph. This lets the installer pass the facts to the existing
importer without opening the tar to discover a second identity. Keep the
signature-only API unchanged. Exercise the returned facts against the
tagless writer in the disconnected fixture. The factory accepted these facts
from the returned catalog value and reproduced the same layer and transport
measurements without a second caller-side tar parse.

## Local Docker registration and readback

The platform installer already uses `OpenCurrentImageSource` and
`WriteTagless` before Docker image load. Use the same factory boundary for
the catalog's first single-image Compose path. The
[Docker Engine API v1.49](https://docs.docker.com/reference/api/engine/version/v1.49/)
provides image load and platform-specific image inspect. Require the
[containerd image store](https://docs.docker.com/engine/storage/containerd/)
because it retains the OCI manifest identity.

A Docker Engine 29.1.3 prototype began with an empty private tmpfs store,
no network and a Unix socket only. Its image load accepted the tagless
VictoriaLogs archive. API v1.49 inspection by the signed digest returned
the same image ID and descriptor digest, Linux amd64 and an empty tag
list. Use that exact local readback before returning success. A matching
image on retry can be reused after the signature and expanded bytes are
checked again. A partial upload or lost response requires retry and
readback. Never retag or remove shared engine data.

Call only an explicitly configured Unix socket, with no proxy, redirects
or context discovery. Bound the operation and responses. The first importer rejected multi-image bundles before contacting the
engine and let the factory reject unsupported archive forms. Directly loading the
publisher's archive would let source names or tags reach the engine and
would skip expanded layer and DiffID checks. Registry pulls, daemon
discovery and starting the workload are outside the import boundary.

The implementation uses the existing ownsolid factory and Go standard
library. The Docker Engine fixture and API implementation are
[Apache-2.0](https://github.com/moby/moby/blob/master/LICENSE), permitting
commercial and offline use and redistribution under their notice terms.
The official image recipe is also
[Apache-2.0](https://github.com/docker-library/docker/blob/master/LICENSE).
The test engine and existing VictoriaLogs image stay in the isolated
development fixture. The catalog module ships neither engine nor image.


The catalog's `ImportInstallImage` entry point passed both the opt-in
integration fixture and the real signed VictoriaLogs archive on Docker
Engine 29.1.3 with fresh tmpfs stores and no networking. The real archive
was first verified against `bcb614f5` and repeated after the rebase. An expansion
limit of one byte and a different publisher key were refused. Two calls
with the trusted bundle left one untagged image with manifest and Docker
image ID
`sha256:45dc9bef07c0c404255790e41f2c22c62ff55a690a1d556e0f9cb6f6e2556f1b`.
The development engine was pinned to the Linux amd64 Docker Official
Image manifest
`sha256:64d6ee47ea821c986467199baa162f5ac8cde3f57b719f18e23f3ed7a7444131`.
The catalog-only delivery worktree on `6f4e45b0` repeated the real import
and the opt-in `TestImportInstallImageIntoEmptyDockerStore` with separate
empty daemons. The repeated import still produced one untagged Linux
amd64 image, with the signed digest in both the image ID and descriptor.

The installer must supply the authenticated publisher key and host policy,
invoke the import as an authorized operation, and carry the verified digest
into workload admission.

## Multi-image registration

OCI layouts support a root index with multiple descriptors while keeping
the manifest, config and layers content-addressed. The
[OCI layout specification](https://github.com/opencontainers/image-spec/blob/v1.1.1/image-layout.md)
defines this separation. [Containers Image](https://github.com/containers/image/blob/main/docs/containers-transports.5.md)
also selects an individual archive image instead of using a publisher's
tag as an installation identity. Use the catalog's already verified
manifest digest for that selection.

The release factory's `OpenCurrentImageSource` selects one image for a
platform release asset. Its inventory validator rejects a different
runtime manifest and requires one selected config. The first catalog
prototype kept this contract by writing a single-image projection for
each signed member. It passed the fault-injection and real Docker tests,
including retry after a second-load refusal and exact untagged readback.

Use the DEL-owned [`OpenCurrentImageSelection` handoff](catalog-image-selection.md)
for the shipped multi-image path. It takes the exact original archive
SHA-256 and size, the selected manifest/config and all approved pairs.
It checks the full local descriptor closure and verifies the selected
expanded layers and DiffIDs. Its tagless writer emits the original
runtime blobs and a single exact manifest descriptor. This removes
temporary projections and keeps the platform and single-image catalog
paths on the original reader.

Verify the complete signed archive before opening any selection. Open
all selections and retain their verified file descriptors before
opening the Docker socket. Bound the sum of expanded layer bytes by
the operation's budget, counting each use of a shared layer across
images. The same timeout covers preflight, transport and readback.
The catalog bounds the inventory at 128 images. Each open rechecks
the original archive, and the importer retains one descriptor per image.
A source that cannot be opened fails before registration. No new
dependency or image ships with this path. The Go standard library,
OCI specification and Docker development fixture keep the licensing
and offline use terms recorded above.

Reject a missing or altered member, an invalid later DiffID and an
aggregate expansion overrun before Docker access. Test a shared layer,
two distinct manifest readbacks and retry after a middle load failure.
A failed operation returns no successful bundle result. Keep any exact
content already registered and recheck the signed set on retry. The
caller owns publisher trust, authorization, audit, workload admission
and deployment state.

The catalog consumer passed separate empty-engine single-image and
multi-image tests in disconnected Docker Engine 29.1.3 on Linux amd64.
A signed later member with a wrong DiffID and a set budget one byte
below the sum refused before the first Docker request. An injected
failure on the second load left the first exact image registered.
Retry sent one load for the missing image, and a further call sent
none. Both readbacks matched the original untagged manifest digests.

A separately signed set containing real
[VictoriaLogs 1.53.0](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaLogs/v1.53.0/LICENSE)
and [VictoriaMetrics 1.155.0](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/v1.155.0/LICENSE)
passed two calls against another empty disconnected engine. Both projects
use Apache-2.0 for these fixtures. The fixtures remain outside the
repository and release. The engine retained exactly these Linux amd64
image IDs and descriptor digests, with no tags.

- `sha256:45dc9bef07c0c404255790e41f2c22c62ff55a690a1d556e0f9cb6f6e2556f1b`
- `sha256:37677492c6e08c70b91df8d0661ec14bd8da9163a4ed8e2bebb3f9a84cd64b84`
