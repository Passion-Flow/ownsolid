# GW-109 request correlation

A caller receives a gateway-generated UUID in `X-Request-ID`. The admission
and outcome already use the same UUID as the control-plane request record,
which preserves the node, deployment and deployment revision. The gateway
also forwards its own ID to the colocated generation endpoint for engine
correlation.

[vLLM's compatible server documentation](https://docs.vllm.ai/en/v0.31.0/serving/openai_compatible_server/#extra-http-headers)
describes `X-Request-Id`. Its pinned
[engine source](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/serve/engine/serving.py)
uses the header as the base request ID, and its
[chat adapter](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/openai/chat_completion/serving.py)
uses that base to name the completion and the actual engine request.

Send the existing gateway UUID to the fixed loopback generation endpoint.
The Rust backend accepts a typed UUID from the admitted request handler.
A client header is never copied. Reusing an untrusted header would allow a
caller to merge unrelated calls in logs or supply values interpreted by
engine extensions. The
[vLLM advisory](https://github.com/vllm-project/vllm/security/advisories/GHSA-vfp2-c8pq-v6h6)
documents one historical example of unsafe interpretation. Returning the
engine's independent ID in a new control outcome field would require a
wire change and would only become available after generation finishes.

The TLS integration fixture should supply a competing caller header and
verify that the outbound engine header matches both the client-facing
gateway UUID and the admitted request. The offline vLLM fixture should
verify the same identifier in its streamed completion ID. The generation
request sends no API key, cookie or forwarding metadata. It keeps the
same fixed destination, deadlines and retry policy.

This part needs no new dependency, model or dataset. It uses the pinned
[vLLM Apache-2.0 implementation](https://github.com/vllm-project/vllm/blob/v0.31.0/LICENSE)
and the [approved offline chat fixture](gateway-chat-streaming.md). Apache
2.0 permits commercial use and offline redistribution with the required
licence and notices.

GW-109 also needs an operator-visible GPU link using the deployment's
recorded placement and verification against actual hardware. Correlation
IDs alone do not provide distributed spans. A future tracing design should
follow [W3C Trace Context](https://www.w3.org/TR/trace-context/) and define
the trust boundary and local collector configuration before propagating
trace context.

## Operator lookup and native acceptance

The pinned vLLM 0.31.0 request-ID path still bases the chat request
identifier on the incoming X-Request-Id header. The native gateway's
Chat, Responses and Messages generation routes all call that backend
chat adapter. Keep the generated UUID as the correlation key and use
existing operator routes. The authorized request detail identifies the
admitted deployment revision and node. The Admin deployment detail
identifies its assigned GPU. Compare the IDs and revision before using
the current deployment as placement evidence. A later deployment
revision is separate evidence from the request's admitted revision.

This check adds no transport, package, model or dataset. It uses the
existing Go and pgx test fixture (BSD-3-Clause and MIT) and pinned
PostgreSQL (PostgreSQL License). The terms permit commercial use,
redistribution with notices and offline use.

For the native Go/Rust foreground protocols, send a competing
caller-provided header. Compare the gateway UUID with the ID seen by
the synthetic backend and with the authorized history for JSON, SSE and
a cancellation. Follow one ID through the actual Admin TLS request and
deployment contracts to the fixture's assigned GPU. Use only the
synthetic placement recorded by the node fixture. Actual GPU execution
and a distributed tracing collector remain separate qualifications.

Disconnected Linux acceptance ran both foreground protocol adapters
against the native Rust gateway and Go/PostgreSQL. Each JSON, SSE and
cancelled call reached the synthetic engine under the gateway-generated
UUID and appeared under the same UUID in the authorized history. The
Admin HTTPS request detail and deployment detail returned the same
node and revision and the deployment's fixture GPU assignment.

## Historical trace through target changes

The admitted request carries its own deployment ID and revision.
The alias supplies its stable public name and can move to another
deployment. Use the existing authorized request detail as the historical
trace source. Resolving the current alias target for an older request
would attribute it to the replacement. The engine correlation contract
and its pinned vLLM source above still apply.

In the existing disconnected cutover fixture, retain the actual Admin
HTTPS request detail immediately after the canonical report. Read it
again after retarget and after rollback. Require the same node, alias,
deployment revision, actor, key and usage. Refuse the same request to
an independently authenticated foreign tenant. The fixture's deployment
observations are synthetic. This adds no runtime or dependency, and
the existing Go and PostgreSQL licence terms above apply.

The disconnected PostgreSQL and HTTPS run passed. The published
`GatewayRequestResponse` body was identical before target changes,
after retarget and resume, and after rollback and resume. The
independently authenticated foreign Admin received RESOURCE_NOT_FOUND.
The current alias and original key moved to the replacement while the
record retained its original admission and outcome. The fixture
verified admitted placement identity and did not execute a GPU workload.
