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
components and nodes contracts both declare four node-exporter routes.
The owning contracts and generated clients require a coordinated change
to resolve those records without breaking callers.

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
