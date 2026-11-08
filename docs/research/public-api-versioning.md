# Public API versioning

DEV-204 needs an offline way to preserve public operations across
upgrades and to communicate migration when an operation is superseded.

[Kubernetes](https://kubernetes.io/docs/reference/deprecation-policy/)
maintains API versions during migration and applies policy between
releases. [Google AIP-180](https://google.aip.dev/180) separates wire,
source and semantic compatibility. For ownsolid, an API URL can keep
working while a renamed OpenAPI operation ID still breaks generated
clients. Changes therefore need review against a released contract
snapshot as well as handler and SDK tests.

Compare the audit's per-domain, per-audience operations against a
local baseline. Detect removed operation identities, audience removal
and operation ID changes, while accepting additional operations.
Keep the analysis bounded to operation identities. Full schema compatibility
needs direction-aware JSON Schema checks, and behavior needs real server
tests. A general diff such as [oasdiff](https://www.oasdiff.com/docs/breaking-changes)
can support deeper review, but introducing it solely for this check adds
a second projection and toolchain. Use the existing Python standard library
inventory without a new dependency, model or dataset. The
[PSF license](https://docs.python.org/3/license.html) permits offline and
commercial use and redistribution under its terms.

[RFC 9745](https://www.rfc-editor.org/rfc/rfc9745) defines when a URI can
be announced as deprecated without changing its behavior.
[RFC 8594](https://www.rfc-editor.org/rfc/rfc8594) communicates a planned
sunset separately. An OpenAPI flag alone cannot implement runtime
signaling. Use local migration documentation and preserve the older
operation until an explicit version transition. No standards text or
code is incorporated into the implementation.

Test removal of one audience, renamed operation IDs, additive endpoints
and paths moved between SDK domains. Re-run against an actual released
contract directory with the network disconnected. The full item requires
the release owners to select and retain those baselines, wire the release
check and test the runtime deprecation behavior across supported versions.
