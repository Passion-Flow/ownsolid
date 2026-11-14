# Foreground Responses text protocol (GW-103)

Reviewed 2026-11-09.

## Protocol evidence

- [OpenAI Responses create](https://developers.openai.com/api/reference/resources/responses/methods/create) and the [generated request types](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response_create_params.py) specify bearer-authenticated creation with string or item input, optional instructions, an output token limit and streaming. Hosted storage defaults to enabled. The local slice accepts omitted or false storage and reports `store: false`.
- [OpenAI Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events) specify typed, sequenced response, output item, content part and text events. A foreground caller cancels by closing the connection. Polling, resuming and explicit cancel apply to stored background execution. See [OpenAI background mode](https://developers.openai.com/api/docs/guides/background).
- [OpenAI response usage types](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response_usage.py) distinguish totals from cache and reasoning breakdowns. [vLLM chat serving source](https://github.com/vllm-project/vllm/blob/main/vllm/entrypoints/openai/chat_completion/serving.py) makes those breakdowns conditional. The local slice projects the three validated totals and omits unverified breakdowns.
- Anthropic [authentication](https://platform.claude.com/docs/en/manage-claude/authentication), [versioning](https://platform.claude.com/docs/en/api/versioning) and [streaming](https://platform.claude.com/docs/en/build-with-claude/streaming) define a different header contract, indexed content-block events, cumulative usage and a final message stop. See the [Messages protocol research](gateway-messages.md) for that slice.

## Implementation and licenses

Translate foreground text input and optional string instructions to the existing single-choice text generation request. Reuse the same fresh control admission, workspace quota, bounded tokenizer and upstream dispatch, private outcome spool and reporting. Project validated chat data into Responses events at the gateway. Passing a request straight through to an engine Responses endpoint would bypass the tested chat tokenization and final-usage contract. A second internal HTTP request would duplicate parsing and complicate cancellation and admission.

This slice introduces no dependencies, models or datasets. Both SDK and vLLM sources are protocol references only, under their respective [OpenAI SDK](https://github.com/openai/openai-python/blob/main/LICENSE) and [vLLM](https://github.com/vllm-project/vllm/blob/main/LICENSE) Apache-2.0 licenses. Apache-2.0 permits commercial and offline use and redistribution with its license, attribution and notice obligations. No SDK or source is copied or shipped by this change. Fixtures use synthetic text and token counts.

## Acceptance and failure modes

- Accept a public model alias, nonempty string input, optional nonempty string instructions, bounded maximum output tokens and temperature, stream flag, and false or omitted store/background flags. Reject duplicate keys, unsupported fields, stateful execution, tools and unsupported input items before backend work. Keep API keys and private model IDs off public payloads.
- Produce one assistant text item with request-correlated IDs. Map normal stop to completed, output-limit stop to incomplete with `max_output_tokens`, and a content filter stop to incomplete with `content_filter`. Use the same usage receipt and budget settlement as Chat Completions.
- Stream typed SSE in order, exposing text before completion. Emit usage and a success or incomplete terminal only after valid upstream usage, a terminal marker, EOF and a durable local outcome. Bound retained text. Simulate upstream failure after text, invalid terminal data, a failed outcome write and a downstream disconnect. Verify the request is dispatched once and incomplete transport retains the reservation.
- Exercise authorization and reporting over local TLS and repeat with the native Rust process and the existing Go/PostgreSQL integration profile. Use loopback-only tests with no external network or model download. Full tool semantics, state and coding-tool interoperability remain separate GW-103 acceptance work.

## Stateless text message items

The generated OpenAI [easy input message](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/easy_input_message_param.py)
and [input text part](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response_input_text_param.py)
define a message with optional type, a role, and a string or typed text
content. Accept the local system, user and assistant roles in a nonempty
input array. Each item may omit type or declare message. Accept its
nonempty string or exact input_text parts, joined in order with the same
newline convention as the Chat adapter. Add optional instructions
before the supplied turns. Use the existing combined message, context
and output limits and the same admission, reporting and SSE path.

Keep item IDs, stored state, tool and output items, unsupported roles and
non-text parts outside the accepted shape. Passing item arrays through
to the engine would introduce unqualified state and tools. A shared text
joiner preserves ordering while each public route validates its own
exact part type. The prior supported string input stays valid.

No dependency, model or dataset is added. The existing Serde licensing
is MIT or Apache-2.0, and the SDK reference is Apache-2.0. Commercial and
offline use and redistribution are permitted under their notice terms.
The TLS fixture checks exact tokenization and generation text, verified
JSON and SSE usage, and that unsupported or malformed items cause no
authorization or backend calls. Real tool acceptance still requires
the serving and GPU evidence in the tool round-trip note.

The TLS fixture first reproduced HTTP 400 for a valid text message
array. After the parser change, JSON and SSE preserved ordered turns
and verified usage. Tool result items, persisted IDs and malformed
or non-text parts caused no model authorization or backend work. The
default Rust suite and Clippy passed. The disconnected native Go and
PostgreSQL foreground test used message arrays for SSE and cancellation,
and verified the original request's canonical receipt and trace.
