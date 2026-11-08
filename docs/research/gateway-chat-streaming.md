# GW-101 offline Chat Completions streaming

The existing Rust route authorizes a model alias, gets the engine's chat
template token count, relays bounded SSE events, and records verified usage
before sending DONE. Exercise that path against an engine that actually
generates tokens. Protocol fixtures still cover interruption, malformed
events, control refusals, replay and shutdown because those cases are
difficult to reproduce predictably with an engine.

[OpenAI's Chat Completions reference](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events)
defines a final usage-only chunk when requested. The
[vLLM compatible server](https://docs.vllm.ai/en/latest/serving/openai_compatible_server.html)
uses the tokenizer's chat template and exposes tokenization plus chat
generation. The [vLLM CPU 0.29 CLI](https://docs.vllm.ai/en/v0.29.0/cli/serve/#--uds)
can bind an AF_UNIX socket with its container network disconnected. A
loopback-only test relay can expose that socket to the Rust fixture without
giving the engine a network interface. No product transport or API changes
are needed for this test.

Use the cached vLLM CPU image and the repository's approved
[Qwen3-0.6B files](../../api/agent/v1/model-catalog.json) for a real-engine
compatibility check. [vLLM](https://github.com/vllm-project/vllm/blob/v0.29.0/LICENSE)
and [Qwen3-0.6B](https://huggingface.co/Qwen/Qwen3-0.6B) are Apache-2.0,
permitting commercial use, offline use and redistribution with required
notices. No new Rust dependency, model distribution or dataset is involved.
The CPU image is a test input and differs from the pinned NVIDIA production
recipe in version and platform.

The test requires an explicitly supplied loopback backend using the approved
served name. It calls through the real gateway TLS listener using the
existing synthetic authorization fixture. A pass requires actual streamed
text under the public alias, consistent prompt and completion usage, exact
DONE, and an identical durable gateway outcome. Any timeout, missing usage,
changed model identity or non-loopback test address fails the check. The
existing fixtures remain the deterministic failure coverage. Installation
qualification also needs the real control service, an exposed gateway
entrance and the approved backend deployment (GW-114).

## Verified CPU fixture

Run the test with a loopback endpoint whose vLLM serves
`projectone-qwen3-0.6b` from local files.

```sh
cd services/gateway
OWNSOLID_TEST_VLLM_ADDRESS=<loopback-ipv4:port> cargo test --locked --offline \
  offline_vllm_stream -- --ignored --nocapture
```

On `ownsolid-cpu`, the vLLM CPU 0.29.0 image at
`sha256:66064cf683152e60eff2e5b18ec2d4ace236fb063c0a241f918691db9caddcf4`
ran with `--network none`, a Unix-domain socket and the catalogue model
files mounted read-only after SHA-256 verification. The gateway test
process had loopback-only outbound network permission. The public alias
returned streamed text, 23 prompt tokens, 9 completion tokens, one DONE
marker and an equal durable outcome. The gateway served real TLS, while
control responses came from the existing fixture.

## Native gateway and real control-plane fixture

The existing `gateway_integration` Go profile also passed with the native
gateway binary and a disposable PostgreSQL 18.6 database with pgvector 0.8.7
installed in `public`. Both shared and dedicated machine HTTPS routes
preserved aliases, acknowledged completed usage, enforced revocation and
retained pending outcomes during control outage. The same profile passed
license expiry and the accepted-operation case. This profile uses the real
Go service and PostgreSQL with synthetic model responses.

The combined quota smoke used the native Rust binary, the actual Go TLS
handler, PostgreSQL and the same network-disconnected vLLM CPU image in
one request path. Both admitted streams returned 23 prompt and 9
completion tokens and terminated with DONE. PostgreSQL acknowledged the
two outcomes with identical usage. The [quota note](gateway-limits.md)
records the admission and two rejected calls. Installer and production
backend qualification remain separate.

## Deliver final usage only after a durable outcome

The [OpenAI Chat Completions request reference](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
defines a usage-only chunk immediately before the DONE marker when
`stream_options.include_usage` is set. The
[WHATWG event-stream format](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation)
delimits events with a blank line. Continue forwarding bounded text chunks
incrementally. Keep the validated usage-only chunk for the terminal write so a
truncated or malformed upstream tail cannot report final usage to the caller
before the gateway has verified and stored the outcome.

Sending usage on arrival leaves the caller with final counts even if the
upstream then sends an invalid frame or the spool cannot publish the result.
Buffering the entire stream would defeat incremental delivery. Buffer only
the terminal payload and send it after a valid DONE, EOF and successful local
outcome publication. Persist the same usage even when the caller omits the
usage chunk. The combined terminal write still contains two complete SSE
events for clients that request usage.

The TLS fixture must receive text and a safe error without a usage-only chunk
or DONE when an otherwise valid stream ends with malformed trailing data.
A forced spool publication failure must also suppress both terminal events.
A complete stream must expose exactly one usage-only chunk immediately before
DONE, with counts matching the durable outcome. Decoder tests must continue
to cover fragmented line endings and byte bounds. The offline vLLM fixture
exercises the same successful path against a real engine.

The change uses the existing Rust runtime and JSON stack. It introduces no
dependency, model or dataset. The existing offline engine and model licences
are listed above.

The Linux amd64 rerun on 2026-11-09 exercised the malformed tail and
forced atomic-spool failure over gateway TLS. In a network namespace
with no external interface or route, the native Rust gateway and Go
control handlers also passed the PostgreSQL 18.6 and pgvector 0.8.7
integration profile for shared and dedicated machine HTTPS, outage
recovery and licence expiry. That profile uses a synthetic model.
Placing the gateway test executable and its private state on tmpfs
avoided timeouts on the shared development server's disk. The real
installed CPU model and console key workflow are recorded under
[GW-114](../product/roadmap/gateway.md#p1-enterprise-release).
