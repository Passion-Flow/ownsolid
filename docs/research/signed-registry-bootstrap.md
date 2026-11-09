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
