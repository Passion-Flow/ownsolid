# RKE2 component inventory in a signed release (DEL-101)

The current factory checks every outer asset against the signed manifest and
SPDX SHA-256 entries. That proves the RKE2 image sets transferred intact. It
does not describe the images, their base packages or their licence evidence.

[RKE2 v1.36.6+rke2r1 release assets](https://github.com/rancher/rke2/releases/tag/v1.36.6%2Brke2r1)
include the two image lists and the pinned archives. The upstream list
checksums and exact archive bytes are recorded in the
[signed delivery research](rke2-signed-delivery.md#pinned-upstream-bytes).
The core archive includes OCI indexes. The Canal archive uses Docker save
metadata, which provides an image config and layers but no registry manifest.
The Linux prototype verified 14 core and two Canal image references. It
matched the core OCI descriptors and compared each Canal config and ordered
layer digests with the corresponding Docker Hub amd64 manifest. Core Docker
save tags omit the implied `docker.io/` prefix. Canonicalization is limited
to that prefix. See [OCI descriptor digests](https://github.com/opencontainers/image-spec/blob/v1.1.1/descriptor.md)
and [Docker attestations and storage](https://docs.docker.com/build/metadata/attestations/attestation-storage/).

The approved archives carry provenance statements for 13 core images and
SPDX predicates for two of those. The Canal registry returned a subject-bound
SPDX predicate and provenance statement for both images. Their statements
were checked against registry descriptor digests and the same image
manifest digests verified against the archive. Provenance is build evidence.
Preserve the original available upstream SBOMs alongside local component
observations. A local scan of the same bytes with
[Syft v1.55.0](https://github.com/anchore/syft/releases/tag/v1.55.0) extracts
package database declarations and compiled Go build information without
starting containers or fetching dependencies. Where the package database
or binary does not provide a licence claim, preserve that fact with
`NOASSERTION`. Keep the original embedded notice texts and their hashes.

Use a separate SPDX 2.3 document for the approved RKE2 group. The main SPDX
references it by checksum and standard external document relationship. The
RKE2 document associates archives with images, layers and observed
components, and identifies the signed collection of licence and supplier
evidence. Pin those two companion assets by byte count and SHA-256 against
the exact approved release. The existing factory and verifier already
check each declared byte and sign the SPDX asset checksums. Pinning this
reviewed closure makes omissions and substitutions fail without adding a
container parser or running a scanner on an air-gapped installation.
An opaque image inventory supplied by the signer would not catch a
missing measured component. A second signing authority would complicate
offline trust and still not solve component discovery.

New factory builds with RKE2 must carry the component group. Existing
verified bundles remain readable under the old policy. An operator can
explicitly require component evidence when preparing a bundle.

## Licensing and failure tests

- Syft is an [Apache-2.0](https://github.com/anchore/syft/blob/v1.55.0/LICENSE)
  release-engineering tool. Its Linux amd64 tarball SHA-256 is
  `bb65249542db5fedeeb4d3f9b018b75e7264dbfbbb10486f044581a92f90490d`.
  The offline installer does not ship or execute it. The prototype disables
  update checks and remote Go licence lookups, then runs without an uplink.
- Python 3 standard-library tooling used to assemble the evidence is covered
  by the [PSF licence](https://docs.python.org/3/license.html). Linux zstd
  for inspecting the published image sets is available under
  [BSD-3-Clause or GPL-2.0](https://github.com/facebook/zstd/tree/v1.5.7).
  Neither is added as a dependency of the installer.
- Component declarations, upstream notices and applicable redistribution
  obligations remain separate. SPDX's
  [package information](https://spdx.github.io/spdx-spec/v2.3.1/package-information/)
  and [relationships](https://spdx.github.io/spdx-spec/v2.3.1/relationships-between-SPDX-elements/)
  distinguish supplied package licences, checksum identity and document links.
  The existing
  [top-level RKE2 licences](rke2-signed-delivery.md#licence-and-production-boundary)
  permit commercial use and offline redistribution subject to their terms.
- Test the real signed release with all companion assets. Reject a missing
  companion, wrong pinned digest, a missing external SPDX relationship and
  a changed payload. Verify the ordinary package and legacy RKE2 reader
  path. Run preparation with test trust in an isolated Linux namespace and
  prove the egress recorder sees its IPv4 and IPv6 probes.

## Verified image identity

The SHA-256 columns identify the Linux amd64 manifest and config. The
SPDX document also records every compressed layer in its original order.

| Archive | Published image reference | Manifest SHA-256 | Config SHA-256 |
| --- | --- | --- | --- |
| canal | `docker.io/rancher/hardened-calico:v3.32.2-build20261022` | `16e26488ced90cff8fd3df2f2192ed6dfab92ac8ab9a48e35307c884503c06d7` | `214e1b27975528e8711e406a64a20bf61cf19d5f6927784e915f9e3fe492b95b` |
| canal | `docker.io/rancher/hardened-flannel:v0.28.10-build20261022` | `a00813cb869d19ab43b0a6ad724c95ffe9981bc6ad1bc8310e65255e45c402ab` | `4f23b420044ae3a951f38e4b9db7ed82ce4a496459c503aec31b54942f5f13b5` |
| core | `docker.io/rancher/hardened-addon-resizer:1.8.24-build20261021` | `3a8850d570c1a8b30e240ca09b3d4c9692bd6aaa9628370a12bc018ad3b0e5a6` | `b49fdaff5f4c62435a98cade133531776c3eb65a743102cbd523b18353525abe` |
| core | `docker.io/rancher/hardened-cluster-autoscaler:v1.11.0-build20261020` | `1fb3234a3e49c7f8bd1d173325872e546596770b2764dca7978331481965513a` | `2f0503377f4c945d9881b61fef37056935d2d2ec5881ce27e876501305dd1027` |
| core | `docker.io/rancher/hardened-coredns:v1.14.7-build20261020` | `ba10d45d1a938a6be192eb63eecdc738e0a7efd035dee756ea5fd7cbacbda216` | `fece0a9b1a8b52a2b76569dcfe7e1cf219898c180ed0a8151f444ae758ce04cc` |
| core | `docker.io/rancher/hardened-dns-node-cache:1.27.2-r1-build20261022` | `dd00f782dc9b8b6385c5d0cc245e9c4109f034117ee30556e9a65b359a4e4ac9` | `586f32c9ff4210bdfb32122e20b64e1bd62e0eea48e2be44bfd3393879304e6a` |
| core | `docker.io/rancher/hardened-etcd:v3.6.14-k3s3-build20261020` | `7b8c5c6b45d34b6b39c7acd20aa3e30b46444df801122ccbe2c8f641141d4891` | `3635bedd5cf463b95d02768957269fcf812b9a53d8496d2e26098edaa7b453be` |
| core | `docker.io/rancher/hardened-k8s-metrics-server:v0.9.0-build20261020` | `55eba130634c424451c5d907a40edca5fd6e052a6f9189d464ddaf764a98e772` | `5512cb91ff065a5554c773c9aa835b3bb4be7871f2c1c8d598e1b87b36075fff` |
| core | `docker.io/rancher/hardened-kubernetes:v1.36.6-rke2r1-build20261029` | `d94933c0c61bab445780c085b75da36c070ec875bc328c9dbdd0b79f75b9aecf` | `73e5aadcbade720bbf28f64e4c11d7790dde168de1474b93377720c2518efee2` |
| core | `docker.io/rancher/hardened-snapshot-controller:v8.7.0-build20261021` | `fd6348c446dd27932d84a785a494aa36b91f7609ec36aeb70fb0634a1f265227` | `87bfd98d5d9a677a912d44b669f7dbd44bdc55b01c07f6e95f786cf63ab7fca6` |
| core | `docker.io/rancher/hardened-traefik:v3.7.16-build20261028` | `c7d907e9cabd97b1f98c082627fb2d9a2e05fc15315bb7ef3c92ee34bb772c31` | `6579fce137078f50b905109091dd3ffa1eb6a44c0f7960f2ef13073d577dde7b` |
| core | `docker.io/rancher/klipper-helm:v0.13.3-build20261021` | `7819a12c069a6e046d54560b5c9005b263910a51f8ef5d77d8dcea272e057272` | `4c0594a91721547ec7b6179a046e9cb305f7febfd26a1e2aea5345495eb20231` |
| core | `docker.io/rancher/klipper-lb:v0.4.17` | `d64ea02dfa3a29433a754fe59b56c6e1a05877528bdd6e20da587841ef22b0ac` | `eaa1212a2b4569c811bfa0ad5708428703ad3120a6dbae3b4353ec3cb36f36d2` |
| core | `docker.io/rancher/mirrored-pause:3.10.2` | `412c4a7219cb8a299a37337f3d87810c5340095322e15594a1637785adad0f17` | `4a83b15d3ecfe0d916b2d0a7991bc2854a629b8097017c2ee1ff65b30ae4c07c` |
| core | `docker.io/rancher/rke2-cloud-provider:v1.36.6-0.20261016153003-c2fdd7c56c2c-build20261021` | `3b2f7053e3292f580c55e452ec254a1bd3ab88c0700f199d5f550d74ac781656` | `187ad7d873e3fe0875ecf06bdafb71f878eb86e1c5b0f0bd50524c7f64c0406f` |
| core | `docker.io/rancher/rke2-runtime:v1.36.6-rke2r1` | `951817e2b1f0f60c9a4b0158e531a68d04a33dff831936ac4f5a530762a37fc4` | `e925758e26eacb3f69effc7f8187f092f5b57f06fcd8a3139bceeb209d3b3dae` |

## Reviewed companion assets

| Signed file under `rke2/` | Bytes | SHA-256 |
| --- | ---: | --- |
| `components.spdx.json` | 3131733 | 6062b5a8891cf22d328f7e53d9a6e9861b9601d0f0fa1814b59b5487df58aceb |
| `component-notices.tar` | 7393280 | fbbfebbf0339f07e17b65f02487792ef7abcc59c5922cca67284a0b7282e16c3 |

The SPDX document validates against the official 2.3 JSON schema. It has
16 image packages, 1,620 distinct measured component records, and 516
checksum-addressed evidence files. The notice archive contains 472 licence
texts, 25 exact Alpine source recipes and 19 intact upstream in-toto
statements, including the four publisher SPDX predicates. A separate check matched the image manifest
digests and verified every archived evidence file against its SPDX checksum.
The five SUSE source indexes named by image labels were accessible and
their exact URLs are preserved in the image packages. The archive also
retains the applicable embedded notices and the fetched upstream texts,
including Calico's six third-party notice files. The [Go module
proxy](https://go.dev/ref/mod#module-proxy) supplied source ZIPs for all
826 observed module versions with a published checksum in the compiled
build information. Their full [Go directory hashes](https://pkg.go.dev/golang.org/x/mod/sumdb/dirhash#HashZip)
matched, including the Azure SDK module. The source notices are linked to
those exact component identities. The Alpine installed database supplied
the [aports commits](https://gitlab.alpinelinux.org/alpine/aports) used to
recover all 25 source recipes. Those recipes are linked to the 38 observed
APK component records and identify the upstream source inputs.

Reassemble the pinned documents using `tools/release/offline/rke2_spdx.py`
and the retained observations and source materials from the verification.
The first argument names the observation directory containing one JSON
file per image, `licenses/` keyed by SHA-256, and the `upstream/`
statements. The second names the source directory containing
`source-index.json`, `module-source-index.json`, `apk-source-index.json`,
the matching SHA-256-keyed `source/`, `module-sources/` and
`apk-sources/`, and `rke2-binary-license.txt`. Use an existing empty
directory as the third argument. Keep all output bytes exact when staging the signed bundle.

## Remaining production evidence

The RKE2 release asset listing supplies no release-wide publisher SBOM.
The core index, registry referrers and digest-based SBOM tags yielded no
publisher SPDX for these exact images: addon-resizer, cluster proportional
autoscaler, CoreDNS, node-local DNS, etcd, metrics-server, Kubernetes,
Traefik, Klipper Helm, Klipper LB, mirrored pause and RKE2 runtime. They
have local observations instead. A publisher SPDX predicate and a local
scan have different coverage and neither substitutes for all notices.

The observed metadata declares a normalized licence for 258 components.
The document keeps `NOASSERTION` where a normalized licence declaration
was absent, even when it can link exact upstream source text. After
matching those texts and available embedded notices, 511 component
records still have neither a normalized declaration nor a linked text.
The measured binaries for those records do not provide a verified
source ZIP checksum or a matching embedded notice. Of those records,
236 have a source notice for the same named version verified in another
image. That supplies source context without binding it to the as-built
record. Forty-three have an unknown measured version. Resolve the exact
as-built source and licence correspondence before production distribution.
The Alpine recipes and SUSE source indexes are available. Qualification
of any corresponding-source payload a distributor must include remains
separate from this notice evidence. These limits keep the production
release gate open.
