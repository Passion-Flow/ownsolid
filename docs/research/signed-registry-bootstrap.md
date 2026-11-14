# Bootstrap the in-site OCI registry from a signed release (DEL-108)

[NET-102](oci-registry-transport.md) verified TLS, authentication, OCI upload
locations and digest readback on CNCF Distribution v3.1.2. The control module
owns tenant pins, grants and consumer authorization. The delivery lane can
install the shared backend without adopting those application authorities.
The current signed release already authenticates complete OCI archives,
selected Linux amd64 manifest and config digests, layer closure and
compressed and expanded content. Retain that contract and its trust policy.

Use the [official Linux amd64 Distribution 3.1.2 image](https://github.com/docker-library/repo-info/blob/master/repos/registry/remote/3.1.2.md)
at manifest digest `sha256:52b489ad8e6f40d1a676702e3ce398ec796ce38f08fbb786e1f324baa12d84b8`
and config digest `sha256:39a2a88344598be51c03d6829b40dd29fcda04ce1e01ef1f7596b5af744276ce`.
The [official build recipe](https://github.com/distribution/distribution-library-image/blob/775973b655bb4b11561db92a1cf50113148ce530/Dockerfile)
pins the upstream Linux binary archive. Distribution is
[Apache-2.0](https://github.com/distribution/distribution/blob/v3.1.2/LICENSE).
Its Alpine base and CA packages carry their upstream licences, including MIT
for musl, GPL-2.0-only for BusyBox and MPL-2.0 for the Mozilla CA bundle.
Retain the upstream license and source obligations when redistributing the
image. Keep production component notices and source correspondence under
DEL-110. The existing Go x/crypto bcrypt implementation is
[BSD-3-Clause](https://cs.opensource.google/go/x/crypto/+/refs/tags/v0.55.0:LICENSE).
These dependencies permit offline and commercial use subject to their
licences. This path adds no model or dataset.

The pinned image's Alpine package database identifies the following
versions and declared licences. Package recipes and source references live
in [Alpine aports](https://gitlab.alpinelinux.org/alpine/aports).
The official image's embedded `/bin/registry` has SHA-256
`7f9e0df42bd028efb1a11eb7f92ef494156bf308770303a256d3acdcadb378cd`.
Its Go build metadata reports Distribution v3.1.2 and includes the upstream
cloud storage drivers. The site configuration activates the
[local filesystem driver](https://github.com/distribution/distribution/blob/v3.1.2/registry/storage/driver/filesystem/driver.go),
which syncs file contents and syncs directories on atomic metadata writes.
The [upstream module declarations](https://github.com/distribution/distribution/blob/v3.1.2/go.mod)
and the actual binary's build metadata are inputs for the DEL-110 notice
inventory.

| Package | Version | Declared licence |
| --- | --- | --- |
| alpine-baselayout, alpine-baselayout-data | 3.7.2-r1 | GPL-2.0-only |
| alpine-keys | 2.6-r0 | MIT |
| alpine-release | 3.24.4-r0 | MIT |
| apk-tools, libapk | 3.0.9-r0 | GPL-2.0-only |
| busybox, busybox-binsh, ssl_client | 1.37.0-r31 | GPL-2.0-only |
| ca-certificates, ca-certificates-bundle | 20261020-r0 | MPL-2.0 AND MIT |
| libcrypto3, libssl3 | 3.5.9-r0 | Apache-2.0 |
| musl | 1.2.6-r3 | MIT |
| musl-utils | 1.2.6-r3 | MIT AND BSD-2-Clause AND GPL-2.0-or-later |
| scanelf | 1.3.9-r1 | GPL-2.0-only |
| zlib | 1.3.2-r1 | Zlib |

## Selected path

- Add an optional signed prerequisite and an exact `registry-image` asset
  to the current release profile. Existing bundles retain admission.
  The Registry command requires the optional group. Select every other
  signed `image_archive` and preserve the selected runtime manifest,
  config and layers by digest. Reuse the verified OCI archive and its
  opened descriptor. Do not re-tag and push through Docker because
  representation changes can alter the manifest digest.
- Start a separate site registry from the signed image already loaded in
  local Docker. Use the [OCI distribution protocol](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-blobs)
  and the same-origin upload Location rule used by NET-102. A temporary
  writer binds only to loopback. It uses the site's TLS certificate
  and an ephemeral bcrypt credential. The seed client pins the loopback dial and
  verifies the site's private IPv4 certificate identity. Only the writer
  gets a writable local registry volume.
- Publish the same verified data on the configured private IPv4 address
  with [Distribution read-only mode](https://distribution.github.io/distribution/about/configuration/#readonly)
  and a read-only bind mount. Use local filesystem storage, disable upload
  purging in the serving process, and configure no proxy or external
  exporter. The [Distribution deployment guide](https://distribution.github.io/distribution/about/deploying/#restricting-access)
  requires bcrypt for htpasswd. Read credentials and TLS keys from
  protected files. Keep a private state binding to the exact release
  and site. On retry, use the bound data, check every manifest and blob
  by digest, and never replace a foreign Docker container.
- Use `ownsolid/release/<signed asset ID>@sha256:...` as the site delivery
  namespace. It grants no tenant pin or workload admission. In the
  supported first path, operators provision a private IPv4 TLS certificate
  and the node trusts the site CA and uses the exact endpoint and digest.
  [RKE2 private registry configuration](https://docs.rke2.io/install/private_registry)
  requires trust and auth on each worker and explicit suppression of the
  default endpoint fallback when used as a mirror. The cell and agent
  owners retain those consumers. This backend can be shared by them.
  External object backup fencing remains a separate REL-103 dependency.

## Acceptance

Use the official pinned image and actual signed Linux amd64 OCI assets on
`ownsolid-cpu`. Two isolated nodes may use independent Docker daemons and
network namespaces linked only by a private veth. Verify an empty node
pulls a complete signed image by digest using TLS and authentication.
Require refusal for anonymous access, an untrusted CA, a missing or wrong
digest, a missing optional signed group and a tampered release.
Check repeat install leaves the serving container identity and verified
digests intact. Assert the serving endpoint refuses mutation. Use the
egress recorder for external routes and record probes separately.

## Prepared release and image proofs

The DEL-108 installer prepares `ownsolid-single-node-linux-amd64` using the
current inventory validator. The consumer originally rechecked the same
prepared tree with the legacy validator, which rejects that profile. A cached
image proof also used the legacy validator directly. Both consumers need
the same signed profile rules and trust as the installer, including the
pinned Distribution prerequisite and the current versioned release root.

Use the bounded manifest bytes to choose the existing validator and trust
loader. A prepared read first matches the caller's independent manifest
digest. The signature must authenticate those same bytes, and the protected
tree, completion journal and all signed assets must pass the existing full
readback before any image proof is issued. Return the verified profile for
the caller's OCI archive reader. Keep the proof transport unchanged so cached
legacy proofs continue to verify. The standalone verifier checks the exact
signed subset for cache readback under independently configured public trust.

Changing the profile in a manifest or trusting a Registry digest alone would
discard signed release authority. Trying multiple trust policies after a
refusal would make errors ambiguous and could bypass current-profile rules.
OCI descriptors bind content type, byte count and digest. TUF also separates
trusted root metadata from content metadata. This change reuses ownsolid's
versioned trust and does not introduce TUF metadata or a new signer.
[OCI descriptor specification](https://github.com/opencontainers/image-spec/blob/v1.1.1/descriptor.md),
[TUF specification](https://theupdateframework.github.io/specification/latest/).

The focused fixture uses the same signed archive for preparation and proof.
Cover the previous fixture profile, the current profile with the DEL-108
Registry group, exact manifest bytes and independent digests, unavailable or
expired trust, and missing or tampered files outside the selected image.
Use the signed asset metadata to open the selected OCI content. This proof
slice adds no dependency, model or dataset. It reuses the existing Distribution
license inventory and Go's [BSD-3-Clause](https://go.dev/LICENSE) standard library.

### Linux amd64 proof acceptance and NET-102 handoff

On `ownsolid-cpu`, the actual installer prepared two signed current-profile
fixtures, one with the pinned Distribution 3.1.2 OCI archive and one without
the optional group. For the site group, the archive SHA-256 was
`0c9e8a710a6f4c43df178d3ddcf1c12193048ea3b1cbc1c8ab4238fcfbc6081e`
and the manifest SHA-256 was
`90e58acd73e8432af8bda0b9cff579e7f640f73591f925bafc569677c8d39d7c`.
On each exact prepared directory the previous factory CLI returned
`RELEASE_PREPARED_INVENTORY_INVALID`. The corrected CLI exported the
original manifest with both the existing key list and the current versioned
root. Library verification recovered the same asset and opened its OCI
manifest and config at the signed digests.

The [focused fixture](../../tools/release/offline/internal/factory/image_proof_test.go)
also passed the legacy profile and refused an incomplete signed site group,
a missing or modified unrelated file, a changed manifest or signature,
an expired root, an unprotected directory and either wrong expected digest.
All testing used a small tmpfs staging area. In the isolated Linux network
namespace both default routes pointed to the recorder, which captured only
the IPv4, IPv6 and DNS probes. The real Registry payload was staged before
the network namespace started. The signed control image in the focused
fixture uses synthetic OCI content.

The [factory consumer interface](../../tools/release/offline/README.md#offline-release-factory-and-shared-bundle-verification)
is ready for NET-102. Use the verified profile to select the OCI reader
and use full prepared verification for catalog and recovery. The OBS
[site admission fixture](../../services/control/internal/modules/imageregistry/site_admission_integration_test.go)
previously expected current-profile refusal. Its consumer acceptance needs
to assert the successful current-profile import and site digest readback.
DEL-108 retains its installed managed-node and production release requirements.
