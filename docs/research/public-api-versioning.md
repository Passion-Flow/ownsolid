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
Keep the analysis bounded to operation identities, named response model
references and closed response object shapes below. Full schema compatibility
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

## Closed response object shape review

The MOD-103 family-history prototype exposed a gap in the named-model check:
adding family_id to the existing closed AdminModelDetail keeps its component
name but gives old strict-schema consumers a field their schema rejects.
The [JSON Schema object reference](https://json-schema.org/understanding-json-schema/reference/object#additionalproperties)
defines additionalProperties=false, and the [required keyword](https://json-schema.org/understanding-json-schema/reference/object#required)
distinguishes an optional field from an always-present field. The existing
AIP-180 distinction between source, wire and semantic compatibility still
applies; generated SDK source can change even when some decoders tolerate a
wire change.

Extend the existing offline response traversal with a small shape inventory:
property names, required names and whether each reachable object is closed.
Keep stable document-local reference locations and escaped inline JSON pointers,
with the existing visited-reference guard. Compare an existing operation only
when domain, method, path, audience and operation ID match. A changed or removed
shape involving a previously or currently closed object requires review.
Keep the named-model signal independently: changing a reference and changing
the fields of the same reference are separate cases.

This is a review signal, not a directional Schema-subset proof. A narrower
response can still change a generated source type. Numeric/string constraints,
property value schemas, requests, effective composition and runtime behavior
remain outside this shape comparison. In particular, unchanged shape names do
not establish compatibility. Do not solve arbitrary JSON Schema implication
or add a second diff dependency for this focused regression.

Tests must cover a newly optional field on a closed response, field removal,
required-name changes, opening/closing an object, inline array items, recursive
and escaped references, status/media separation and both audiences. Annotation
and key/required ordering must be inert. Unchanged objects and new operations
must not acquire findings. Retain invalid/local-reference refusal and existing
named-model checks, and compare the actual isolated model-history revisions:
revision A changes old closed metadata, while revision B leaves it unchanged.
No dependency, model, dataset or third-party source is added. The existing
Python PSF licence permits commercial and offline use and redistribution under
its terms. DEV-204 remains open for release baselines, complete directional
Schema review and runtime deprecation signaling.

The targeted Linux tests passed in a network namespace without an external
interface: nine shape cases, six named-model cases and seven inventory cases.
The actual revision-A prototype produced one old AdminModelDetail field-shape
finding while retaining its named response binding. Revision B produced no
shape or named-model findings for its existing same-identity reads. The current
430-operation inventory had no declaration conflicts, and self-comparison was
empty. These results do not qualify the prototypes' new routes or missing
non-prototype operations. Layout, roadmap and diff checks also passed. The
complete offline make test-tools target also passed on Linux: 13 layout tests
and all 57 SDK/tooling cases. It reused prepared pinned Node parser dependencies
through a read-only cache path in an isolated network namespace. No assertion,
case or expected result changed. A fresh worktree needs those Web dependencies
before its Help projection cases; the first full-check attempt stopped at that
missing prerequisite and its failed log is retained. Whole repository
checks/build/test remain a separate publication boundary for this candidate.

## Document-local response pointer evaluation

The existing response audit refuses a valid reference into an allOf array and
an encoded component name, while accepting an invalid tilde escape when a
matching literal key exists. [RFC 6901 sections 3, 4 and 6](https://www.rfc-editor.org/rfc/rfc6901)
define token escapes, array indices and UTF-8 URI fragments. The
[OpenAPI Reference Object](https://spec.openapis.org/oas/v3.1.1.html#reference-object)
uses URI references. Decode a local fragment once, apply tilde escapes once,
and distinguish object names from canonical zero-based array indices.

Keep the existing nonempty document-root pointer scope. Reject malformed
percent/UTF-8/tilde encodings, invalid indices, missing values and external
resources with a clear ValueError. Preserve raw named-reference bindings and
the visited set so this correction does not redefine compatibility findings.
Anchors, external retrieval and nested resource base-URI resolution remain
outside this bounded audit. A second resolver dependency or a broader Schema
compatibility engine is unnecessary. Python's existing urllib.parse and
standard-library code use the PSF licence already recorded above; commercial,
redistribution and offline use remain permitted under its notices. No model,
dataset, third-party implementation or standards text is incorporated.

Tests must reproduce array-element response shapes and review their changes,
encoded names including UTF-8 and a literal percent, one-pass tilde/percent
decoding, recursive references and shared Response Objects. Malformed indices
must fail even if Python would otherwise accept them; numeric object keys stay
valid. Invalid encodings and missing/external targets fail without opening a
socket. Unchanged public contracts must retain the same inventory and findings.
These tests do not qualify runtime compatibility or complete DEV-204.

The focused response/inventory regression set passed all thirty-one cases,
including nine new pointer cases. The actual 430-operation inventory is byte
identical before and after this resolver correction. Comparing against main
9e32b023 still produces exactly the existing explicit audit-read 409 model
binding review and exit 1, with no declaration findings. No rule or failure exit
was weakened. The separate exact 9be Linux full attempt remains failed at Web
ESLint under the acceptance wrapper's imposed 2048 MiB heap limit; this Python correction does not qualify or rerun
that gate. Release and runtime deprecation work remain open.

## Reachable response schema types and the audit-read 409 review

The retained 430-operation report has one source-compatibility review:
GET /api/v1/admin/audit-events, Admin listAuditEvents, adds an explicit
409 application/problem+json binding to Problem. Baseline 9e32b023 already
has the same default Problem response and the same Problem schema. The
server registry maps STORAGE_COMMIT_OUTCOME_UNKNOWN to 409 and the HTTP
handler propagates its service error without releasing a page. These are
static source observations, not a new HTTP or database run. Regeneration
changes which Go response accessor decodes 409; the SDK README records
GetApplicationproblemJSON409() and deliberate retry behavior. Neither
that migration text nor successful declaration checks approve the source
transition on behalf of the release owner.

Exact source audits on 2e64ab4c, 37cbb09c, 9f26da06 and WEB 82c602d3
produce the same report against 9e32b023, byte for byte, with exit 1.
The 2e and WEB declaration-only reports each have 430 operations, no
findings and exit 0. Thus this finding predates the strict JSON reader.
Do not remove 409, replace the baseline with the candidate, suppress the
finding or call declaration success a compatibility pass.

A separate reproduced auditor defect accepts a numeric response schema,
a reference to a string and a reference to an array, returning exit 0.
The [OpenAPI Schema Object](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
uses [JSON Schema object or boolean schemas](https://json-schema.org/draft/2020-12/json-schema-core#section-4.3.1).
Require those two types at a declared response media schema root and at
each followed schema reference. An omitted media schema remains allowed;
an explicitly null schema does not become an omission. Do not interpret
examples or other annotations as schemas, validate every unreferenced
component, or introduce a general dialect validator. Preserve boolean
schemas, recursive schemas, the Response Object resolver and current
status/model review semantics.

Invalid inputs must fail with exit 2 and no partial JSON report. Add the
source filename, method and URL to response traversal failures so an
operator can identify the broken declaration. Do not echo scalar payload
values. Tests cover root and referenced invalid types, both booleans,
empty object and omitted schemas, shared Response Objects, annotations,
baseline errors and the intentional default-to-explicit 409 review. All
current operation rows and findings must remain unchanged. The standard
library and existing PSF licence suffice; no dependency, model or dataset
is added. Full generation, native clients and complete release gates
remain outside these static checks.

The schema-type correction passed 39 focused Python cases, including eight
new cases. Its complete 430-operation comparison against 9e32b023 remains
byte-identical to the retained original report, SHA-256
910a412a5bef5071ac9a0409adc5388b7507001cf4bef306fc4f8803df240422,
with exactly the explicit 409 source-review finding and exit 1. No generated
client, HTTP handler, API document or Web file changed with this correction.
