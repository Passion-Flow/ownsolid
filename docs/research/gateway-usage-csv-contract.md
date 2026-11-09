# Gateway usage CSV download contract (GW-107, DEV-101)

The [registered Distribution handler](../../services/control/internal/modules/distribution/http/human.go)
at 93d8f7ad appends /export to the existing cell-scoped Admin and Portal
request lists. Its [HTTP acceptance](gateway-usage-history.md#download-boundary)
runs through the identity dispatcher and real PostgreSQL. Use the same
workspace parameter, session schemes and optional limit, cursor and key
filter as the JSON list. Admin uses platform authority and Portal uses
the session's actor in the workspace.

Each success returns one bounded UTF-8 CSV attachment with CRLF records
as described in [RFC 4180](https://www.rfc-editor.org/rfc/rfc4180.html).
The shared writer sets the attachment name per
[RFC 6266](https://www.rfc-editor.org/rfc/rfc6266.html), exact byte length,
artifact SHA-256, no-store and request correlation. The success response
has no JSON envelope. Current continuation uses the opaque cursor from the
corresponding JSON page with the same workspace, audience, actor and key.
Successive reads observe live history. A direct continuation link depends
on a production response that exposes it.

Use the [OpenAPI Response Object](https://spec.openapis.org/oas/v3.1.1.html#response-object)
for the media and headers, and the existing Problem schema for refusals.
Preserve the JSON list and detail identities and shapes. Embedding cursor
metadata in CSV would change its format. A full-history read would bypass
the page bound. Use the existing SDK raw response adapters for bytes and
keep typed failure decoding.

The existing [history dependencies](gateway-usage-history.md#licenses-and-verification)
and [SDK generator licenses](sdk.md#licenses) allow commercial use, offline
use and redistribution under their notice terms. No dependency, dataset
or model is added.

Verify both real paths, status and media type, byte length and digest,
JSON cursor reuse, and 400, 401, 404 and 503 problems without attachment
headers. Verify the pinned Go and Python clients encode the path and query,
preserve CRLF bytes and decode problems. Regenerate TypeScript and the
downloadable contracts for both entrances before the Web handoff.

The pinned Python generator initially treated text/csv as a decoded string
and attempted to pass it to BytesIO. The SDK's generator-only media override
uses its existing raw-byte file parsing, with no change to the advertised
text/csv media type. Go retains the exact bytes and parses the required
attachment headers in its generated response wrapper.

## Web handoff

Use the generated adminExportGatewayUsageCSV and portalExportGatewayUsageCSV
operations with the same site, cell, workspace, limit and optional API key as
the displayed JSON history. Pass that JSON page's cursor for subsequent
pages. On HTTP 200, download the attachment bytes as gateway-usage.csv.
Problem responses keep their usual HTTP status and problem code. The current
production reply has no continuation header. A later direct Link header
needs coordinated runtime, contract and Web verification.

The disconnected Linux amd64 fixture sent both registered paths through
the real TLS dispatcher and PostgreSQL 18.6. It checked status, media and
published header schemas and validated problem bodies. The existing receipt
assertions covered exact CSV rows, the JSON cursor, retries and scope.
Pinned Go and Python wire probes used both generated paths, checked scope
and query serialization, preserved CRLF bytes, and decoded INVALID_CURSOR.
