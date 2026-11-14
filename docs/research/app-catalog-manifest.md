# Offline application manifest (CAT-101)

Package authors need a versioned ownsolid document that identifies a local
deployment artifact, describes when the application can run, and drives the same
typed form in both supported languages. Engine-specific workload syntax remains
in the packaged chart or Compose project.

[Helm charts](https://helm.sh/docs/topics/charts/) separate package metadata from
values and allow schema validation of values. Dependencies can be carried in
`charts/`. [Docker Compose](https://docs.docker.com/reference/compose-file/services/#pull_policy)
supports an explicit local-only pull policy, and
[`compose up`](https://docs.docker.com/reference/cli/docker/compose/up/) has
switches to refuse pulls and builds. [OpenShift's disconnected catalog](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/disconnected_environments/olm-restricted-networks)
takes the application catalog and its image inventory into the isolated site.
The [OCI descriptor](https://github.com/opencontainers/image-spec/blob/main/descriptor.md)
binds artifact identity to a content digest. No implementation from 1Panel is
used.

Use a strict, bounded JSON document and sibling English and Simplified Chinese
locale documents. JSON avoids ambiguity from YAML tags, anchors and merge keys
in the admission format. Reuse the engines' native formats inside the bundle.
An ownsolid manifest identifies one app release, one local chart or Compose
artifact by SHA-256, a list of distinct image manifest digests, and an explicit
inclusive minimum and exclusive maximum for the platform. Use the stable
[SemVer](https://semver.org/spec/v2.0.0.html) triplet with bounded decimal
components. Reject prerelease, build suffixes and version expressions in v1.
Using chart metadata or Compose variables alone would give each adapter a
different compatibility and localization contract. Fetching charts, image tags,
schemas or translations by URL would break air-gap operation.

Forms are a bounded list of machine identifiers with typed text, integer,
boolean and enumerated choice fields. Locale dictionaries must supply the same
labels and descriptions, including labels for each choice. No credentials or
values supplied by a dependency belong in the form contract. Configuration
decoding rejects unknown fields and incorrect scalar types. Keeping dependency
bindings distinct lets a later resolver deliver secrets only as mounted file
references (CAT-102).

## Listing local releases

Operator Lifecycle Manager models package and bundle metadata separately
from installing a bundle. The Update Framework authenticates target
metadata before retrieving the target bytes. A local ownsolid catalog
can reuse its signed bundle index and locale verification to list
versions without scanning every chart or image archive.
[OLM file-based catalogs](https://olm.operatorframework.io/docs/reference/file-based-catalogs/),
[TUF target metadata](https://theupdateframework.github.io/specification/v1.0.37/#45-targetsjson-and-delegated-target-roles).

List up to 128 stable version directories for one authorized app and
trusted publisher. Verify each index, manifest and both locales, check
the signed app and directory version, and return newest versions first
by numeric version ordering. Each entry supplies the exact pin that
`VerifyInitialRelease` verifies at install time. Unsigned listing data
could misstate configuration or license text. Fully verifying each
payload during browsing would make the read depend on archive sizes.
Return metadata only and leave payload and authorization checks with
the installation and catalog service. A failed entry returns no list.
The app directory supplies the local set of versions.

At initial selection, authenticate the selected app, exact pin and
platform range using the bounded signed metadata before opening the
engine artifact or image archive. TUF checks trusted target metadata
before retrieval. Helm applies a declared platform compatibility
range during installation. Reuse the catalog's existing ownsolid range
and verify payloads under the same open version root after admission.
Checking compatibility after reading the media can obscure an
incompatible selection behind a missing archive. Test below-minimum
and exclusive upper-bound cases and changed pins with missing media.
A compatible selection still requires the complete signed payloads.
[TUF target retrieval](https://theupdateframework.github.io/specification/v1.0.37/#57-fetch-target),
[Helm compatibility metadata](https://helm.sh/docs/topics/charts/#the-kubeversion-field).

Test sorted versions, both signed locale dictionaries and the pin
handoff. With a missing archive, the release remains readable as
metadata and installation fails. Refuse a wrong app or publisher,
modified locale data, malformed or excess entries and cancellation.
Reuse the existing BSD-3-Clause Go standard library and catalog
verifier. This read adds no dependencies, models or datasets.

Tests must exercise loading the same app release from local files, with each
engine declaration, Chinese and English labels, accepted values and the
compatibility boundaries. Reject unsupported manifest versions, unknown and
duplicate keys, malformed UTF-8, missing translations, unsafe identifiers,
wrong types, version ranges with no compatible release and mutable image
references. Package validation performs local reads only. Artifact signature
verification and cross-checking the rendered image inventory, disabled pull
policies and workload privileges are required before execution through the
installer's admission path (CAT-105 and CAT-107).

## Engine compatibility handoff

The ownsolid platform range admits a selected signed app release. The engine adapter
also needs the target Kubernetes capabilities and the chart's declared
`kubeVersion` before rendering and admission. Helm 4.3.1 passes its capabilities
and values through dependency processing and schema validation. Use that pinned
engine boundary for chart semantics. Keep the catalog's stable ownsolid version
range and typed form as the package contract.
[Helm 4.3.1 installation source](https://github.com/helm/helm/blob/v4.3.1/pkg/action/install.go).

The current catalog package supplies the signed app ID, release pin, platform
range, typed form and both locales. The remaining consumer needs a server-authorized catalog
route, the actual running platform version and an installer operation that
retains the same pin through engine admission. The production package and
engine acceptance prerequisites are recorded in the
[bundle handoff](app-bundle-verification.md#application-chart-consumer).

The reader uses the existing Go toolchain and
[BSD-3-Clause standard library](https://go.dev/LICENSE), including its
[strict JSON decoder](https://pkg.go.dev/encoding/json/v2). Commercial use,
offline execution and redistribution with the required notice are allowed.
[Helm](https://github.com/helm/helm/blob/main/LICENSE) and the
[Compose specification](https://github.com/compose-spec/compose-spec/blob/main/LICENSE)
are Apache-2.0 references. This contract ships neither engine. An adapter must
check its exact shipped binary and packages separately. There are no new
dependencies, third-party application artifacts, models or datasets in this
contract.
