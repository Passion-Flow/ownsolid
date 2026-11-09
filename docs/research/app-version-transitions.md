# Signed application version transitions (CAT-105)

Operators need one verifiable next upgrade and the exact previously installed
artifact for recovery. The engine name and version number alone do not bind
the chart or Compose bytes, locales or images.

Operator Lifecycle Manager defines explicit upgrade edges and can step through
a replacement chain one release at a time. Helm uses an explicitly selected
release revision for rollback. SemVer requires a new version when published
content changes. The catalog applies those patterns to its shared Compose
and Helm package contract. [OLM upgrade graphs](https://olm.operatorframework.io/docs/concepts/olm-architecture/operator-catalog/creating-an-update-graph/),
[Helm rollback](https://helm.sh/docs/helm/helm_rollback/),
[SemVer 2.0.0](https://semver.org/spec/v2.0.0.html).

Use an optional `upgrade_from` field in the signed application manifest. Each
edge names a strictly older stable version and the SHA-256 of its exact signed
`bundle.json` bytes. Require distinct predecessor versions and bound the list.
The signed index already authenticates the manifest and image archive. The
manifest binds the engine artifact. A verified install bundle returns its own
version and signed index digest for the installer to retain with the successful
installation. Old manifests without the field remain valid and can be named as
predecessors.

Upgrade selection authenticates the current pin and scans the bounded set of
version directories in one private immutable app catalog. Require directory
versions to match their signed manifests. Choose only a unique direct successor
of the exact installed pin. A supplied expected target must match that choice,
and a fork refuses even when one candidate is named. Do not infer a migration
from numeric ordering, skip through the graph, or fall back to another release
after an integrity failure. Rollback uses the exact predecessor pin from the
installation record and requires that the installed manifest declared the edge.
Both endpoints must support the running platform and use the same engine.
A cross-engine migration and a transition from an incompatible installed
release need their own tested adapter and migration policy.

Authenticate bounded metadata while finding edges. Verify the chosen
destination with the existing installation bundle verifier, including its
signed engine artifact and complete OCI image closure. Compare the returned
pin to the one selected from metadata. An upgrade also verifies the
installed bundle's full payload so its recovery artifact is available.
Rollback requires only authenticated metadata for the failed current
release and verifies the exact predecessor's full payload. The byte limit
applies independently to each verified bundle. Keep the same private immutable
staging custody through install. This works with the publisher key already
configured for a catalog, without registry calls or additional trust files.
A missing installed, rollback or explicitly expected release refuses. Fork
detection covers the local catalog snapshot. A version omitted from that
snapshot and never named by the caller cannot be inferred from independent
bundle signatures. A future distribution layer can authenticate the complete
available snapshot if required. [The Update Framework snapshot metadata](https://theupdateframework.github.io/specification/v1.0.36/).

The implementation uses the Go standard library under BSD-3-Clause and the
existing signature and OCI readers. No engine, dependency, model or dataset
is added to the product. Helm and Compose are Apache-2.0 references and
developer fixture tools. These licenses allow commercial and offline use
and redistribution with their notices. The fixture chart and Compose
files remain synthetic. [Go license](https://go.dev/LICENSE),
[Helm license](https://github.com/helm/helm/blob/v4.3.1/LICENSE),
[Compose license](https://github.com/docker/compose/blob/main/LICENSE).

Acceptance exercises the verification entry point twice per transition for
signed native Compose and Helm artifacts, using distinct image and engine
digests between releases. It covers an unsigned or altered candidate or image,
a missing endpoint, version-directory substitution, a republished predecessor,
an incompatible platform or engine, a skipped edge and multiple successors.
The installer still owns authenticated installation history, authorization,
audit, workload admission and execution, configuration and data recovery,
and uninstall with an explicit retention choice.

## Verification

The Compose and Helm fixture bundles use native engine artifacts and different
signed image digests per version. Both selected the next exact release and
recovered the original artifact and OCI digests on repeated verification.
Rollback still selected the original bundle after the current artifact was
removed from the fixture. Upgrade refused the same loss on its recovery side.

The Linux amd64 test binary also ran in Docker Engine 29.1.3 with
`--network none` and `--pull never`. Both engines' fixtures imported the
initial and selected upgrade images through the catalog's Docker Unix
socket path. Repeated rollback verification and import reused the exact
original images without adding images. No workload was started.
