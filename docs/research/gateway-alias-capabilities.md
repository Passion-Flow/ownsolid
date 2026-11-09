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
