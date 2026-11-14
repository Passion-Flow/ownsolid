# Client function round trip (GW-103)

Reviewed 2026-11-09.

## Public and backend contracts

[OpenAI Responses function calling](https://developers.openai.com/api/docs/guides/function-calling)
returns a function call item with an item ID, a separate call ID, a name and
JSON-encoded arguments. A stateless next request supplies the original
history, the function call item and a function_call_output item whose
call_id matches. The client runs the function.
[Anthropic Messages](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)
represents the equivalent as an assistant tool_use block with object input
and a user tool_result block referring to tool_use_id. The two public
envelopes need separate parsers. Neither client-provided ID is authority.

The [NVIDIA serving recipe](../../api/agent/v1/inference-recipe.json)
pins vLLM 0.31.0. The [agent command](../../services/agent/internal/inference/container.go)
sets no tool or reasoning parser. The [vLLM CLI defaults](https://docs.vllm.ai/en/v0.31.0/cli/serve/)
disable automatic tool choice and leave parser selection empty. The cached
CPU 0.29.0 image previously used for text is a distinct test input.

The [vLLM 0.31.0 named-choice guide](https://docs.vllm.ai/en/v0.31.0/features/tool_calling/#named-function-calling)
describes default named function support. In the source matching the pinned
version, [ParserManager](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/parser/parser_manager.py)
returns no parser when neither parser is configured.
[Chat serving](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/openai/chat_completion/serving.py)
then passes raw generation text through with an empty tool-call list on the
named non-streaming branch, and emits plain content deltas on the streaming
branch. A named request therefore cannot supply a verified native call ID
on the current installed path. The source branch was exercised with a
synthetic model output using a small Python probe, without importing or
starting vLLM.

## Bounded candidate

Evaluate a single caller-executed, explicitly selected Responses function
with a closed object schema. The prototype accepts one required string
property, rejects undeclared keys, nested values, duplicate keys and empty
or oversized arguments, and pairs one call with one text result. Disable
parallel calls, persistence and automatic tool selection. The signed call
ID prototype binds a nonce to the authenticated tenant, workspace and key
identity, alias and deployment revision, tool name and schema, canonical
arguments and preceding history. Changed history, IDs, parameters or scope
fail verification. A production implementation must derive scope from
fresh control authorization and define key lifecycle and stale-call handling.

The [pinned Qwen template](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/tokenizer_config.json)
was rendered from the SHA-256 verified local catalog file. It rendered the
assistant tool call and the tool response text but omitted both call IDs.
Changing the IDs did not change the prompt. Match IDs and validate the
call before constructing backend messages. The
[vLLM tokenize request](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/serve/tokenize/protocol.py)
accepts tools and its [renderer](https://github.com/vllm-project/vllm/blob/v0.31.0/vllm/entrypoints/serve/tokenize/serving.py)
uses them in the prompt. An eventual generation implementation must prove
that tokenization sees the same tool definitions, history, arguments and
result as generation in both rounds.

The probe ran in a loopback-only network namespace. Its mock output and
signed IDs are synthetic, and it qualifies the source branches, template
and binding logic only. Gateway tool requests stay disabled. A separately
qualified serving recipe with an authorized parser is a candidate. Treating
raw assistant JSON or markup as a native tool call is rejected because the
serving path does not produce the authoritative call envelope. A distinct
structured-output adapter would need its own engine and accounting
qualification before it could be used.

## Licenses and acceptance

[vLLM 0.31.0](https://github.com/vllm-project/vllm/blob/v0.31.0/LICENSE)
and the [pinned Qwen model](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
are Apache-2.0. The isolated probe used
[CPython](https://docs.python.org/3/license.html) under the PSF license and
[Jinja 3.1.2](https://github.com/pallets/jinja/blob/3.1.2/LICENSE.rst) and
[MarkupSafe 2.1.5](https://github.com/pallets/markupsafe/blob/2.1.5/LICENSE.rst)
under BSD-3-Clause. These licenses permit commercial use, offline use and
redistribution subject to their license and notice terms. This change adds
no product dependency, model, dataset or redistributed upstream code.

- The public routes refuse a complete forced-function declaration and
  forged result history before admission. A genuine key from a second PostgreSQL installation fixture is accepted
  by its own distribution owner and refused by both local text routes.
- An unsolicited upstream call after text produces a safe protocol error
  and retains the unverified token reservation.
  It forwards no call arguments or final usage and dispatches once.
  Disconnecting before that frame cancels the upstream and reports
  cancellation without a usage receipt.
- Before enabling any shape, run both rounds against an admitted,
  disconnected engine using the exact serving recipe. Require a real
  structured call with valid arguments and a bound result, matching prompt
  counts, one durable receipt per completed round, no retry after output
  and denial of a foreign key or scope. Full tool support stays open.
