# Agent vLLM tool parser candidate (GW-103)

Reviewed 2026-11-09. The candidate is for a disposable Linux amd64 NVIDIA
qualification node. A normal Agent build cannot select it.

## Upstream behavior and selected configuration

The [gateway round-trip probe](gateway-tool-roundtrip.md) showed why the
current pinned text launch emits raw content for a forced named call.
In vLLM 0.31.0, [ParserManager](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/parser/parser_manager.py)
only resolves a tool parser when both the enable flag and parser name are
present. [Chat serving](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/openai/chat_completion/serving.py)
uses the resulting unified parser for named calls in both JSON and SSE.
With a forced named call, the source allows a structured call with
`finish_reason: "stop"`. Require the actual call envelope and arguments in
both modes. The finish reason alone cannot qualify a call.

[Qwen's function calling guide](https://qwen.readthedocs.io/en/latest/framework/function_call.html#vllm)
selects Hermes for Qwen3. The [pinned tokenizer template](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/tokenizer_config.json)
uses JSON `name` and `arguments` inside `<tool_call>` tags, which matches the
[built-in Hermes parser](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/tool_parsers/hermes_tool_parser.py).
The [Qwen3 XML format](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/parser/qwen3.py)
is for a different template. Reject parser selection by a mutable model name,
parser plugins, request-supplied templates and scraping raw assistant text.
Set non-thinking mode for this candidate and leave the reasoning parser unset.
The [versioned CLI](https://docs.vllm.ai/en/v0.31.0/cli/serve/) documents the
following argv suffix. `--enable-auto-tool-choice` loads the local parser.
Gateway qualification admits a single explicitly selected function in the first
round and uses `none` in the result round.

```text
--enable-auto-tool-choice --tool-call-parser hermes --no-trust-remote-code --no-trust-request-chat-template --default-chat-template-kwargs '{"enable_thinking":false}'
```

## Trust, licenses and compatibility

- The [base catalog](../../api/agent/v1/model-catalog.json) binds
  `Qwen/Qwen3-0.6B` revision `c1899de289a04d12100db370d81485cdf75e47ca` and
  every file. `ModelCatalogDigest()` is
  `4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc`.
  The previously verified `tokenizer_config.json` SHA-256 is
  `d5d09f07b48c3086c508b30d1c9114bd1189145b74e982a265350c923acd8101`.
  The historical CPU text result used a different image and qualifies
  neither this CUDA recipe nor this model's tool calls.
- The candidate accepts only [compiled recipe v1](../../api/agent/v1/inference-recipe.json),
  `vllm/vllm-openai` 0.31.0, Linux amd64 CUDA 13.0.2. The SHA-256 of
  Go `json.Marshal(ApprovedInferenceRecipe())` is
  `48fa2638f2e3e8dca1359e77b34aa4f7a34b9f24ba0dee96602feb6fb1f08a31`.
  This binds both the OCI manifest
  `sha256:a4a4c0437bf7240089da5f08aa370c4aee17ae5290f7a3b468825ee26c4c3a6b`
  and image config
  `sha256:c76d0e2225a4b1cb1e2109ace39639f55e714abd1a7a427acc8b0bbd7f6a83b3`.
  Use the existing signed offline delivery path for the exact pinned image.
  Agent still inspects the local image, verifies model bytes, prepares the
  guarded network and checks container ownership before starting it.
- [vLLM](https://github.com/vllm-project/vllm/blob/v0.31.0/LICENSE) and
  the [model](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
  are Apache-2.0. Commercial use, offline use and redistribution are
  permitted under the license and notice terms. The exact image retains its
  existing [NVIDIA CUDA distribution terms](https://docs.nvidia.com/cuda/eula/index.html).
  This candidate adds no dependency, dataset, image or upstream source copy.
- Keep `inference.deploy.v1` and profile version `1.6`. Old and current
  release Agents continue to receive the same text lease and render the
  same base and derived commands. Unknown parser, recipe and argv fields in
  a lease must fail strict decoding. The diagnostic build is selected only
  by applying the fixture in a scratch checkout. It uses the normal lease
  and manager, so both Docker create and recovery compare the same candidate
  argv. A running text container and candidate container have different
  ownership identities. Use fresh enrollment, deployment and Agent state
  on a disposable node. Remove the diagnostic deployment with its own Agent
  before reusing that node. A future mixed-fleet rollout needs a separately
  qualified recipe and versioned admission that sends it only to Agents
  advertising support.

## Acceptance

Local tests must compare the whole candidate container configuration with the
text baseline except for the exact argv suffix, exercise unchanged derived
launches, and refuse catalog drift, recipe changes, the CPU image, alternate
image identities and wire-supplied parser flags. The model still runs as
`2000:0` with read-only root and model, a private guarded network, denied DNS,
loopback host port `18000`, one GPU UUID, and the existing resource limits.

Before any activation, run the two-round backend probe on the pinned GPU
engine with external networking disconnected. Verify an actual native call ID,
function name, closed-schema arguments, matching result, and tokenization
parity in JSON and SSE. Retain the source version, image IDs, model catalog
hash, argv, response envelopes and per-round counts. The gateway still has to
qualify its authorization, scope-bound IDs, final usage and durable receipt
behavior independently.

## Reproduce the qualification build

From the checkout containing this note, create a disposable detached worktree.
The patch lives in [Agent test data](../../services/agent/internal/inference/testdata/gw103-tool-candidate.patch).
Keep the binary outside the release build and release bundles.

```sh
scratch=$(mktemp -d)
git worktree add --detach "$scratch/src" HEAD
git -C "$scratch/src" apply --check "$PWD/services/agent/internal/inference/testdata/gw103-tool-candidate.patch"
git -C "$scratch/src" apply "$PWD/services/agent/internal/inference/testdata/gw103-tool-candidate.patch"
go -C "$scratch/src/services/agent" test ./internal/inference -count=1 -run 'GW103|ApprovedContainer|DerivedContainer|FixedDockerContract|OwnedNetwork'
go -C "$scratch/src/api" test ./agent/v1 -count=1 -run '^TestInferenceToolCandidateKeepsV1LeaseClosed$'
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C "$scratch/src/services/agent" build -trimpath -o "$scratch/agent-gw103" ./cmd/agent
```

Enroll the diagnostic build on the disposable node with the normal inference
profile, signed offline input and verified base model. Deploy through Control
using a fresh deployment, without publishing an alias. Wait for the existing
text readiness probe, then record the inspected container image, argv and
network policy. Send the first request to the node's loopback
`POST http://127.0.0.1:18000/v1/chat/completions` with `Content-Type: application/json`.

```json
{
  "model": "projectone-qwen3-0.6b",
  "messages": [{"role":"user","content":"Call lookup_color with city Oslo. After the result, state the color in one word."}],
  "tools": [{"type":"function","function":{
    "name":"lookup_color",
    "description":"Look up the fixture color for a city.",
    "strict":true,
    "parameters":{
      "type":"object",
      "properties":{"city":{"type":"string"}},
      "required":["city"],
      "additionalProperties":false
    }
  }}],
  "tool_choice": {"type":"function","function":{"name":"lookup_color"}},
  "parallel_tool_calls": false,
  "temperature": 0,
  "max_tokens": 128,
  "stream": false
}
```

1. Require exactly one native `message.tool_calls` entry with a nonempty
   `id`, type `function`, name `lookup_color` and JSON-string arguments that
   decode strictly to `{"city":"Oslo"}`. An empty or truncated call, raw
   JSON in `content`, an unexpected function or an extra or duplicate key
   fails the probe. The fixture function returns `blue` locally.
2. Keep the original request's tools and user message. Append an assistant
   message with `content: null` and the exact `tool_calls` array just
   returned. Append `{"role":"tool","tool_call_id":"<returned id>","content":"blue"}`.
   Change `tool_choice` to `"none"`. Require a final answer containing `blue`
   and no tool calls. Backend replay checks the engine contract. Gateway
   replay must also reject a changed or foreign result ID before dispatch,
   because the [Qwen template does not encode the IDs](gateway-tool-roundtrip.md#bounded-candidate).
3. In each round send `model`, `messages` and `tools` from that exact request
   to `POST http://127.0.0.1:18000/tokenize`. Its `count` must match
   `usage.prompt_tokens`. Use the server's fixed template defaults on both
   endpoints. Do not pass a client template or template kwargs through the
   gateway. The [vLLM tokenize renderer](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/serve/tokenize/serving.py)
   accepts those tools and uses the same default template kwargs.
4. Repeat as SSE with `stream:true` and `stream_options:{"include_usage":true}`.
   Reassemble a single call by tool index, require its ID, name and complete
   validated arguments, then use that run's ID for its result round. Require
   terminal usage with the same prompt count and normal stream termination in
   both rounds. A failure after partial output cannot dispatch a replacement
   call. Repeat a plain text request and verify that the diagnostic node
   still serves ordinary text.
