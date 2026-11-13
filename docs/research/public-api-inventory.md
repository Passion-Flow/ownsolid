# Public API inventory

DEV-101 requires a complete public API. The
[OpenAPI 3.1 operation object](https://spec.openapis.org/oas/v3.1.1.html#operation-object)
uses operation IDs to identify operations for tools. The
[paths rules](https://spec.openapis.org/oas/v3.1.1.html#paths-object)
treat parameter-name variants of the same path shape as identical.
[Kubernetes API discovery](https://kubernetes.io/docs/concepts/overview/kubernetes-api/#discovery-api)
separates the operation inventory from the schemas clients use.

Inventory the local versioned contracts with the SDK's audience rules. Emit
the domain, audience, method, path and operation ID. Report missing public
operation IDs and competing declarations of the same method and path shape
in an audience. A path and method can legitimately serve different audiences.
Check duplicate IDs within each OpenAPI document, where the specification
requires uniqueness. Report summary and description presence as measurements,
since OpenAPI allows them to be omitted. A machine-readable report and a
nonzero exit on findings support a repeatable release check.

Runtime discovery would need an authoritative route registry covering all
entrances. Inferring coverage from handler source names or by hitting each
route cannot distinguish all permission refusals, service paths and disabled
features. This tool audits contract declarations. Completion still requires
the owning modules' authorization and end-to-end evidence, documented
operations and agreement with the running dispatchers.

Use the existing SDK projection and the Python standard library. The
[PSF license](https://docs.python.org/3/license.html) permits offline and
commercial use and redistribution under its terms. No dependency, model,
dataset or remote lookup is added.

Test private and unlisted exclusion, multi-audience collisions, parameter
renaming, per-document ID collisions, and missing IDs. Run the report on the
current repository in a network namespace with no external routes. The
route audit counts source declarations separately from compatibility
projections.

## Cloud inventory operation identities

The six Admin and Portal [cloud inventory routes](../../api/openapi/v1/cloud-inventory/openapi.json)
already serve retained observations, authorized import, and receipt lookup.
The [OpenAPI operation object](https://spec.openapis.org/oas/v3.1.1.html#operation-object)
gives each operation a unique identifier for client tooling. The pinned
[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) and
[openapi-python-client](https://github.com/openapi-generators/openapi-python-client)
already infer names for these routes. A scratch generation proved that
making those inferred names explicit produces byte-identical Go and
Python client sources. Shorter identifiers would change released client
symbols. Keep the wire parameters, body, status and route unchanged.
The API reference already uses method and path as the bookmark for these
operations. Use an [OpenAPI extension](https://spec.openapis.org/oas/v3.1.1.html#specification-extensions)
to preserve those six anchors independently of SDK names.

Use the current [SDK licenses](sdk.md#licenses). The metadata adds no
dependency, model or dataset. Keep all six explicit identifiers covered
by the offline inventory test. Confirm generated Web artifacts and
exercise the existing TLS and PostgreSQL authority, import and receipt
fixtures. The provider tests use loopback TLS protocol fixtures.

The disconnected Linux fixture passed both audiences' authorization,
retained reads, import and receipt replay, and rollback tests against real
PostgreSQL and the loopback AWS protocol fixture. The inventory no longer
reports missing cloud operation IDs. The existing reference summaries and
bookmark anchors are byte-identical.

## Node exporter operation aliases

The four node exporter path items in Nodes and Components were identical.
The registered [components handler](../../services/control/internal/modules/components/http/handler.go)
owns status, actions and logs. Give Components the canonical operations
and point the Nodes path items to the same paths in that contract.
[OpenAPI Path Item references](https://spec.openapis.org/oas/v3.1.1.html#path-item-object)
and [JSON Pointer fragments](https://www.rfc-editor.org/rfc/rfc6901#section-6)
describe the reference and escaping. The local authoring prototype expanded
the four items to the previous Nodes document and TypeScript bytes.

Preserve both SDK packages and the help downloads by materializing the
four references before projection. Nodes and Components keep their own
legacy local schema and security names, including their distinct Problem
schemas. A generic dereference to the Components schema would change the
Nodes SDK error type. Dropping the Nodes projection would remove existing
methods and help bookmarks. Limit the authoring resolver to same-path,
one-hop Nodes references to Components. Unknown, dangling or chained
references fail before generation. The audit retains the Nodes operation
identities for release comparison and counts each Components declaration
once for route collisions.

No library is added. The existing [SDK licenses](sdk.md#licenses), Node.js
MIT license and Go BSD-style license cover generation and fixture code.
Compare both pinned SDK outputs, TypeScript, help summaries and the
downloaded contracts with the previous versions. Verify the four real
HTTPS routes and their existing scope and refusal behavior.

The Go and Python Nodes and Components sources, generated TypeScript and
both help projections were byte-identical. The audit retained 421 public
operation identities, removed the four duplicate findings and reported no
compatibility changes. The raw Nodes document also resolved through
openapi-typescript in a disconnected namespace.

The Linux TLS fixture used PostgreSQL 18.6, VictoriaLogs 1.53.0 and both
digest-checked [Apache-2.0 node exporter archives](https://github.com/prometheus/node_exporter/blob/v1.12.1/LICENSE).
It verified Admin and Portal status, scoped maintenance and task-backed
journal download. The original fixtures predated the current Models
composition, scoped enrollment and external log store. A temporary
fixture used the current Nodes enrollment schema and the real log store
while retaining the four existing exporter response schemas.

## ARC-104 node enrollment profile

The [console handoff](cell-cluster-console.md#owner-handoffs-before-implementation)
identifies a contract gap for the existing Kubernetes node path.
The [Nodes token service](../../services/control/internal/modules/nodes/join_writes.go)
admits the explicit Kubernetes profile on PostgreSQL. The
[agent handshake](../../api/agent/v1/schema.json) describes protocol 1.0 and
its four ordered capabilities. [Enrollment](../../services/control/internal/modules/nodes/enrollment.go)
uses the effective peer IP, requires a usable unicast address and returns the
persisted profile through the existing token and node reads.

[OpenAPI 3.1](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
uses JSON Schema for the payload. Widen the existing profile and capability
enums, and add the disjoint machine handshake and enrollment alternatives
using the agent handshake. Retain every previous alternative, field and
optional read-only default. [AIP-180](https://google.aip.dev/180) distinguishes
wire and source compatibility. Callers sending existing profiles retain
their behavior. Generated clients that exhaustively match response enums
need to account for the Kubernetes value the server already returns.

An Admin-only request edit would still leave the returned node or token
unrepresentable. A separate operation would duplicate working authorization
and token semantics. The request does not accept a caller-supplied peer
address. The public node shape continues to follow the actual node response.
Kubernetes task and candidate eligibility projections are separate
contract work. No dependency, model, dataset or runtime fetch is introduced.

Verify the contract shape against the canonical agent handshake. Generate
the existing Web artifacts and check their consistency. Exercise the real
PostgreSQL enrollment path with the Kubernetes profile, including invalid
addresses and a successful persisted registration, and run the wire profile
tests. This qualifies the contract and enrollment, with RKE2 deployment
qualification owned by ARC-104.

The disconnected PostgreSQL 18.6 fixture drove the real Admin TLS handler
to mint a token and read it back. The machine handler refused a loopback
peer with HTTP 409 and `KUBERNETES_NODE_ADDRESS_INVALID`. The same token
then enrolled successfully over TLS when the fixture supplied a synthetic
canonical unicast peer at the server boundary. The persisted node and token
responses validated against the changed OpenAPI schemas. The ARC-104
service tests also passed for address rejection and server and worker
registration. The targeted agent-v1 tests passed for the handshake and
address. The fixture did not start an RKE2 installation.

`make generate`, `pnpm -C web generate:check` and the tooling tests passed.
The Console prerequisite landed in commit 7ae05f17. Its profile map
accepts the Kubernetes value and the English and Simplified Chinese locale
files define its label before the generated response enum expands. See the
[Console verification](nodes-page.md#kubernetes-execution-profile-dev-101-arc-104).

## MOD-103 and DAT-106 Admin model detail

The [retained lineage handoff](training-dataset-lineage.md#dev-contract-handoff)
defines the two source references and the server's existing integrity boundary.
Publish the optional field only on the successful Admin detail operation.
Leave the published manifest, scope, status codes, lists and Portal reads
under their existing contracts.

[OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
uses JSON Schema 2020-12. Its
[object applicators](https://json-schema.org/draft/2020-12/json-schema-core#name-applicators)
evaluate `additionalProperties` in the schema that declares it. Combining the
existing closed ModelVersion with an extra property under `allOf` would
reject enriched data. Use a separate closed Admin detail object with the
same base fields and manifest conditions. A tooling test compares those
fields so the two schemas cannot drift. Reusing a newly factored open schema
would also change the existing generated types. [AIP-180](https://google.aip.dev/180)
distinguishes additive wire changes from generated source types. Existing
SDKs that tolerate unknown response fields can read the enriched result.
A consumer validating against the former closed Admin detail schema needs
the updated schema before accepting an enriched response.

Require both retained version references when lineage is present. Each is a
closed object with canonical lowercase UUIDs and a lowercase SHA-256 digest.
Reject null, incomplete pairs, unexpected members, noncanonical IDs and
digests, and lineage on an approved base model. Keep omission valid for
previously published metadata. A corrupt derived graph keeps the existing
conflict error. The current [SDK license inventory](sdk.md#licenses)
applies. This change adds no dependency, model, dataset or network access.

Verify the SDK projection, regenerated Web artifacts, and the runtime response
against the exact Admin schema. In the disconnected TLS and PostgreSQL fixture,
check the retained training and holdout tuples, a base response without
lineage, scope and conflict refusals, and the unchanged list and Portal schemas.

The joined Linux amd64 fixture checked trusted HTTPS and PostgreSQL with
only loopback. Retiring the training dataset version preserved both source triples.
The Admin response passed its published schema. The same fixture checked
the original shape for base and Portal reads and both lists, scope refusals,
and the existing conflict error on corrupt metadata. The schema rejected
null and incomplete lineage, extra fields, noncanonical identifiers and
digests, and lineage on an approved base. Older generated Go and Python
clients decoded the captured Admin response with the original fields. The
new clients generated with the pinned tools also decoded both retained
references and accepted responses with the property omitted.

## IAM-111 current account browser sessions

The [identity handoff](session-management.md#verified-behavior-and-integration-handoff)
defines the Admin and Portal paths and retention behavior.
[GitLab Active Sessions](https://docs.gitlab.com/user/profile/active_sessions/)
allows people to inspect and end browser sessions. The
[OpenAPI security requirement](https://spec.openapis.org/oas/v3.1.1.html#security-requirement-object)
can express the Admin or Portal cookie as separate alternatives. Reuse
those existing schemes and the server's Origin and CSRF checks. Each
entrance works only with its authenticated account and live sessions.

Use the current session route for expiry status and the existing logout route
for ordinary sign-out. Add a distinct paginated list and a
[bodyless DELETE](https://www.rfc-editor.org/rfc/rfc9110.html#name-delete)
for ending an owned session. A later retry against an already ended session
returns 204 for a still-authenticated caller. Ending the caller's current
session expires both identity cookies for that entrance. A foreign or unknown
UUID yields the same 404. Device labels and raw client identifiers would
exceed the retained metadata. The contract exposes only the six fields
projected by the handler. The existing [SDK licenses](sdk.md#licenses) apply,
with no additional dependency, model or dataset.

Validate the response and request headers on a real loopback HTTPS fixture
backed by disconnected PostgreSQL. Exercise both audiences, account-scoped
cursors, background reads, cookie replay, Origin and CSRF refusal, no response
body on DELETE, current-session cookie cleanup, revocation without a license
and one durable audit on concurrent retries. Check that existing identity
operations and their generated symbols retain the same shapes.

The disconnected Linux HTTPS fixture validated live pages against the
published list response. Both entrances rejected foreign cursors and
sessions. Concurrent revocation and a retry returned bodyless 204 responses
and produced one audit record. Current-session cookie cleanup, an unlicensed
revocation, session status and the Admin audit regression also passed.

The pinned Python generator rejects a string literal in a header parameter.
Represent the existing audit actor header's single accepted value with an
equivalent one-value enum. The server behavior and generated Go client
are unchanged. The pinned Python generator now retains the audit operation
along with the two browser session operations.

## DAT-105 retained three-way dataset reads

The [split handoff](dataset-three-way-splits.md#verification-and-devweb-handoff)
describes the retained schema 2 manifest. Hugging Face
[DatasetDict](https://huggingface.co/docs/datasets/package_reference/main_classes#datasets.DatasetDict)
keeps split names explicit. The current dataset version handlers already
serialize the retained manifest and serve the sealed test asset when it
exists. The public publication handler still uses the two-way default.
Describe the existing version detail, list and download behavior while the
data owner prepares a separately verified public write path.

Keep the existing named manifest and asset types and preserve all required
schema 1 fields. Widen the two discriminator enums and use
[JSON Schema conditions](https://json-schema.org/understanding-json-schema/reference/conditionals#if-then-else)
for the matching split algorithm, test fields and file inventory. The schema
2 file list must contain each of the six asset names once, with a nonempty
test file. The retained writer verifies aggregate counts and ratio arithmetic.
The raw manifest download keeps its existing binary response contract.
A separate endpoint would duplicate authorization and retained file checks.
A union wrapper in generated Go would replace existing field access.
The existing [SDK licenses](sdk.md#licenses) apply. This change adds no
library, model or data.

Check both Admin and Portal over trusted loopback TLS and real PostgreSQL.
Publish the original default through HTTP and prepare a three-way version
through the current owning service with the installed Python processor.
Verify version list and detail, the test asset digest, access scope, and
rejection of a test download from a schema 1 version. Verify that old clients
still decode schema 1 and newly generated types decode both. Existing strict
schema 1 clients need the updated contract to read schema 2.

The Linux test used a disposable PostgreSQL instance in a network namespace
with only loopback, the production TLS dispatcher and the installed Python
processor. Admin and Portal detail and mixed-version list responses validated
against the public schemas. Both test downloads matched the sealed digest.
A schema 1 test download returned HTTP 409 and a foreign workspace could
not read schema 2. The public write handler rejected a test split option.
Malformed schema 2 responses were rejected for missing counts, a mismatched
algorithm, repeated asset names or a zero-size test asset. Both pinned SDK
generators decoded the captured schema 1 response before and after the
change, and the updated Go and Python clients decoded schema 2.

## DAT-105 three-way publication request

The existing [three-way split policy](dataset-three-way-splits.md) targets
connected source groups and keeps the default two-way manifest. Google's
[compatibility guidance](https://google.aip.dev/180#adding-components)
keeps added request fields optional and preserves the prior behavior for
omission. Its [request identification guidance](https://google.aip.dev/155)
uses a stable identifier to replay a write after an uncertain response.
Apply those properties to the existing Admin and Portal publication routes
and their existing Idempotency-Key header.

Add one optional integer to the closed request shape. Omission keeps the
original request, fingerprint, and manifest. A test ratio from 100 through
5,000 basis points opts in to a test split. The sum with the validation
ratio must be at most 9,000. Document the aggregate limit and leave its
enforcement to the shared split validator. A separate operation would
duplicate workspace authorization, draft locking and replay semantics.
A nullable field or a serialized default would hide a caller's explicit
selection error. Keep the existing job and retry bodies and read the
effective ratio from the sealed manifest. The existing [SDK licenses](sdk.md#licenses)
and [processor and fixture licenses](dataset-three-way-splits.md) apply.
No dependency, model or dataset is added.

Use the data owner's disconnected TLS and PostgreSQL fixture on the same
tree as the public contract. Exercise omission, malformed and out-of-range
values, the combined limit, a conflicting replay and a foreign workspace.
Drive a retryable worker failure and the existing retry endpoint, then
verify the completed three-way manifest and sealed test download. An
insufficient group count must be reported on the queued job without a
published version. Confirm that pinned client generators omit the option
for existing callers and encode it as an integer when set.

The joint Linux run used the [dataset TLS integration fixture](../../services/control/internal/modules/datasets/http_integration_test.go),
loopback-only PostgreSQL 18.6 and the installed CPython processor. Both
accepted request bodies validated against the updated schema. Omission
published schema 1. Invalid values returned HTTP 400, the 9,000-basis-point
boundary was accepted, and replay with a different valid ratio returned
HTTP 409. A recoverable processor failure and the HTTP retry preserved
the selected ratio and produced three sealed nonempty files. The existing
processor tests also verified retry, snapshot restore and insufficient
groups. With a second workspace created over TLS, publication and retry
requests carrying the original dataset ID both returned HTTP 404. The
pinned Go and Python generators kept the legacy request body when the
option was absent and encoded the opt-in value as an integer.

## DAT-104 near-prompt publication request

The [retained near-prompt policy](dataset-near-duplicates.md#initial-policy)
uses a bounded, exact comparison of candidate shingles. DataTrove's
[MinHash implementation](https://github.com/huggingface/datatrove/blob/main/src/datatrove/pipeline/dedup/minhash.py)
illustrates a candidate-based approach. The retained policy avoids a
probabilistic cutoff and has separate safeguards for grouping and removal.
[OpenAPI 3.1](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
can express the one public policy value without changing the request's
required fields. [AIP-180](https://google.aip.dev/180#adding-components)
preserves behavior for callers that omit a new option.

Expose one optional string enum, near_prompt_v1, through the existing
publication operations. Omission keeps exact duplicate removal and the
existing fingerprint. Three-way splitting still groups matching prompts
before allocation, even with exact removal. An explicit empty string
would be indistinguishable from omission at the service boundary, so
validate it before building the internal options. An open string or an
explicit exact value would admit selections the handler does not accept.
The existing retry request keeps its reason-only shape and inherits the
frozen policy. The bounded comparison failure belongs to the job result.
The existing [SDK licenses](sdk.md#licenses) and
[policy dependencies and fixture licenses](dataset-near-duplicates.md)
apply. No dependency, model or dataset is added.

Verify default exact removal and the opt-in policy, including selection
with a test split. Use the DAT-105 TLS fixture with real PostgreSQL and
CPython. Compare retained and removed rows and sealed duplicates, test
invalid policy values, replay and conflict, and verify a failed attempt
and retry keep the frozen choice. Check that the generated clients leave
the field absent by default and can encode only the documented selection
through their declared types.

## DAT-102 single-turn source attachment

The [single-turn row policy](dataset-training-formats.md#published-single-turn-messages)
uses the textual role and content sequence described by
[Hugging Face TRL](https://huggingface.co/docs/trl/dataset_formats#language-modeling).
The [OpenAPI parameter contract](https://spec.openapis.org/oas/v3.1.1.html#parameter-object)
describes request headers separately from the uploaded bytes.
[RFC 9110 field handling](https://www.rfc-editor.org/rfc/rfc9110.html#section-5.2)
distinguishes singleton fields from list-valued fields.

Add one optional singleton Dataset-Source-Row-Format header to the two
existing attachments. Its only explicit value is single_turn_messages_v1.
Omission preserves the old prompt/completion decoder. Keep the bounded
NDJSON body, byte count, digest and all response shapes. A JSON wrapper
would change digest identity and streaming behavior, and inference from
row contents would reinterpret older uploads. Store the interpretation
with the import job so repeated uploads and explicit retries cannot
change it. The existing [processor licenses](dataset-training-formats.md#published-single-turn-messages)
and [SDK licenses](sdk.md#licenses) apply. No dependency, model or dataset
is added.

Verify omitted and explicit values in the generated clients and check that
the binary payload is identical to its input. Run the shared disconnected
HTTPS and PostgreSQL fixture against the actual handler, processor and
contract. Check malformed and repeated headers, an identical replay, a
changed-format conflict, a retryable processor failure, physical line
errors, and canonical publication with the raw source digest preserved.

## APP-109 Portal conversation search

The [search handoff](application-conversation-search.md#acceptance-and-handoff)
and registered Portal handler already accept q. Open WebUI's
[history search](https://docs.openwebui.com/features/chat-conversations/chat-features/history-search/)
matches content within the caller's history. The ownsolid query matches the
application name and completed-turn question or plain-text answer as a
literal substring. It returns the existing caller-private metadata.
[OpenAPI parameters](https://spec.openapis.org/oas/v3.1.1.html#parameter-object)
describe the bounded optional string. [AIP-158](https://google.aip.dev/158)
keeps page tokens opaque and binds continuation to the same query.

Document one optional q with a 120-character maximum and preserve the
existing operation, authorization, metadata response and unfiltered cursor.
Search cursors bind the exact term, workspace, owner and login session.
The service rejects invalid or repeated terms and keeps the existing
availability refusal for search outside PostgreSQL. A separate search route
would duplicate current permission and pagination behavior. The existing
[SDK licenses](sdk.md#licenses) and [service dependencies](application-conversation-search.md#continuation)
apply. No dependency, model or dataset is added.

Exercise the existing disconnected Portal TLS and PostgreSQL fixture for
literal wildcard and Unicode matching, private scope, invalid input,
cursor substitution and legacy pagination. Verify both Go and Python
request builders omit q by default and encode an explicit term unchanged.

The disconnected Linux fixture exercised the real Portal dispatcher and
PostgreSQL 18.6. Success and refusal bodies validated against the OpenAPI.
The fixture covered wildcard and Unicode matches, caller and workspace
privacy, search cursor replay across sessions, invalid terms and unchanged
legacy pagination. The pinned Go and Python generators preserved existing
request construction and encoded an explicit Unicode and wildcard term.

## Portal application session references

The [APP handoff](app-lane-questions.md#assistant-catalog-handoff-for-agt-103)
identifies two application reads that reference an undeclared portalCookie
scheme. The [OpenAPI Security Requirement Object](https://spec.openapis.org/oas/v3.1.1.html#security-requirement-object)
requires a declared scheme name. Both real routes use the existing Portal
session cookie. Point their requirements to the declared PortalSession
scheme. Keeping the undefined name or adding a second scheme would leave
two names for one security boundary. No runtime authentication or operation
identity changes. The existing [SDK licenses](sdk.md#licenses) apply.

A pinned generation prototype produced byte-identical Go and Python source.
Verify both real Portal routes reject a request without the cookie and
return their existing response with a valid session. The existing
assistant tool catalog should stay byte-identical.

The disconnected TLS and PostgreSQL fixture returned HTTP 401 without
the cookie and schema-valid HTTP 200 responses with the cookie on both
routes. The assistant catalog generator matched its committed output.

## Admin audit action filters

The registered Admin audit list accepts exact action values from the
[identity query catalogue](../../services/control/internal/modules/identity/lists.go).
Its request enum omitted two values that already have production writers.
The [mail resend operation](../../services/control/internal/modules/identity/mail_resend.go)
commits mail.resent and the [node pacing operation](../../services/control/internal/modules/nodes/pacing.go)
commits node.pacing_updated.

Keep the input enum and extend it by those two accepted values. The
[OpenAPI Parameter Object](https://spec.openapis.org/oas/v3.1.1.html#parameter-object)
describes the schema at the query boundary. [AIP-180](https://google.aip.dev/180#adding-components)
allows new request enum values while preserving existing callers. A free
string would advertise values the server rejects. Leave the response's
action string, route, operation ID, authorization, and cursor untouched.

Verify the request enum matches the real allowlist, the two writers create
events that the registered Admin HTTPS list can retrieve by exact action,
and a near miss returns VALIDATION_FAILED. Regenerate clients and verify
that both new values survive request encoding while old public operation
identities remain. The existing [SDK licenses](sdk.md#licenses) and
[Admin audit fixture licenses](admin-audit-cli.md) apply. No dependency,
model or dataset is added.

The Linux amd64 loopback TLS and PostgreSQL 18.6 test resent a failed
invitation email and changed pacing through their registered Admin writes.
Both exact filters returned their single schema-valid event. A misspelled
action returned VALIDATION_FAILED. The SDK projection retained the complete
server allowlist, and the pinned Go and Python builders sent both added
values and an existing value unchanged.

## Training collection cursor bounds

[OpenAPI query parameters](https://spec.openapis.org/oas/v3.1.1.html#parameter-object)
describe the input limits clients can rely on. The
[JSON Schema string limit](https://json-schema.org/draft/2020-12/json-schema-validation#name-maxlength)
is expressed in characters. The ownsolid cursors are opaque URL-safe
base64, so the character and byte limits agree for valid tokens.
[AIP-158](https://google.aip.dev/158#opacity) recommends replaying a
returned token with the same query without interpreting its contents.

The Admin and Portal training job, attempt, checkpoint and recovery
lists call the [shared paginator](../../services/control/internal/platform/pagination/page.go),
which rejects cursors longer than 512 bytes. The metrics list uses a
[separate sequence cursor](../../services/control/internal/modules/training/metrics.go)
with a 2048-byte limit. The published contract declared 2048 for all ten
operations. Use the existing per-operation schema to match those two
bounds. Increasing the server limit would expand what it processes and
removing the limit would advertise unsupported input. Preserve the
operation identities, wire format, response schemas and handling of
previously issued cursors. The [SDK licences](sdk.md#licenses) apply.
No dependency, model or data set is added.

Verify both entrances against the actual HTTPS dispatcher with PostgreSQL.
Check same-scope cursor replay and the server's INVALID_CURSOR problem
at each bound. Confirm the eight list operations document 512 and both
metrics operations retain 2048. Regenerate Web API and Help and validate
that pinned Go and Python clients still emit server-issued cursors
unchanged.

The Linux amd64 loopback TLS fixture passed all ten Admin and Portal
operations against PostgreSQL 18.6. Each endpoint accepted an in-scope
cursor at its limit and returned schema-valid INVALID_CURSOR for the
first larger base64 encoding. The metric fixture padded a real returned
token for each entrance and still read the correct second metric.
Pinned generation preserved the four Go attempt and metric requests and
all ten Python collection requests. The public inventory reported no
changed operation identities.

## GW-104 alias capability contract preparation

The gateway owner's local candidate 65ed7c87 provides a scoped internal
projection. Its follow-up 42efece0 verifies that the four public reads
still use the released shape. The [LiteLLM model management API](https://docs.litellm.ai/docs/proxy/ui_store_model_db_setting)
distinguishes informational model metadata from enabled endpoints. The
[OpenAI model object](https://developers.openai.com/api/reference/resources/models)
has its own compatible shape. Keep ownsolid capability evidence on
the versioned Admin and Portal alias reads. Use the
[OpenAPI 3.1 Schema Object](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
to constrain its presence and fields.

| Candidate read contract | Value |
| --- | --- |
| Operations | adminListModelAlias, adminGetModelAlias, portalListModelAlias, portalGetModelAlias |
| capabilities | Optional closed object. When present, it contains both fields below |
| text_generation_verified | Boolean true, backed by the exact ready target's generation verification |
| context_token_ceiling | Positive integer. The minimum of the approved model, serving recipe and gateway policy ceilings. The live tokenizer can enforce a smaller limit |
| Missing evidence | Omit capabilities entirely for a visible alias with no exact verified target. This includes legacy snapshots, stale or mismatched revisions, unsupported recipes and unavailable targets |
| Paused alias | Admin keeps the existing visible alias with capabilities omitted. Portal keeps its current list and detail visibility, including MODEL_ALIAS_NOT_FOUND for detail |
| Scope refusal | Keep the current workspace, tenant, cell and session authorization and the existing public problem codes |

Publish only after the gateway owner lands list and detail response
composition for both audiences. Require each alias and its optional
projection to share the same authorized transaction and exact alias
and deployment revisions. Keep the compatible model list, machine
bindings and mutation responses on their existing shapes. The current
ModelAlias component is shared by reads and writes, so verify the four
operations' actual response boundaries before choosing the published
component binding. A new endpoint would duplicate alias discovery and
require separate authorization. Deriving flags from the model family
would bypass verified target evidence. Keep tools, pooling, rerank and
multimodal claims outside this projection.

After the backend lands, validate mixed list pages and detail for a
verified target, missing evidence and a retarget. Exercise paused,
foreign-workspace and foreign-tenant refusals over disconnected HTTPS
and PostgreSQL. Keep the existing operation identities and cursor
scope. Verify the generated Web, Help, Go and Python projections for
both presence and omission, and check that machine and mutation
responses retain their fields. The [SDK licences](sdk.md#licenses)
apply. This preparation adds no dependency, model or data set.

GW-111 and DAT-107 retain their exact released error codes. The Help
error index can add the remaining entries after the Web owner lands
both English and Simplified Chinese title and detail text.

## MOD-121 abandoned import API contract preparation

Main at e97a4fb4 introduced an opt-in PostgreSQL expiry worker for one
workspace. The model owner's HTTPS candidate 0cd30e89 exercises it
with the workspace policy set by the fixture. Installation startup
does not yet configure this policy. See the
[worker design and recovery coverage](model-import-cleanup.md#mod-121-abandoned-upload-retention).
The [tus expiration extension](https://tus.io/protocols/resumable-upload#expiration)
treats expired uploads as requiring a fresh upload. The
[HTTP 409 definition](https://www.rfc-editor.org/rfc/rfc9110.html#name-409-conflict)
covers writes that conflict with the current resource state. Use the
existing ownsolid upload operations and request keys. Adding a discovery
endpoint would duplicate authorization and lookup, and copying another
protocol's refusal would break this API's established conflict contract.

The canonical base path is
`/api/v1/sites/{site_id}/cells/{cell_id}/admin/model-imports`. Keep the
platform Admin session and the existing workspace, tenant and cell
checks. The API already has a nullable `ModelImport.error_code` string.

| Operation | Verified result after terminal expiry |
| --- | --- |
| listModelImports, getModelImport, resolveModelImportIntent | HTTP 200. The authorized record is `cancelled` with `error_code: MODEL_IMPORT_EXPIRED`. Its ID and accepted byte count persist, `model_id` is null, and staging has been removed. |
| startModelImport | An exact replay with the original key returns HTTP 201 and the original terminal record. A new key starts a new upload. |
| cancelModelImport | A repeat cancellation returns HTTP 200 and the same terminal record. An ordinary user cancellation retains a null error code across replays. |
| uploadModelImportChunk, queueModelImport | Valid requests against the terminal import return HTTP 409 and the existing Problem code `MODEL_IMPORT_STATE_CONFLICT`. |
| resolveModelImportIntent outside the key's workspace | An authorized lookup in another workspace returns HTTP 200 with `status: absent` and a null import. A foreign-tenant lookup or detail request returns HTTP 404 without import metadata. Portal list, detail and resolve also return HTTP 404. |

The candidate HTTPS fixture passed on Linux amd64 with a real
PostgreSQL 18.6 database and loopback-only networking. It ran unchanged
as a Go test overlay against 836c8048. It covered exact start and
cancel replays, the two schema-valid 409 responses, audit counts and
the cross-scope reads above. The fixture used synthetic upload bytes
and explicitly enabled the worker. The [SDK licences](sdk.md#licenses)
apply. This preparation adds no dependency, model or data set.

Keep the released OpenAPI, SDKs and Help on the current contract. The
terminal reason is data on a successful import response. The shared
HTTP Problem registry has no entry for that terminal reason. Publish
a dedicated terminal expiry
conflict only after both actual authorized write paths produce it
and the shared registry maps it to HTTP 409. Recheck that the
in-progress `MODEL_IMPORT_EXPIRY_PENDING` marker and ordinary
cancellation do not produce the terminal expiry Problem. Require
schema-valid `application/problem+json` from both operations with
otherwise valid requests. Preserve the successful replays, refusal
for missing CSRF, workspace and tenant isolation, and Portal's
absence of import metadata. Verify the new error against its two
operation declarations and the generated projections. The installed
operator flow also depends on startup policy wiring and the Console
expiry state.

## S-108 Help parameter references and cutover errors

The [OpenAPI Parameter Object](https://spec.openapis.org/oas/v3.1.1.html#parameter-object)
allows parameter entries to reference reusable definitions. Resolve local
component parameter references before applying Help's header and cookie
filter. Follow component aliases and JSON Pointer escaping, and reject
missing, cyclic or external parameter references with a source diagnostic.
Silently treating a reference as a parameter loses its name and location.
Removing the shared CSRF definitions would duplicate the identity contract.

The registered adminCutoverAlias write shares planCutover with
adminPreviewAliasCutover. Publish its actual conflict reasons together
with authentication, request validation, idempotency and storage refusals.
Keep HTTP statuses aligned with the shared Problem registry, and retain
the existing operation ID, successful response and request shape.
The [Problem Details format](https://www.rfc-editor.org/rfc/rfc9457.html)
and the [SDK licences](sdk.md#licenses) apply. No dependency, model or
data set is added.

Verify reference chains, escaped names, path and operation parameter
references, hidden header and cookie references, and clear failure for
missing or cyclic references. Regenerate the actual identity Help for
both entrances and require every visible parameter to have its name and
location. Confirm the Admin cutover Help preserves the declared statuses,
includes the five cutover conflicts, and stays absent from Portal.
Exercise the existing real TLS and PostgreSQL cutover flow offline.

The Linux amd64 disconnected check and build passed. The three existing
PostgreSQL cutover tests passed through the actual TLS dispatcher or its
underlying persisted flow. They covered CSRF, nullable fields, same-key
replay, foreign scope, stale revisions and audit rollback. The Help
generator tests reproduced the missing-name rows before regeneration,
then passed for both entrances, including reusable path and pagination
parameters. Three newly visible parameter descriptions have Simplified
Chinese translations in the existing Help description catalog.

## OBS-109 fleet history publication boundary

The [bounded history design](telemetry-retention-tiers.md) defines the
Admin operation adminFleetHistory at
`/api/v1/sites/{site_id}/cells/{cell_id}/admin/monitoring/fleet-history`.
It accepts workspace_id, from and to. Workspace identifiers are canonical
nonzero UUIDs. Timestamps are UTC seconds ending in Z, with from strictly
before to. The encoded query has a 512-byte bound. Session, tenant,
workspace and cell authority come from the original dispatcher and the
service's current authorization checks.

The service selects the raw or rollup store and evaluation grid. The
response contains tier, step_seconds, evaluated from/to and sparse cells
with site_id, cell_id and ordered at, observed and reachable points.
Missing observations stay absent and measured zero reachable counts stay
present. Empty cells are a successful result. A range with no valid
evaluation point returns a range error and can require adjusting its
duration. Each cell has at most 1440 points. The response uses unsigned
64-bit counts and preserves that type in the Go client. The complete
data envelope and trailing newline fit within 1 MiB before the authority
recheck and successful audit.

The [OpenAPI schema rules](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
and [generator type extensions](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.2/README.md)
define the existing projection approach. The
[SDK licences](sdk.md#licenses) cover the pinned generators and clients.
No dependency, model or data set is added. A separate discovery endpoint
would duplicate authorization and lookup. The existing monitoring
operation family owns this read.

Keep strict request timestamps as patterned strings in SDK inputs.
Python's [datetime.isoformat](https://docs.python.org/3.12/library/datetime.html#datetime.datetime.isoformat)
uses a numeric UTC offset, so a date-time query type can produce +00:00,
which the handler refuses. String inputs preserve the canonical value.
Response timestamps retain the date-time format. Both generated request
builders preserved the three query values and both models retained the
maximum unsigned count and measured zero in the offline projection check.

The public Problem registry maps range refusal to 400, response size
refusal to 422 and the four missing or unavailable history/store states
to 503. The reader chooses its tier before I/O and preserves a failed
long-tier read's refusal. Unknown native text is sanitized by the existing
Problem writer. The [reviewed recovery copy](in-product-help.md#monitoring-and-native-transaction-recovery-copy-obs-109)
explains all 28 declared codes in both languages.

### Native transaction refusals

Both monitoring reads preserve STORAGE_TIMEOUT, STORAGE_CANCELLED,
STORAGE_COMMIT_ROLLED_BACK, STORAGE_DEADLOCK and STORAGE_LOCK_TIMEOUT.
The public registry maps these existing classifications to HTTP 503.
The earlier Monitoring Problem enum omitted all five and the generated
Python model rejected their responses with ValueError. Preserve the
service's transaction contract and declare the five codes in the shared
schema and history operation. The public SDK projection regression
protects their presence.

Pinned offline generation produced all 28 public domain clients in each
language and all generated Go packages compiled. The standalone Go/Python
wire checks passed. Controlled HTTPX responses exercised all five codes
through the published monitoring read and the generated history read.
These client checks have their own scope. The PostgreSQL and native-store
acceptance exercises the normal cookie, CSRF, TLS and cell dispatcher,
current authorization during I/O, request-bound success audits, the
complete response limit, retry and private-store certificate replacement.

The reusable response components and six history Problem mappings were
staged before the operation. Publication combines the monitoring owner's
exact Owns/Handle dispatch with the operation and reviewed locale files.
The existing protected monitoring policy supplies store origins and
retention settings. Installed retention delivery, trust distribution and
the Console history view remain with their owning modules.

## Identity audit-read commit outcome (DEV-101 / AUD-108)

Reviewed 2026-11-13 against identity runtime fcbb839a, its qualification in
[identity audit-read access](identity-audit-read-access.md#qualification), and
current main 8b17a295. The actual GET /api/v1/admin/audit-events reads its
bounded page and appends audit.events_read in the same transaction. The shared
Problem registry already maps STORAGE_COMMIT_OUTCOME_UNKNOWN to HTTP 409 and
the Identity Problem enum already includes it. The operation lacks its explicit
409 response and x-error-codes entry; reuse that existing Problem without a
new error code, endpoint, receipt store or response field.

[RFC 9110 safe methods](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.1)
allows incidental request logging, while its
[retry rules](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2)
do not establish whether this particular access audit committed. A 409 or lost
reply must not produce successful data or an automatic second access append.
A deliberate later read is a new audited operation, not a replay or a rollback
claim. Keep the same filters, cursor and representation when retrying. Preserve
the earlier request ID for diagnosis rather than inventing an idempotency key.
Read the page before its access event is appended, so the page cannot contain
that same read's event.

The current server auditActions whitelist does not include audit.events_read.
Do not add it to the exact-action OpenAPI enum. category=audit already selects
the family. The IAM owner must first admit and qualify the exact action before
DEV can publish that enum value. Keep the thirteen standard response fields and
the existing optional verified assistant pair unchanged.

Add a per-operation cursor description so the current Help parameter projection
can carry the recovery semantics. Operation descriptions are retained in the
served OpenAPI but are not shown in today's Help operation summary. Web owns
the required Simplified Chinese description and generated Web/Help files;
prepare their exact inputs without editing that lane's source or locales.

[Go Transport](https://pkg.go.dev/net/http#Transport) can transparently replay
an eligible GET after a network error on a reused connection. The generated
Go dispatch now uses an isolated single-use transport for this audited read;
callers must disable any application/proxy retry policy. Python callers must also keep transport retries
disabled. Merely calling GET does not establish one access append. Controlled
409 codec tests prove one generated call and explicit retry; they do not
qualify arbitrary caller transports, real commit faults or installed services.

No dependency, model or dataset is added. The existing SDK generator/runtime
licences in [sdk.md](sdk.md#licenses) permit commercial, redistribution and
offline use with their existing notices; Go is BSD-3-Clause and HTTPX is BSD-3-Clause.
Standards and upstream documentation are design references, not embedded code.

Verify the explicit 409 mapping in the raw and projected contract, the unchanged
Problem/standard fields and assistant pair, exact-action parity with the actual
server whitelist, Help's recovery description and error metadata, and generated
Go/Python 409 decoding without automatic retry. Explicit re-read must be a new
call with the original selection. Retain the IAM owner's actual PostgreSQL and
wire-fault qualification separately; none is rerun or inferred from codec tests.
Public generation and whole-repository gates remain required before publication.

The explicit 409 response creates a status-specific Go Problem accessor after
SDK regeneration. Existing wire clients still receive the same Problem shape,
but regenerated Go consumers must use GetApplicationproblemJSON409 rather than
assuming every unspecified error uses the default accessor. Python retains its
existing Problem model. Keep this source-review distinction visible; declaring
the already-existing runtime status does not prove every consumer is unchanged.

Ten lightweight Linux contract regressions passed in a network namespace
without external networking: five audited-read cases, the existing assistant
projection case and four existing browser-session cases. Pinned offline
identity generation at metadata-only input 2426699b preserved all 25 public
operations and produced 121 Go and Python files. Six controlled Python sync/async wire cases passed, including
explicit 409 decoding, a new deliberate read after 409, unchanged selection,
transport failure and permission refusal without an automatic extra call.

A private filesystem projection, using the actual generators and cached pinned
Node dependencies, produced all 29 Web contract types and 144 Help files. Both
generator --check commands passed there. Only five Web paths differ from the
baseline: Identity types, the Admin Help summary and raw contract, and the source
and generated Chinese description catalogues. Admin Help retains the 409 and
recovery description; Portal excludes the Admin operation. Product Web files
were not edited. The Web owner must apply that relay and run product generation
before this contract candidate can be published.

The source comparison reports no operation-identity or closed-object changes.
Its single named-response review signal is the added explicit 409 binding.
All component definitions are unchanged. The metadata-only input's four private
Go wire cases were not compiled: their nonblocking Linux admission exited 75.
The SDK correction below supersedes that fixture with ten canonical protocol
groups, now qualified on native Linux amd64 as described below. Whole repository
gates and actual installed qualification remain separate; the CLI b967d7c8 gate
is unchanged.

### Browser consumer retry boundary

Static review of main 726f2149 found a separate Web consumer gap, since fixed
by the Web owner in e9186ade with the complete gate recorded at 124520b9.
The shared [query client](../../web/packages/shell/src/session.ts) retries
ApiProblems while failures is below two and retryable is true. The
[problem classifier](../../web/packages/api/src/problem.ts) makes status-zero
network and timeout failures retryable, and the
[transport](../../web/packages/api/src/transport.ts) produces those failures
when a reply is lost. Both the [audit page](../../web/apps/console/src/audit-page.tsx)
and [resource activity](../../web/apps/console/src/resource-page.tsx) use
listAuditEvents without a local retry override. This path can issue additional
audited reads automatically after an uncertain reply. A 409 itself is outside
the current retryable status set. No browser or SQL reproduction is claimed
for this static review.

The owner now scopes explicit retry and refetch behavior to both audit consumers,
including initial and continuation recovery, uncertain replies, denied caches,
reader/scope separation, focus, reconnect, invalidation and remount. Its built-in
Help article explains the same recovery in both languages. The complete Web gate
at c70e9f59 passed 1,191 Console and 410 Portal cases, with 28 and 10 existing
conditional skips. The failing old six-request controls and corrected no-data
invalidation failure remain separate evidence. See the owner's
[explicit recovery and complete gate](identity-audit-read-access.md#explicit-web-read-recovery).
These are owner-qualified scripted Web results, not a DEV installed or SQL run.
The generated five-file public metadata relay remains a separate required change;
the earlier two-consumer prototype patch is superseded and must not be applied.

### Generated Go transport correction

The standard shared Go transport can retry an eligible GET on a connection
that was used before. Documentation alone leaves that SDK-owned default unsafe
for an audited read whose reply was lost. Use the pinned generator's existing
postprocessing boundary for only Identity listAuditEvents at its existing path.
Emit a small private method with the generated Identity package. It clones a
standard http.Transport for one HTTP/1 call, disables connection reuse, refuses
redirects and retains the configured TLS trust/client certificate, cookie jar
and timeout. It does not catch a failed call or issue a second request.

[Go 1.27.3 Transport.Clone](https://pkg.go.dev/net/http@go1.27.3#Transport.Clone)
keeps configuration separate from connection reuse; the
[Client redirect contract](https://pkg.go.dev/net/http@go1.27.3#Client)
allows the original redirect response to be returned without following it.
Require an ordinary http.Client with a standard transport for this operation;
a custom request executor or transport can hide its own retries, so reject it
before dispatch with an actionable configuration error. Other operations retain
their configured request executor. HTTP/1 admission is explicit for this read.
No TLS verification, actor, scope or audit policy becomes optional.

Reject generated output that no longer has the pinned method/dispatch shape,
and reject an unnoticed operation rename, route move or domain move. A general retry
framework, empty-body tricks and mutating a shared transport are not selected.
Pinned [HTTPX 0.28.1](https://github.com/encode/httpx/blob/0.28.1/httpx/_transports/default.py)
defaults to zero connection retries; its generated Python
operation has no failure/retry loop. Keep that behavior and test both sync and
async calls, including a lost reply and an explicit second read.

Tests must cover the actual generated Go operation with a primed shared-client
control, lost response, explicit 409, explicit retry, preserved TLS/cookie trust,
redirect refusal, unsupported request executors and concurrent independent calls.
Within those ten protocol test groups, retain consecutive lost replies across
separate deliberate calls and caller cancellation before and during dispatch.
[Go request context](https://pkg.go.dev/net/http@go1.27.3#Request.WithContext)
controls connection establishment, sending and response reads. Check cancellation
with the generated operation and an actual waiting HTTP handler, then show that
only a new uncancelled call can dispatch again. Cancellation or timeout cannot
establish whether an earlier server access was recorded.
Use the existing SDK wire runner; no new service or qualification framework is
needed. Generator unit/projection checks can run independently of Go compilation.
No dependency, model, dataset or third-party implementation is added; existing
Go BSD-3-Clause and SDK generator/runtime notice obligations remain unchanged.

With no external network, 58 SDK tooling tests, 13 layout tests, the layout and
roadmap checks passed. The pinned one-domain generation still has 25 public
operations and now has 122 files: only the Go dispatch changed and its one
helper was added; all 118 Python output files are byte-identical to the
metadata-only generation. Gofmt reports no diff in the helper and ten-case Go
fixture. Eight controlled Python scenarios pass across sync/async,
standard/assistant and 409/lost-response cases, each followed by one explicit
new call. These exercise generated Python dispatch with MockTransport, not a
real server or PostgreSQL.

### Native Linux audit transport qualification

Frozen fixture source 783567ea4a2e19f3b2f7ca065d47dfe59886633e and transport
source 1e8f1fadf8caff8d7b9ac771cd91641ba19d4287 passed all ten top-level
protocol groups and all six subtests, with zero skips and exit 0. The generated
Identity client ran over loopback HTTP/TLS with Go 1.27.3 linux/amd64,
`-race -count=1 -p=2 -mod=readonly -timeout=90s`. Its network namespace had
only loopback; module and checksum downloads were disabled. This covers the
shared GET positive replay control, repeated lost audit replies, separate
manual calls, 409, client timeout, caller cancellation, CA/cookie preservation,
redirect refusal, unsupported executors and concurrent calls. It does not
exercise the Control dispatcher, PostgreSQL or an installed user flow.

The two-group parent exited 1 after 49.817 seconds because the resource check
before the independent model-history codec group observed I/O full avg10
27.29, above its maximum 5. That second group never started: its actual root
count is zero, with no test exit or skip. The audit group remains a qualified
pass; the combined segment is not a pass. No admission retry was attempted.
Both inherited lock descriptors closed, their original carriers remained,
the temporary directory was removed and a separate observation found neither
the parent nor its child process group. The earlier Mac wait remains exit 75
with zero Go roots; it is not native Linux evidence.

The private evidence on ownsolid-cpu is
`/root/lane-rel-tools/identity-audit-contract/go-native-linux-proof.json`
(SHA-256 `cd648aa45ce2ab13f08838b9254a18b2151ca93c56f0201a3f1f9ca1d5e13cd1`)
and `go-wire-native-linux.log`
(SHA-256 `f391f6191c1293348969b862e005b7126f4245a300000d978276ef027e985d15`).
The audit fixture SHA-256 is
`6aace94f201533adf7a79cb8115ed5bf1f30052b260d99910fb0fa3fd7fa7821`.
All 122 generated audit SDK files and all 81 independent history SDK files
were verified before execution and after release; their hashes are unchanged.
Whole repository check/build/test, product generation and Web/installed
qualification remain open. No roadmap box is ticked by this transport run.

### Composition with current public metadata tooling

The audit candidate and DEV-204 closed-object review tooling were rebased and
composed on main 9975d769 in input 4b1e5c99. The audit contract, SDK generator,
Go helper and actual native wire fixture are byte-identical to the qualified
783567ea input. Its Linux ten-root transport result remains bound to that
original source; no native Go rerun or second resource admission was attempted.
The Models contract still has SHA-256
`7a27e07d45fd56640e96a26fa1f7a3b74635af13fb9cc463d420518ac006d9bc`;
the private Model history draft and its unexecuted codecs are not composed here.

Thirty-two targeted contract/generator/inventory tests passed on the composition.
The actual 430-operation comparison against main 9975d769 has no declaration
conflicts, operation identity changes or closed-object shape findings. Its one
named-response review finding is the explicit 409 shared Problem binding for
listAuditEvents. The generated Go 409 accessor change is intentional and
documented in the SDK README; it must not be reported as a general compatibility
pass or silently removed from the review report.

A separate private projection used the current main Help articles and pinned
parser dependencies. Both actual Web generators and both checks passed: 30
TypeScript and 144 Help files. Exactly the original five generated/locale paths
change; the updated relay patch SHA-256 is
`a3def1685309d4a7609274852cbc17f0904f9477a8edcaa9d016b4ced085dea9`.
No product Web file was changed. The owner must land that metadata generation
before the new OpenAPI can pass the repository's generated-output gate.
This projection does not qualify the separate shared Help URL assertion.
The Web owner has since fixed that assertion at
44ab52293e516128ba2efc0edc2cb2053acc3462; f11aa11f records four focused
bilingual entrance cases and a fresh installation-client self-check. The two
earlier MODEL failures remain evidence, and those focused checks are not a
complete gate for this API/SDK composition.

Required remaining gates are the five-file product generation handoff and a
complete check/build/test on their actual integrated source, including the
owner's shared Help fix. CLI b967d7c8 whole make test and installed AUD-108 acceptance
remain distinct. Existing native audit transport and owner Web recovery results
cannot substitute for those gates. No roadmap checkbox moves here.

SCI 8531226d supplies WriteScopedWithin for a caller-owned PostgreSQL transaction
and performs no commit or retry itself. Main now pairs the actual workspace Go
consumer with that writer; the owner's dc7feaf1 qualification covers exact scope,
append refusal, cancellation and both real COMMIT wire-loss modes. The published
installation-wide HTTP view still uses WriteWithin and gains no workspace query
or route. These are owner-qualified SQL results, not this SDK's transport run.
The current server exact-action whitelist still excludes audit.events_read;
retain category=audit and do not extend that enumeration or change the thirteen
standard response fields.
