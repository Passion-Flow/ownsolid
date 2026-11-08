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
