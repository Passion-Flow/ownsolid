# GW-106 gateway rate limits and token budgets

A request count bounds submission pressure but does not bound GPU occupancy or
token consumption. Enforce independent request-rate, active-request and
hourly, daily and monthly token limits across the tenant, application and key
of an authorized call.

## Sources and algorithm

- [LiteLLM virtual keys](https://docs.litellm.ai/docs/proxy/virtual_keys) enforce
  parent-team and per-key rate and budget policies. [Portkey budgets](https://portkey.ai/docs/product/ai-gateway/virtual-keys/budget-limits)
  document per-key token limits with recurring resets. Apply every configured
  ancestor cap to the same call, and reject an explicit child ceiling above any
  corresponding ancestor ceiling at startup.
- [Envoy local rate limiting](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/local_rate_limit_filter)
  uses token buckets and computes a delay until a request can be retried.
  The [Redis algorithm comparison](https://redis.io/tutorials/howtos/ratelimiting/)
  describes fixed windows, token buckets and exact sliding logs. A fixed
  one-second window can admit twice the allowance across its boundary. A
  continuously refilled bucket has constant space but permits an initial burst
  plus refills within a trailing second. Use an exact one-second sliding log
  with monotonic timestamps and bounded configured capacity per scope. It
  permits a burst up to the configured allowance and enforces the ceiling in
  every trailing second. The cost of expiring timestamps is amortized across
  admitted requests.
- [OpenAI's streaming contract](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events)
  places aggregate usage in the final usage chunk and warns that interrupted
  streams may never provide it. The gateway already requests this chunk from
  the backend regardless of client display preferences, and validates usage
  against the tokenizer and output ceiling.
- [RFC 6585 section 4](https://www.rfc-editor.org/rfc/rfc6585.html#section-4)
  defines 429 and calls for details about the condition.
  [RFC 9110 section 10.2.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3)
  defines the integer-seconds form of Retry-After. Round rate and reset waits up,
  and use a one-second retry hint for concurrency, whose release time is
  unknown. Preserve the control plane's integer delay when it rejects a call.

## Identity and execution

The existing [gateway wire](../../api/gateway/v1/protocol.go) authenticates a
gateway, workspace and model binding but does not return a tenant, application
or API key ID. Use an explicit mapping in the gateway's private operator JSON.
The [control credential format](../../services/control/internal/modules/distribution/keys.go)
carries the key UUID, and [control authorization](../../services/control/internal/modules/distribution/authorization.go)
authenticates that UUID and its secret in the returned workspace. Resolve
the mapping after successful control authorization and require the configured
workspace to match. An unmapped key is denied when scoped quotas are configured.
A fresh control admission remains mandatory before any inference. Caller
headers, request bodies and aliases cannot select a quota scope. The JSON is
optional so existing deployments retain their existing control limits.

Check rate and concurrency together before queueing and backend work, with a
single in-memory lock per tenant. Hold the concurrency permit through request
preparation and generation, including streaming and cancellation. Count
requests that pass this check even if they later fail validation or the token
budget check. Each scope can omit dimensions and remains subject to its
ancestors' settings.

After fresh admission, tokenize the prompt through the selected backend.
Before sending the generation request, debit the validated prompt token count
plus the requested maximum output at all three scopes and configured periods.
This upper bound protects against concurrent overspend; a character heuristic
or a debit only after generation does not. At successful unary completion or
validated SSE completion, refund the difference between the debit and actual
prompt-plus-output usage. For SSE, successful completion requires the valid
usage chunk, exact DONE and clean upstream EOF. Keep the original debit on
missing usage, malformed output, timeouts, cancellation or uncertain execution.
Debit belongs to the UTC hour, day and calendar month in which generation was
reserved. A later settlement never credits a newer period.

## Durability and scope

Check limits in memory, with no quota RPC or database read. Persist accepted
token debits and successful refunds in a bounded private per-tenant snapshot
under the existing gateway state-directory lock. Publish through a synced
temporary file, [atomic rename](https://pubs.opengroup.org/onlinepubs/9799919799/functions/rename.html)
and [directory sync](https://doc.rust-lang.org/std/fs/struct.File.html#method.sync_all).
Wait for the debit to be durable before dispatch; treat storage uncertainty as
unavailable for new token reservations in that tenant. Run file operations on
the blocking pool. A crash can retain an unrefunded debit, preserving the ceiling.
Requests limited only by rate or concurrency do not write quota state. Reject a
wall clock regression into an earlier persisted budget period to avoid resetting
spend on a backward clock adjustment.

This is a single-gateway quota authority. Shared counters across gateways,
authoritative application metadata, automatic key registration and Console
policy management need control-plane and deployment work. Static configuration
and local persistence let the Rust path enforce a complete local hierarchy
with the current wire.

## Dependencies and acceptance

No new service, model, dataset or external runtime call. Promote the existing
locked [time](https://github.com/time-rs/time) crate to a direct dependency for
UTC calendar arithmetic. It is MIT OR Apache-2.0 and permits commercial use,
redistribution with applicable notices and offline use. The quota path reuses
Rust std, Serde, serde_json and UUID (MIT OR Apache-2.0), Tokio and Axum (MIT)
and the existing nix filesystem wrapper (MIT). These permit commercial use,
redistribution with the applicable notices and offline use.

Verify each limit independently, aggregate sibling keys against their application,
tenant isolation, config validation, rolling-second boundaries, retry delay,
atomic concurrent debits, stream refunds and unknown usage, UTC rollover,
and token persistence after reopening state. Exercise 429 and final usage
through the TLS listener with loopback control and backend fixtures. Report
an optimized contention benchmark of the in-memory check separately from
the durable budget path.

## Local release benchmark

Apple M4 Max, 8 worker threads and 128 tenants, all three request and
concurrency scopes enabled, 800,000 admitted checks. The quiet rerun
measured 5.58 million checks/s and p50/p95/p99 of 0.125/0.583/9.125 µs
for key lookup, validation and acquisition. The durable path, measured
separately against the local filesystem with 1,000 sequential reserve/refund
pairs, took 9.904/14.671/21.820 ms for reservation and
9.848/15.245/22.648 ms for refund. A run concurrent with build and test work
reached a 76.959 ms reservation p99. Durable I/O remains sensitive to the
state volume and serializes within one tenant. These numbers exclude TLS,
control RPCs, and upstream inference.
