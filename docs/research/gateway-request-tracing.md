# GW-109 request correlation

A caller receives a gateway-generated UUID in `X-Request-ID`. The admission
and outcome already use the same UUID as the control-plane request record,
which preserves the node, deployment and deployment revision. The local
engine generates a different request identifier today, so an operator
cannot correlate an engine generation with that record.

[vLLM's compatible server documentation](https://docs.vllm.ai/en/v0.29.0/serving/openai_compatible_server/#extra-http-headers)
describes `X-Request-Id`. Its pinned
[engine source](https://github.com/vllm-project/vllm/blob/v0.29.0/vllm/entrypoints/serve/engine/serving.py#L116-L126)
uses the header as the base request ID, and its
[chat adapter](https://github.com/vllm-project/vllm/blob/v0.29.0/vllm/entrypoints/openai/chat_completion/serving.py#L279-L309)
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
[vLLM Apache-2.0 implementation](https://github.com/vllm-project/vllm/blob/v0.29.0/LICENSE)
and the [approved offline chat fixture](gateway-chat-streaming.md). Apache
2.0 permits commercial use and offline redistribution with the required
licence and notices.

GW-109 also needs an operator-visible GPU link using the deployment's
recorded placement and verification against actual hardware. Correlation
IDs alone do not provide distributed spans. A future tracing design should
follow [W3C Trace Context](https://www.w3.org/TR/trace-context/) and define
the trust boundary and local collector configuration before propagating
trace context.
