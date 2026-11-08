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
source ZIP checksum or a matching embedded notice. Resolve the exact
as-built source and licence correspondence before production distribution.
The Alpine recipes and SUSE source indexes are available. Qualification
of any corresponding-source payload a distributor must include remains
separate from this notice evidence. These limits keep the production
release gate open.
