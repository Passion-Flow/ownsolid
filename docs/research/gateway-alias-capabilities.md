# Gateway alias identity and capabilities (GW-104)

Reviewed 2026-11-09.

[LiteLLM deployment and model group documentation](https://docs.litellm.ai/docs/router/learn/deployments_and_model_groups)
separates the name callers request, deployment identity and metadata. A
published routing change replaces a registry snapshot. [Portkey's model
catalog](https://portkey.ai/docs/product/model-catalog) separates model
discovery and model details from provider credentials. The [OpenAI model
object](https://developers.openai.com/api/reference/resources/models)
provides a name, creation time and owner in the compatible model list.

## Local alias authority

Go publication and cutover validate the tenant, workspace, gateway node,
deployment and exact ready revision. The machine authorization snapshot and
the later durable admission already return the alias ID and deployment
revision. The Rust gateway authorizes the name before queueing and admits
it again after the queue. Go uses the same alias row for a retarget. Before the fix, Rust
compared only the workspace across those answers. An inconsistent
admission for the same public name reached the backend in the regression
fixture and produced HTTP 200.

Compare the stable alias ID from the first answer with the alias ID of
the durable admission before any backend call. Keep the fresh deployment
and backend selection when the alias ID is the same, so a valid retarget
can serve queued traffic. If the ID differs, persist the existing
terminal gateway-unavailable outcome to release the admission, with null
usage and no generated output. Comparing the whole binding would also
reject legitimate target revisions. Relying only on the public name leaves
an inconsistent control response undetected.

## Capability discovery

Keep the OpenAI-compatible list's existing shape. A versioned discovery
projection can use the exact ready target and serving recipe, plus the
enabled gateway adapter. The tokenizer currently enforces the tighter of
the runtime context window and the admission limit. A public capability
projection still needs DEV to define its fields and route, including
unknown values and how capabilities follow a cutover. Tools, embedding and
rerank require qualified serving support for their respective routes.
A model's family name or tokenizer window alone is insufficient evidence
of those capabilities.

## Licenses and verification

No dependency, model or dataset is added. The Rust routing fixture uses
the existing [Axum](https://github.com/tokio-rs/axum/blob/main/LICENSE),
[Tokio](https://github.com/tokio-rs/tokio/blob/master/LICENSE) (MIT),
[Serde](https://github.com/serde-rs/serde),
[UUID](https://github.com/uuid-rs/uuid) and
[reqwest](https://github.com/seanmonstar/reqwest) (MIT or Apache-2.0).
The licenses allow commercial use, redistribution under their notice
terms and offline use. The fixture replies contain synthetic text.

- Make the synthetic control return a valid admission for a different
  alias ID under the same name and workspace. Assert that the native HTTPS
  gateway fails before tokenization, records the original request's
  terminal outcome without usage and leaves no generation.
- Change only the target identity and revision in the admission for the
  same alias. Require successful generation with the correct public name
  and a single successful outcome. Existing Go/PostgreSQL cutover tests
  separately exercise the real persistence and authorization boundary.

The Linux loopback-only run rejected the different alias with HTTP 503,
made zero tokenizer and generation calls, and delivered one canonical
outcome with null usage and no output. The valid retarget produced one
generation and a receipt with nine tokens. Disconnected PostgreSQL
covered ready-revision publication, cutover and same-key rollback, and
the existing Admin TLS cutover contract. The Rust HTTP model backend
was a synthetic fixture. No serving recipe or public schema changed.

## Scoped target metadata candidate

The current deployment owner validates ready state, its exact revision
and lease, the scoped model, the pinned serving image and a positive
generation observation in `ReadyGatewayTargetWithin`. Model import
verifies the approved base catalogue by digest. The derived model owner
validates its immutable catalogue and context field before serving.
[vLLM's server contract](https://docs.vllm.ai/en/latest/serving/openai_compatible_server/)
separates text generation from tasks such as pooling and tool calls.
The [compatible OpenAI model object](https://developers.openai.com/api/reference/resources/models)
does not describe those capabilities.

Provide a separate server-internal, opt-in projection using the existing
Admin and Portal alias visibility and a single scoped transaction.
Require the current alias revision's immutable target snapshot to match
the exact ready deployment, model catalogue and serving recipe. For an
identified ready target, report verified text generation and the
configured context ceiling. Use the minimum of the pinned model limit,
serving recipe limit and current gateway admission policy. The live
tokenizer can apply a tighter limit. Missing or mismatched snapshot,
unknown kind or digest, disabled alias, unavailable deployment and
inactive workspace yield no metadata. The candidate PostgreSQL path
requires the local cell on the alias, target and deployment.

A name or family match could describe the wrong model after a retarget.
Reading the latest history row without matching the alias revision could
turn an old target into a current promise. Reading the host's agent
capability would describe its operator profile instead of its deployment.
Keep metadata reads off machine authorization. Human HTTP reads use the
separate composition below, with capability publication disabled until
DEV publishes the field and clients.

Test the projection through the real PostgreSQL authorization service
for an Admin and Portal member, a missing old snapshot, a disabled alias,
a stale deployment revision, and foreign workspace or tenant refusal.
Retarget and resume the same alias and verify the new target identity.
Compare the existing HTTP response against the released schema. Verify
the recipe selector refuses unknown digests and recipes. No dependencies,
models or data sets are added. The existing Go and PostgreSQL tools,
Apache-2.0 model catalogue and pinned engine remain within their notice,
commercial use and offline redistribution boundaries.

### Versioned contract handoff

The released `ModelAlias` and machine binding schemas are closed.
Keep the compatibility model list and machine admission unchanged.
For the Admin and Portal cell-scoped alias detail and list operations,
the proposed optional `capabilities` object can carry
`text_generation_verified` and `context_token_ceiling`. The first is a
positive assertion backed by the exact deployment's verification.
The second is a positive integer ceiling from the model, recipe and
policy, subject to the live tokenizer. When the server has no exact
proof, omit the object. An empty or false value would conflate missing
proof with a verified negative. The projected alias ID and revision
and deployment ID and revision must match the surrounding alias view.
Produce both views within the same transaction when wiring the
contract. Maintain existing disabled-alias visibility and the
not-found boundary for unauthorized scopes.

The candidate intentionally carries no provider address, credentials,
model manifest, raw observation, or API adapter capability. A claim
for tools, pooling, rerank, multimodal input or an individual gateway
adapter needs the owning versioned serving and gateway capability
source. DEV owns the schema, generator and operation binding. WEB
owns the catalogue display and its missing-metadata state.

### Disconnected qualification

`TestPostgresAliasCapabilitiesFollowVerifiedScopedTarget` passed with
PostgreSQL 18.6 and pgvector 0.8.7 in a loopback-only Linux namespace.
Admin and a Portal member saw the same verified base target and the
configured ceiling of 1,024 tokens. The service withheld metadata
for an otherwise callable predecessor alias without a target
snapshot and an alias bound to a deployment revision that did not
exist. It preserved cross-workspace and cross-tenant not-found
responses and Portal's hidden paused-alias behavior. After retarget
and resume it returned the replacement deployment identity. The
existing Portal HTTPS detail still conformed to `ModelAliasResponse`.
Deployment observations were synthetic. Recipe selection was tested
separately for unknown inputs and the approved derived recipe.
Production capability publication remains disabled pending the read contract.

With the protected fixture DSN file in `PROJECT_ONE_TEST_DSN_FILE`,
run this from the repository root.

```sh
go -C services/control test -p 1 ./internal/modules/distribution   -run '^Test(PostgresAliasCapabilitiesFollowVerifiedScopedTarget|AliasContextCeilingRequiresRecognizedSource|ActualAliasCutoverTLSNullableContractCSRFHistoryAndExactReplay)$' -count=1
```

### Compatibility boundary and DEV inputs

The shared capability selector backs `Service.AliasCapabilities` and
the registered human read composition. Both use the same workspace
and visibility rules as alias detail. In that transaction it requires the current alias
ID and revision, node, deployment ID and revision, model ID and kind,
catalogue digest and nullable recipe digest to agree across the
local-cell alias, immutable target, live deployment and ready model.
The deployment owner also requires a fresh generation observation.
A valid alias with unavailable or unidentified evidence yields a nil
projection. A paused alias yields no projection for Admin and the
existing not-found result for Portal. A refusal keeps its existing
problem code. The internal result carries the exact alias and
deployment revisions to prevent attaching it to a stale view.

The [OpenAPI Schema Object](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
and the released `ModelAlias` schema keep the current response closed.
Test all four registered Admin and Portal list and detail operations
against their released response schemas. Check that the existing
payload never acquires a capability field and that a workspace without
membership and a foreign tenant cannot read it. Admin still sees a
paused alias in its normal representation. Portal omits it from the
list and returns the existing not-found problem on detail.

The registered read composition binds the optional object to these
exact operations. DEV must publish the schema and generated clients
before enabling its composition switch. Use the same authorization,
page limit and cursor.
For a projected result, require both proposed fields together.
`text_generation_verified` is true and `context_token_ceiling` is a
positive integer. Omit the entire object when the projection is nil.
List and detail must both use the exact target in the same authorized
transaction. Publish the schema and regenerate both clients before
exposing the object. The public model list, machine binding and
mutation responses retain their current fields. No runtime, dependency
or product data changes are needed to verify this boundary.

The versioned operation IDs are `adminListModelAlias`,
`adminGetModelAlias`, `portalListModelAlias` and `portalGetModelAlias`.
The disconnected `TestPostgresAliasCapabilityPublicContractBoundary`
run checked all four through the real TLS dispatcher. Their published
response schemas accepted the replies. Each authorized item retained
its alias and deployment identity without a capability object. The
Portal member could not read a separate workspace, and neither
audience could read a foreign tenant. Admin could still read the
paused alias, while Portal detail returned MODEL_ALIAS_NOT_FOUND
and its list excluded the alias. This run used PostgreSQL 18.6 and
synthetic deployment observations.

```sh
go -C services/control test -p 1 ./internal/modules/distribution -run '^TestPostgresAliasCapabilityPublicContractBoundary$' -count=1
```

## Registered read composition

DEV's [publication boundary](public-api-inventory.md#gw-104-alias-capability-contract-preparation)
fixes the candidate object and the four operations. The released
ModelAlias component is shared by reads and writes and remains closed.
Use an independent AliasReadView for human list and detail, with the
existing Alias embedded and only the two approved capability fields.
Keep mutation results, machine bindings and the compatible models list
on their existing types. A composition-time PublishAliasCapabilities
switch defaults to false until DEV publishes and regenerates the read
contract. It is not a customer policy or an authorization bypass. The
registered handlers use the read type in both modes; the default mode
must still pass the released schemas without new capability queries.

When explicitly enabled, prepare availability and capability evidence
inside the same authorized transaction that selects the alias or page.
Match the current alias and deployment revisions again in the evidence
query. Do not call the standalone capability method after returning a
page, because a concurrent cutover could attach new evidence to an old
alias. Preserve the existing cursor scope and page order. Omit the object
when readiness or exact evidence is absent, including paused Admin
aliases, and preserve Portal's existing visibility rules. A database
failure returns the existing storage problem rather than a successful
response with silently omitted proof.

Verify the actual four HTTPS routes with a mixed page of a verified
alias and a callable legacy alias without a snapshot. Compare the
optional object to the draft closed schema and both revisions to the
surrounding view. Exercise cursor continuation, scope refusal, unavailable
or stale evidence, pause, retarget and resume. Check that mutation results
still pass the released ModelAlias schema while the read projection is
enabled. The existing default-mode contract test must continue to pass.
No new dependency, model or data set is added; the licences above apply.
The current upstream [OpenAPI schema rules](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
and [vLLM serving overview](https://docs.vllm.ai/en/latest/serving/online_serving/)
continue to support the separation of metadata, enabled endpoints and
model tasks. The switch stays disabled in production composition until
publication. Full UI and actual engine qualification remain separate.


### Registered-route qualification

TestPostgresAliasReadCapabilitiesAtRegisteredEntrances passed on Linux
amd64 against PostgreSQL 18.6 with pgvector 0.8.7 in a loopback-only
namespace. The fixture explicitly enabled the actual registered
handler's service. Both audiences returned the exact two-field object
for the verified target and omitted it for a callable legacy alias
without a snapshot. Mixed one-item pages and detail retained exact alias
and deployment identities. Foreign workspace and tenant reads were
refused. A stale deployment revision omitted the evidence; pause hid
the alias from Portal, and retarget and resume exposed the replacement.
The actual cutover replies still passed the released closed mutation
schema while read capabilities were enabled.

With the owned evidence table temporarily unavailable, default-mode
list and detail still passed the published schemas. Enabling capability
publication returned STORAGE_UNAVAILABLE over HTTP 503 for both
audiences instead of treating the failed lookup as absent evidence.
The fixture restored its table and disposed its owned schema. The
internal selector, released default read boundary and recipe selector
regressions also passed. Deployment observations were synthetic; no
model or GPU ran. This verifies backend composition with a draft object,
not publication of a new OpenAPI contract or generated clients.

```sh
go -C services/control test -p 1 ./internal/modules/distribution -run '^Test(PostgresAliasReadCapabilitiesAtRegisteredEntrances|PostgresAliasCapabilityPublicContractBoundary|PostgresAliasCapabilitiesFollowVerifiedScopedTarget|AliasContextCeilingRequiresRecognizedSource)$' -count=1
```

Production composition still leaves PublishAliasCapabilities false.
DEV can bind the four read operations to a separate closed read
component and regenerate Web, Help, Go and Python clients without
adding the object to mutation responses. Activation and the catalogue
UI remain open. The shared Linux full check, build and Rust gate waits
for ARC's first window and the current migration ordering; this focused
run does not replace that gate.


### Exact schema-58 source handoff

The minimal gateway-only source and test patch against 106ca52d is
retained in /root/lane-gw-acceptance/gw104-main106-handoff.76uoxxyb,
with contract.json and a SHA-256 manifest. It contains the two read
methods, exact evidence selector, shared alias visibility helper,
composition switch, registered handler calls and focused tests. It
excludes metering, key-purpose DDL, shared migration files and all
public schema/generator changes. The patch applies independently to
the baseline; the prior actual handler and PostgreSQL execution used
the retained gateway stack, so the final assembled publication still
requires its own acceptance.

Enable PublishAliasCapabilities only in the verified full-platform
PostgreSQL composition after DEV publishes the four read contracts
and generated clients. Earlier cumulative profiles may not have the
target-history table; keep the switch off there. Non-PostgreSQL
projections remain absent. The switch does not change authorization,
and customer input never selects it. A missing evidence relation on
an enabled composition is a storage failure, not a supported legacy
snapshot. A legacy row in an existing evidence schema may omit the
object when it lacks an exact snapshot. Preserve that distinction
when wiring startup and qualifying the installed consumer.
