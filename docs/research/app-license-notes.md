# Signed application license notes (CAT-108)

Operators reviewing a local app need the publisher's license and attribution
summary before they admit the workload. Package metadata can carry that
summary independently of the deployment engine. Artifact Hub reads a Helm
chart's license file or a publisher annotation. OCI has a license annotation
for image content. Both are component-level sources. An app can combine
multiple images and another engine artifact, so an operator also needs an
app release summary. [Artifact Hub Helm annotations](https://artifacthub.io/docs/topics/annotations/helm/),
[OCI annotations](https://github.com/opencontainers/image-spec/blob/v1.1.1/annotations.md).

Reuse the app manifest's two signed locale documents. Each accepts one
optional `license_note` of up to 2,048 Unicode code points. If one locale
supplies it, require the other locale as well. Reuse the existing plain-text
validation and return the notes on the verified release. Older bundles can
omit both notes. The installer and future console consume the authenticated
summary using the same release object as the form and image inventory.

This first path keeps the publisher's exact summary as plain text. Inferring
an app license from a chart or one image would lose information in the other
components. Requiring an SPDX identifier for prose would prevent publishers
from describing multiple component obligations. License expressions and full
notices can stay with the component packages. A signed summary is display
metadata. Site admission, tenant visibility, quota and GPU reservation
remain with their service owners.

The change uses existing Go JSON and signature validation under BSD-3-Clause.
It adds no dependencies, models or datasets. The OCI specification is
Apache-2.0. Those licenses permit commercial and offline use and
redistribution with the applicable notices. The tests use synthetic note
text. [Go license](https://go.dev/LICENSE),
[OCI license](https://github.com/opencontainers/image-spec/blob/v1.1.1/LICENSE).

Verify both notes through the existing signed installation bundle path.
Reject a one-sided translation, empty, oversized or control-containing
text, and a note edited after signing. Verify a legacy bundle with both
notes absent. Use the same bounded local files and run the tests without
network access.

The signed installation verifier returned both exact notes and accepted a
legacy bundle with neither note. It refused both one-sided variants, empty
text, control characters, an over-limit Unicode note and unsigned edits.
The tests passed natively and in a disconnected Linux amd64 container
running as a non-root user.
