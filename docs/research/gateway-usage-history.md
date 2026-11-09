# Auditable gateway request history (GW-107)

Reviewed 2026-11-09.

## Existing extraction path

[LiteLLM Spend Tracking](https://docs.litellm.ai/docs/proxy/cost_tracking)
uses bounded pages for programmatic per-request export. [Portkey Logs
Export](https://portkey.ai/docs/product/observability/logs-export) separates
the filtered request log from scheduled or manual file delivery. The ownsolid
history API already returns bounded JSON pages with request identity, state,
timestamps and canonical outcome metadata. This slice verifies extraction
through the existing authorized request list. An attachment, bulk snapshot
or new public contract belongs to the export owner.

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
