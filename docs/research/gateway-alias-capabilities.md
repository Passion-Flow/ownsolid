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
Keep these metadata reads off machine authorization and existing HTTP
paths until DEV publishes the field and clients.

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
The candidate has no public discovery route yet.

With the protected fixture DSN file in `PROJECT_ONE_TEST_DSN_FILE`,
run this from the repository root.

```sh
go -C services/control test -p 1 ./internal/modules/distribution   -run '^Test(PostgresAliasCapabilitiesFollowVerifiedScopedTarget|AliasContextCeilingRequiresRecognizedSource|ActualAliasCutoverTLSNullableContractCSRFHistoryAndExactReplay)$' -count=1
```
