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
current repository in a network namespace with no external routes. In the
current tree the cloud inventory contract contains six public operations
without IDs. The components and nodes contracts both declare four
node-exporter routes. The owning contracts and generated clients require
a coordinated change to resolve those records without breaking callers.

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
