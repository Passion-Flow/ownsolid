# Portal conversation history search

APP-109 needs a caller to find conversations they can already read. The
[Open WebUI history search](https://docs.openwebui.com/features/chat-conversations/chat-features/history-search/)
matches titles and messages within the caller's visible conversations. The
ownsolid conversation schema has no title field. Search the current application
name and the question or plain-text answer of completed conversation turns.
Return the existing conversation metadata. Keep pending turns, tool results,
trace data and deleted conversations out of search.

## Query and scope

Stage optional `q` on the existing Portal conversation list, consistent with
the workspace list. Accept one nonblank UTF-8 term of at most 120 Unicode
characters with no control characters. Treat SQL wildcard characters as
literal text using the existing pagination helpers. Bind the term as a
parameter. PostgreSQL supports this case-insensitive substring predicate with
[`lower`, `LIKE` and `ESCAPE`](https://www.postgresql.org/docs/18/functions-matching.html).
Use a correlated `EXISTS` on completed runs so multiple matching turns yield
one conversation. Bind the same tenant, workspace and owner inside the run
subquery so PostgreSQL can scope it even when it builds a hash table. Match
the run's workflow, conversation and version to the authorized row. The
existing conversation page index restricts the outer scan, and the unique
conversation and turn key
gives the inner query its conversation prefix. Workflow retention and the
existing operation timeout still apply. Keep the unfiltered SQL unchanged.

Start with PostgreSQL. The existing unfiltered projection also supports MySQL
and Dameng and retains that behavior. Dameng documents limits on [CLOB pattern
matching](https://eco.dameng.com/document/dm/zh-cn/faq/faq-sql-gramm).
Search returns a storage availability problem on those engines until a native
text search can be qualified there. Never substitute an unfiltered response.
An external search service adds offline delivery and retention obligations.
A derived transcript or trigram index also adds storage and lifecycle costs.
Measure the indexed owner-scoped read before adding either. Avoid regular
expressions and tokenization because they change literal matching semantics.

## Continuation

Keep the existing `updated_at DESC, id DESC` keyset and `limit+1` behavior.
PostgreSQL requires a [deterministic order for limited results](https://www.postgresql.org/docs/18/queries-limit.html).
For searches, authenticate the entire encoded position and its Portal owner,
workspace, login session and exact term with the existing control-plane HMAC
key. Use a separate purpose string and the existing length-framed
[HMAC](https://www.rfc-editor.org/rfc/rfc2104) helper. A scope hash alone would
let a client copy the scope from a newly issued cursor onto an old position.
Retain the current cursor format and semantics when `q` is absent. Reject
search cursors used as unfiltered cursors and vice versa. The cursor carries
neither the term nor login credentials. This keyset is a live view. Updates
and retention between requests may move or remove rows.

The change uses the existing Go standard library ([BSD-style license](https://go.dev/LICENSE)),
PostgreSQL ([PostgreSQL License](https://www.postgresql.org/about/licence/)) and
existing ownsolid data. These dependencies permit commercial use, offline
operation and redistribution with their notices. No model, extension, new
database dependency or external index is required.

## Acceptance and handoff

Drive the registered Portal handler on real PostgreSQL in a disconnected
environment. Cover application name and completed question and answer matches,
literal wildcard and Unicode text, a repeated match, stable timestamp ties,
empty results, invalid input, another owner and workspace, deletion, session
revocation, and exact legacy query behavior. Exercise replay with a changed
term, workspace or login session and with a substituted cursor position.
DEV published `q` and the opaque search cursor semantics on
`ListApplicationConversations` in `9cb3d16a`. The Portal owner still needs
to wire the search field and start a new page when the term, session or
workspace changes. Rename and export require the workflow owner's persisted
title and export permission, versioned operations and Portal flows. Keep
APP-109 open for those integrations.

`TestPortalConversationSearch` passed through the registered dispatcher over
TLS in a network-isolated Linux container with PostgreSQL 18.6 and pgvector
0.8.7. It verified a signed search continuation against the existing legacy
cursor and checked that search responses contain only conversation metadata.

The same registered TLS flow passed natively on Linux amd64 with the full
schema-58 PostgreSQL 18.6/pgvector 0.8.7 catalog and network mode `none`.
Its binary was built offline with preloaded Go 1.27.3 and existing pinned
dependencies; no model was needed. The owned protocol container was confirmed
removed. An initial harness invocation could not start because its package
working directory was missing. Creating that owned directory corrected the
harness; no application code changed. The failed invocation and accepted run
are retained separately. Portal integration, rename and export remain open.
