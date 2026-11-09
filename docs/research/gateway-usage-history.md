# Auditable gateway request history (GW-107)

Reviewed 2026-11-09.

## Existing extraction path

[LiteLLM Spend Tracking](https://docs.litellm.ai/docs/proxy/cost_tracking)
uses bounded pages for programmatic per-request export. [Portkey Logs
Export](https://portkey.ai/docs/product/observability/logs-export) separates
the filtered request log from scheduled or manual file delivery. The ownsolid
history API already returns bounded JSON pages with request identity, state,
timestamps and canonical outcome metadata. This slice verifies extraction
through the existing authorized request list. The distribution service
owns the bounded CSV artifact and HTTP attachment.

Use the existing descending keyset of admitted_at and request ID, with
tenant and workspace equality, actor ownership in Portal, and the optional
API key filter. [PostgreSQL row comparison](https://www.postgresql.org/docs/18/functions-comparisons.html#ROW-WISE-COMPARISON)
defines the lexicographic boundary and [multicolumn B-tree
indexes](https://www.postgresql.org/docs/18/indexes-multicolumn.html)
support the leading scope equality. The existing owner and key indexes
support these reads. No offset scan, new table or copy of the receipts is
needed. Pages reflect live history. A request admitted ahead of the cursor
after an export starts is visible on the next extraction. This is not a
transactional snapshot across HTTP requests.

A stored outcome has a canonical SHA-256 digest from the machine receipt.
Report rechecks that digest on retry. In the disposable PostgreSQL fixture,
changing a valid outcome from 9 to 10 tokens without changing its digest
was accepted by both the detail and the page before the fix. Both paths now
use the same canonical verification as report replay.
Absent outcomes are meaningful for admitted or reconciled requests. A
cancelled request can have a valid terminal outcome with null usage. Neither
case represents zero verified tokens.

## Licenses and verification

The existing integration stack uses [Go](https://go.dev/LICENSE) and
[google/uuid](https://github.com/google/uuid/blob/master/LICENSE) under
BSD-3-Clause, [PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE) under
the PostgreSQL License, and [pgx](https://github.com/jackc/pgx/blob/master/LICENSE)
under MIT. These licenses permit commercial use, offline use and
redistribution under their notice terms. No dependency, model or dataset
is added. The gateway integration uses synthetic inference replies.

- Use the real authorized HTTP JSON page, including the API key filter, to
  read completion tokens, exact elapsed milliseconds, and a cancelled and
  an unknown usage row. Force equal admission timestamps to exercise the
  request ID tie-breaker. Reject cursor reuse with different authority or a
  different key. A foreign tenant session must not read the local workspace.
- Read real native gateway reports through the same RequestPage function
  and verify the persisted token totals, latencies and cancellation status.
  Replay an identical report without adding a request or audit event and
  refuse a conflicting report.
- Modify a disposable PostgreSQL outcome into a different valid receipt
  while retaining its original digest. The list and detail must fail closed
  without exposing a partial page. Recheck canonical data, unknown rows and
  the existing API response schema.

The isolated Linux run on 2026-11-09 used PostgreSQL with only loopback and
the real native gateway binary against synthetic inference responses.
The authorized JSON test returned four Portal requests with 20 known tokens
and five Admin requests. It denied the same list and detail to a separately
authenticated tenant. Native foreground Responses and Messages runs persisted
and read back each canonical JSON, SSE and cancellation receipt. The list and
detail mutation regression failed before the fix and passed after it.

## Bounded CSV artifact

Use Go's [encoding/csv writer](https://pkg.go.dev/encoding/csv) with the
header and CRLF convention described by [RFC 4180](https://www.rfc-editor.org/rfc/rfc4180.html).
The writer is in the existing BSD-3-Clause standard library. No new
dependency, model or dataset is needed.

Build one complete artifact from RequestPage and return its next cursor
separately. The existing maximum is 100 rows. Keep the same tenant,
workspace, audience, actor and optional key filter, and let RequestPage
validate canonical receipts before serialization. A failed page returns no
bytes or cursor. Each row has a request ID, UTC admission time, request
state, outcome status, request count, elapsed milliseconds and token
counts. Exclude API key IDs and secrets, actor and deployment metadata,
and all user-entered text. The result contains only UUIDs, dates, server
enums and unsigned decimal values. A missing receipt leaves outcome status
and elapsed time blank. Unknown usage leaves the token cells blank. Zero
milliseconds or a zero token count that the receipt proves is literal zero.

Export one bounded page instead of buffering or streaming a full workspace.
A live page can reflect a receipt reported since a prior read, while retrying
the same unchanged page returns the same bytes without altering the ledger.
A database aggregate would lose call-level identity and would need its own
authorization and canonical receipt checks. A CSV projection of the entire
history DTO would expose unnecessary identity and routing fields.

## Download boundary

Use a read-only GET on the cell-scoped Admin and Portal request histories,
adding `/export` to the list path. Use the dispatcher's existing bounded
memory download to send `text/csv; charset=utf-8`, a fixed
`gateway-usage.csv` attachment name per [RFC 6266](https://www.rfc-editor.org/rfc/rfc6266.html),
the byte length and the SHA-256 of the complete artifact. Reject malformed
raw queries before parsing the existing filters so an invalid key selector
cannot silently widen the export. Return the normal problem response if
authorization, pagination, storage or receipt verification fails. A caller
may continue by passing the opaque next cursor from the corresponding JSON
history page with the same key and scope. The two reads can observe
different data if new calls are admitted between them.

A self-contained download page should carry the next cursor from its own
`ExportUsagePage` result as a [next Link](https://www.rfc-editor.org/rfc/rfc8288.html).
The shared `httpserver.MemoryDownload` type in
`services/control/internal/platform/httpserver/download.go` currently has
no continuation field. Its writer, `success` in
`services/control/internal/modules/identity/http/handler.go`, is the exact
integration point for an optional next link before `WriteHeader`. The
distribution export branch can then populate it from `UsageExport.NextCursor`
and the original scoped route with the same key and limit. Omit the link
on the last page. Keep pagination out of the CSV and the attachment name.
The public OpenAPI and generated consumers should describe the download,
its success headers and normal problem responses. The new route uses
existing Go standard library types and has no new dependencies.

Test through the production dispatcher over TLS using real PostgreSQL.
Verify exact headers and rows, pagination with the JSON cursor, identical
retries on unchanged data, empty foreign-actor results, cross-tenant and
cross-audience refusal, malformed and over-limit queries, and a bad
receipt. Failed responses must have no CSV body or attachment headers.
The isolated run on 2026-11-09 used the real cell router and identity
dispatcher with locally verified TLS. The container network namespace
had only loopback. Four single-row Portal attachments contained 20
verified tokens, and the Admin attachment contained five workspace rows.
The CSV used the same JSON cursors, returned identical bytes on retry
and left request rows and audit counts unchanged. An unauthenticated
request, over-limit or malformed query, changed key or audience cursor,
foreign tenant and corrupt receipt all returned a problem without any
attachment. The companion receipt regression also passed on the JSON
list and detail. The direct continuation link and UI acceptance remain
open.

## Isolated continuation qualification

An isolated Go overlay tested the shared seam with the normal production
cell and identity dispatcher on 2026-11-09. The candidate adds optional
`NextPagePath` to `httpserver.MemoryDownload`. The distribution handler
sets it only when the exported page has a next cursor. It replaces the
cursor in the validated query, preserving the key and limit, and
reconstructs the scoped path using `cellroute.Public`. The shared
`success` writer formats the server-owned path as an RFC 8288 next
`Link` before sending the attachment. Existing downloads omit the field.

The disconnected TLS test followed only the attachment links to
navigate four Portal pages. It checked each same-origin path and scope,
compared each cursor with the JSON history contract, and verified an
identical link on retry. The final page, over-limit and malformed
queries, foreign tenant, scope mismatch and corrupt receipt returned
no next link. Failed responses returned only their normal problems.

The passing overlay and runner are retained in
`/root/lane-gw-acceptance/gw107-link.yMl6SoLb` on the Linux test host.
The [published CSV contract](gateway-usage-csv-contract.md) uses
JSON-cursor continuation and is ready for Web integration. Adopting the
shared type and writer changes needs a corresponding contract update
and verification from the usage page.
