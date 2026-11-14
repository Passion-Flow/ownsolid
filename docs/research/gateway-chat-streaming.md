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

The fresh real-engine rerun remained at startup. One isolated CPU vLLM
attempt loaded the weights and stalled during warm-up. A second with
bounded CPU threads did not reach readiness during the run. The server
showed sustained disk I/O waits. The earlier live evidence still
applies to the installed path it exercised. Acceptance of the changed
stream tail and engine request identifier against real vLLM remains open.

## Bounded readiness rerun

On 2026-11-09 the same pinned CPU image was preloaded, and all nine
model files were copied to tmpfs and matched their catalogue SHA-256
digests. The container used `--network none`, one sequence, four
assigned CPUs, an 8 GiB memory limit with no container swap, and
128-token context and batch limits. It exposed only a Unix socket.

The 361-second readiness window ended with harness exit 22. The
container was still running, without an OOM event. Python started at
03:16:54 China time, completed its Torch import at 03:18:02, and
completed the vLLM CLI module import at 03:19:41. The process was
repeatedly in uninterruptible kernel file or buffer waits. The CLI
did not log server arguments, weight loading or a bound socket.
The final direct socket probe failed to connect (curl exit 7).
The direct generation and gateway streaming probes were never reached.
The logs establish a startup limit on this shared host before model
loading. They provide no result for the selected CPU configuration or
for the gateway's handling of actual model events.

## Comparison with the installed GW-114 run

[GW-114's prototype](gateway-delivery.md#prototype-2026-11-08) used the
vLLM CPU 0.29.0 tag and the approved Qwen3-0.6B revision, with eight CPUs
and 10 GiB. The engine served on host loopback TCP, and the documented
warm-up took about four minutes. The later installed run
([commit 90ebbad](https://github.com/Passion-Flow/ownsolid/commit/90ebbadd664a8102ba2e20e3bff32bc913aeb668))
verified a scoped key, real chat and streamed generation, and matching
usage in the call log. The installation evidence records the image tag
and model family. The archived launcher available for comparison names
the backend address but does not identify the successful engine
container's digest or complete arguments.

The current cached tag resolves to
`sha256:66064cf683152e60eff2e5b18ec2d4ace236fb063c0a241f918691db9caddcf4`.
That is the pinned image from the bounded GW-101 rerun. The same catalogue
revision and weight digest appear in the GW-114 source and the verified
GW-101 inputs. GW-101 staged its weights on tmpfs, used four CPUs and
8 GiB, and wrapped the image entrypoint to timestamp imports. It probed
`/v1/models` over a Unix socket before running chat. The last observation
was inside CLI startup, before model loading or socket bind. Changes to
engine request or streaming semantics could only be evaluated after that
stage.

For the next isolated run, use the cached digest and the image's original
`vllm serve` entrypoint, the approved files, and the recorded eight-CPU and
10 GiB allocation when the shared-host gate permits it. Keep the engine
in its own disconnected network namespace. Probe its private socket for
the approved served name and run one direct stream before the Rust
gateway fixture. Record startup time, socket readiness, direct SSE
completion, and the gateway's usage and request-ID checks separately.
The GW-114 TCP setup is evidence for the installed path. The socket and
loopback relay preserve the no-egress condition for this acceptance.

## Text-only message content parts

The [OpenAI Chat Completions request](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
permits text parts in the message content array. The approved local
text route currently accepts a string only, which rejects clients
that send their text in parts. The pinned
[vLLM 0.31.0 chat parser](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/chat_utils.py)
joins text parts with newline separators for a string-content template.

Accept an array of exact text parts for the existing system, user and
assistant roles. Flatten once, in order, before both tokenization and
generation. Keep the existing body and context limits, and refuse empty
arrays, empty parts, unsupported modalities and extra part fields. A
literal string keeps its original text. Passing arrays straight to the
engine would make behavior depend on its template autodetection and
would expand the accepted input beyond the qualified text adapter.
Joining without separators would change the approved engine semantics.

No dependency, model or dataset is added. The new parser uses the
existing Serde stack (MIT or Apache-2.0) and the pinned vLLM source
describes the text convention under Apache-2.0. These licenses permit
commercial and offline use and redistribution under their notice
terms.

The native Rust TLS fixture should assert the same newline-joined
messages at both tokenizer and generation, in JSON and SSE. Confirm
existing verified usage and DONE behavior. Check that a malformed,
empty or non-text part reaches neither authorization nor the engine.
The fixture uses synthetic text and no live vLLM process.

The disconnected native Rust TLS fixture first reproduced HTTP 400 for
valid text parts. After normalization, both JSON and SSE returned verified
usage. The fixture checked the same newline-joined messages at tokenization
and generation. Empty, malformed and non-text parts caused no model
authorization, admission or backend calls. The default Rust suite and
Clippy passed on Linux.
