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
Keep the analysis bounded to operation identities and the named response
model references below. Full schema compatibility
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

## Named response models in generated clients

The GW-104 read preparation demonstrates a separate source-compatibility
case: operation IDs can remain stable while a detail response or a page
item starts using a different named model. The pinned generators derive
these models from the contract's component references. Old generated
operation decoders can accept the new wire reply while regeneration still
changes the declared return types.

Extend the local audit to retain the named schema references reachable
from each response status and media type. Compare those sets only for an
existing operation with the same domain, method, URL, audience and operation
ID. Emit a review finding when the bindings change. Follow document-local
references with a visited set so recursive models terminate. Reject
unresolved or nonlocal response references without making a network call.

This is a source-review signal, not a proof of wire incompatibility.
Descriptions, property order and duplicate references do not change the
set. An inline field change, constraint change or a reference moved between
two fields can still need review even when the reachable names stay the
same. Request schemas and runtime behavior remain outside this check.
Comparing entire JSON documents would confuse annotations with behavior;
introducing a second general schema-diff tool remains unnecessary for this
narrow case. The OpenAPI and generator sources above, the
[Go generator's named models](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.2/README.md)
and the existing PSF licence apply. No dependency, model or dataset is added.

Test direct and nested model replacements, recursive and escaped local
references, unchanged bindings after annotation/order changes, audience
and operation-identity boundaries, and rejection of missing or external
references. Run the audit offline against the actual GW-104 candidate and
its local published baseline. Keep DEV-204 open: release-baseline selection,
general directional schema compatibility and runtime deprecation signaling
still require their owning release and server paths.
