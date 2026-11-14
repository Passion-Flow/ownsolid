# Anthropic Messages text protocol (GW-103)

Reviewed 2026-11-09.

## Protocol evidence

- The [Messages API](https://platform.claude.com/docs/en/api/messages/create) accepts a model, output token bound, prior turns, optional top-level system instructions and a stream flag. A string and one plain text block represent the same turn. Consecutive turns, prefill, images and tools have additional semantics.
- [Authentication](https://platform.claude.com/docs/en/manage-claude/authentication) documents Bearer and legacy x-api-key headers. [Versioning](https://platform.claude.com/docs/en/api/versioning) requires an explicit version. The [2026-10-21 migration](https://platform.claude.com/docs/en/api/migration-2026-10-21) removes sampling fields. This slice targets the current 2026-10-21 text contract and refuses other versions.
- [Streaming](https://platform.claude.com/docs/en/build-with-claude/streaming) starts a message with empty content, opens an indexed text block, sends incremental text, closes the block, then emits cumulative usage with the stop reason and a final message stop. [Stop reasons](https://platform.claude.com/docs/en/build-with-claude/handling-stop-reasons) distinguish natural stop, max tokens and policy refusals. The chat backend does not provide Anthropic policy refusal details.
- [Error responses](https://platform.claude.com/docs/en/api/errors) use a typed envelope and request-id header. The same envelope is used as an SSE error after headers. The gateway also preserves its stable error code and existing correlation header.

## Implementation and licenses

Translate an alternating user/assistant history ending in a user turn into the existing text generation request. Map a top-level system string or one plain text block to the first backend system message, with the same messages sent to tokenization and generation. The API uses the configured local sampling defaults. Require the current API version and one site credential. Workspace identity comes from control, so reject client workspace selection and beta headers.

Reuse the same route pipeline for fresh control authorization, quota, bounded tokenizer and upstream generation, outcomes and reporting. Directly forwarding Messages requests or adding an SDK client would either bypass that control path or add another network request. The adapter instead projects the existing validated backend stream without retaining the entire generated text. Its initial input count comes from the local tokenizer. Verified cumulative output usage is emitted only with a durable terminal outcome. Cache, tool and thinking counters are omitted.

No dependency, model or dataset is added. The current [Anthropic SDK source](https://github.com/anthropics/anthropic-sdk-python/blob/main/LICENSE) and [vLLM source](https://github.com/vllm-project/vllm/blob/main/LICENSE) are Apache-2.0 references. Older SDK releases have their own [MIT license](https://github.com/anthropics/anthropic-sdk-python/blob/v0.84.0/LICENSE). These licenses permit commercial and offline use and redistribution subject to license, attribution and applicable notice requirements. No third-party code is copied or distributed.

## Acceptance and failure modes

- Accept a bounded, positive max_tokens, configured public model alias, alternating nonempty string or single plain text block turns and optional top-level system. Refuse duplicate keys, ambiguous credentials, unsupported versions, unsupported request fields, multiple blocks, empty turns, prefill, tools and beta state before dispatch.
- Verify the backend receives the same translated system and user messages for tokenization and generation. Return one assistant text block in JSON. Map upstream stop to end_turn and length to max_tokens. Reject other upstream stop semantics without inventing a refusal category or reporting success.
- Verify SSE event names, indices, order, text deltas and cumulative usage over TLS. An interrupted stream emits an error when the connection still permits it. Invalid terminal data and a failed outcome write cannot emit final usage or message stop. Closing the downstream connection cancels the upstream, retains unverified quota and persists a cancellation.
- Repeat the authorization, receipt and cancellation path through the native Rust gateway, Go distribution owner and isolated PostgreSQL, with synthetic inference replies and network disconnected. Full tools, generated public OpenAPI and real engine acceptance remain open.
