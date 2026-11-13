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

The schema-type correction at 3e82919e passed 39 focused Python cases, including eight
new cases. Its complete 430-operation comparison against 9e32b023 remains
byte-identical to the retained original report, SHA-256
910a412a5bef5071ac9a0409adc5388b7507001cf4bef306fc4f8803df240422,
with exactly the explicit 409 source-review finding and exit 1. No generated
client, HTTP handler, API document or Web file changed with this correction.

## Response status and media declaration removals

The next offline reproduction removes application/json from an inline
string response while retaining text/plain. A second removes a declared
204 response while retaining 200. Both currently return exit 0: neither
uses a named model or a closed object, so the existing checks have no
edge to compare. These are valid before/after operation declarations,
not malformed-schema cases. The
[OpenAPI Responses Object](https://spec.openapis.org/oas/v3.1.1.html#responses-object)
and [Media Type Object](https://spec.openapis.org/oas/v3.1.1.html#media-type-object)
identify status and media bindings independently of schema names. Generated
decoders depend on these declarations even when the schema is inline or
omitted. The existing AIP-180 source/semantic distinction applies.

Inventory every resolved response status with its declared media types,
including an empty list for a response without content. For unchanged
operation identity and audience, emit PUBLIC_RESPONSE_BINDING_REMOVED if
an old status disappears or an old media type is removed. A new default
response does not prove that an old generated status accessor survives;
retain that review. Additions, descriptions and ordering stay inert. This
is a declaration/source review, not a directional wire-safety proof. It
must not redefine the existing named-model or closed-shape findings.

Reuse the resolved response traversal and the same-identity matching for
all three response comparisons. Preserve the existing response_surface
function's pair return for its callers. No second resolver, dependency,
model or dataset is needed; Python's existing PSF licence applies.
Test inline and omitted schemas, no-content statuses, shared responses,
both audiences, default fallback, identity changes, harmless additions and
annotation/order changes. Reconcile all 430 actual operations, retaining
exactly the original explicit 409 review against 9e32b023. The report gains
response_media_types; old retained reports remain immutable, and their
original fields must match rather than claiming whole-report byte identity.

The expanded regression set passed 47 Python cases, including eight schema
cases and eight response-declaration cases. All 430 old operation fields
remain identical. The current inventory adds 1,254 status declarations and
1,192 media bindings. Comparing every operation against 9e32b023 finds 429
unchanged response declarations and exactly one added explicit 409 binding
for the Admin audit read. No old status or media binding was removed. Fresh
checks of the 2e, 37, current and WEB82 input directories each preserve the
one original named-model review and exit 1. Declaration-only current audit
returns 0. The expanded report SHA-256 is
f018d11ec45f5543229676f69688992e0ed4a984131c55165cfc7ca525f88aaa;
it differs from the retained 910a412a report by the added inventory field,
not by removed operations or a suppressed 409 finding. No SDK generation,
compiler, socket, service, carrier or complete gate was used for this work.

## Formal audit-read 409 migration decision

The comparison is baseline 9e32b023a0a3deed6a8653a8878d52c2583fd6cf
against joint source 2e64ab4c6c16bd84ae5c101b9de5444550760ff2. All
identity components are equal. The operation changes only its description,
the cursor description, the explicit 409 response and its x-error-codes entry.
The existing default and new 409 reference the same Problem Response Object.
AdminSession security, audience, method, path, operation ID, filters, required
parameters, success schema and pagination are unchanged. The transaction
adapter, HTTP problem registry, identity list service, audit-read accounting
and actual commit-fault HTTP test sources are also unchanged between those
two revisions. This is a correction to the documented existing failure,
with a real generated-client source consequence, not a new server failure
mode or an auditor false positive.

The [OpenAPI Responses Object](https://spec.openapis.org/oas/v3.1.1.html#responses-object)
uses default for otherwise undeclared statuses. The pinned
[oapi-codegen response template](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.2/pkg/codegen/templates/client-with-responses.tmpl)
instead chooses the explicit branch after regeneration. The retained identity
SDK source confirms that 409 then fills ApplicationproblemJSON409 and
Headers409, leaving the old Default fields empty. Python's retained decoder
returns the same Problem class from either branch. These are source inspections;
an old compiled SDK has not been run against a new server in this review.

Keep API v1 and the explicit 409. Preserve the old Go Default Problem and
header accessors for this response as well as the new 409 accessors. Apply
the compatibility adaptation only to ParseListAuditEventsHTTPResponse,
only when 409 and default retain the reviewed shared Problem reference.
Refuse changed or ambiguous generated parser shapes. Preserve one decoded
Problem value; copy the identically shaped header value using the
[Go explicit struct conversion](https://go.dev/ref/spec#Conversions), so a
future incompatible generated type fails compilation rather than losing fields.
Do not change another operation, the HTTP transport, automatic retry behavior,
contract metadata version 0.5.0 or the compatibility auditor. A synthetic
API-major or SDK release number would not resolve the field regression.
There is no selected published SDK release version to increment here.

The alternative of requiring every caller to abandon the old Default fields
would impose an avoidable source behavior change within v1. Removing 409
would hide the actual runtime failure. Suppressing the audit finding or
substituting the candidate as baseline would hide the migration. Reject all
three. The chosen design is recorded in
[ADR 0085](../decisions/0085-preserve-audit-read-default-response-accessors.md).
No dependency, model or dataset is added. Existing generator Apache-2.0,
Python PSF and Go BSD-3-Clause licence obligations remain as recorded in
[SDK research](sdk.md#licenses); commercial, redistribution and offline use
remain permitted under those terms.

Pure Python tests must cover the scoped parser adaptation, reapplication,
missing/ambiguous/changed parser and contract refusal, and preservation of
other operations. Native assertions must check old/new fields, code, request
ID and header values for both representations, one request after 409 and
lost replies, and a deliberate second request with a distinct server ID.
Existing real PostgreSQL/HTTPS tests cover both a COMMIT not forwarded
(zero persisted reads) and an acknowledged server COMMIT whose reply is
suppressed (one persisted read); neither releases a page or Retry-After.
They also cover revoked sessions, forged entrance and cross-workspace refusal.
These tests are acceptance requirements, not newly executed results.

The raw 430-operation audit must still contain the one
PUBLIC_RESPONSE_MODELS_CHANGED review and exit 1. Technical migration review
is accepted with the compatibility adaptation; release compatibility remains
unqualified until fresh full SDK generation, old/new client upgrade/rollback,
native tests and complete check/build gates run on the final source. Retain
the selected supported-release baseline; 9e32b023 is this review's exact
comparison source, not proof that a released-support baseline was selected.

The focused suite passes 56 Python tests: the retained 47 audit regressions,
seven response-compatibility cases and two transport-hook text cases. It
starts no compiler or real generator. An in-memory transformation of the
retained 122-file identity SDK archive's actual Go parser also preserves
every byte outside that parser and is unchanged on reapplication; no generated
file is written. The current declaration audit returns 0 for 430 operations.
The comparison against 9e32b023 returns 1 with the same sole 409 finding and
the unchanged f018d11ec45f5543229676f69688992e0ed4a984131c55165cfc7ca525f88aaa
report digest. The added native accessor/header assertions have not run.
This is not fresh generation of all 28 SDK domains or a native/full-gate result.

### Compile the same legacy caller against both SDKs

The original 9e32b023 generator has no audit-read transport helper and its
default-only contract produces no dedicated 409 accessor. The retained 122-file
identity archive already has an explicit 409 decoder; it is not that older SDK.
Generate the actual 9e32b023 source separately for this comparison. Keep its
original generator and five templates; do not synthesize an old client by
removing fields from new generated code. The final candidate still has 36
generation inputs: 29 raw contracts, six templates and the generator. Only
the generator changes from 37cbb09c to e4bd3138; existing raw contracts and
templates remain identical. Original joint 2e64ab4c output remains a separate
comparison requirement, not a substitute for the default-only baseline.

A self-contained Go legacy-caller test must assign the public Default body and
header fields, read the existing getter and use the actual generated operation.
Compile that exact source against both 9e32b023 and the new SDK, then compile
the explicit-409 tests against the new SDK. Run the legacy source against the
old SDK again for the client rollback path. These are real SDK compilation
and loopback HTTP checks, not actual installed-server upgrade qualification.

Use the reviewed no-retry HTTP/1 transport profile in the legacy fixture:
disable connection reuse and redirects, keep the standard client and transport,
and make one call at a time. This supplies explicit caller configuration to
the unguarded older SDK; it does not claim every historical client prevents
transparent GET replay. For standard and assistant representations, require
one request after 409, stable body/header correlation and no page; a deliberate
second call has a distinct server ID. Close the actual first HTTP connection
to test a lost reply and require no transparent replay. An undeclared status
must still decode through Default without initiating a second request.

These fixtures add no dependency, model or dataset; existing Go BSD-3-Clause
and generator/runtime licence terms apply. Native execution, baseline/new SDK
generation and installed-server/database upgrade/rollback remain NOTRUN until
the coordinated execution window. Python source transformation checks alone
cannot qualify any of these Go paths.

### Qualified generated-client migration

The later fixed-window execution completes actual tagged tool construction,
all 28 Go/Python domains and the four frozen Go caller groups. The exact
e4bd3138 generator/ee7a214c fixture scope passes 23 root cases and 20 subcases,
with zero skips under the race detector. Both language outputs account for
all 430 operations. Compared with original 2e generation, 1,674 of 1,675 files
are identical and only the intended Go identity parser adaptation differs.
The default-only original 9e identity SDK is separately generated and actually
compiled for the legacy and rollback callers. This updates the earlier
Python-only qualification boundary; no test or output is reconstructed by hand.

The [SDK evidence](sdk.md#qualified-tool-generation-and-go-client-migration)
records source/tree IDs, exact output digests, native case counts and cleanup.
The explicit 409 review remains visible with the unchanged compatibility
report and exit 1. These SDK protocol results do not replace installed-server,
database, Python SDK runtime or complete final-source platform gates, and do
not select a supported-release baseline or complete DEV-204.
