# Portal conversations

Specification draft for POR-102 and S-302. The build plan owner can adopt this entry after
reviewing the contract boundaries below. This draft uses the signed-in portal and the workspace
selected in its switcher.

## Evidence and approach

[Open WebUI](https://docs.openwebui.com/features/chat-conversations/) makes the transcript the
main surface and separates ancillary controls from everyday chat. [Dify's API guide](https://docs.dify.ai/en/api-reference/guides/get-started)
keeps application credentials on the server and gives application runs a separate identity. ownsolid
can use its own Portal session and the existing conversation and run resources. No product code,
assets or dependencies come from either reference. The draft adds no dependency, model or dataset.
The comparison informs the interface. The [applications](../../api/openapi/v1/applications/openapi.json)
and [workflows](../../api/openapi/v1/workflows/openapi.json) contracts define actual behavior.

Use persistent conversation and run reads so a reload can recover history and an in-flight response.
The existing run contract returns JSON snapshots. The draft uses background polling and a textual
state, with a step list when node results exist. Token streaming, fabricated progress and browser
calls to the model gateway would need behavior beyond this contract. Scope and idempotency stay with
the server. The browser does not persist prompts or results in local storage.

## Draft S-302 entry

### Conversations `/conversations`

**For** a workspace member using an application published to that workspace.

**Archetype** list with a preview drawer.

**Primary** New conversation.

**Does** finds and resumes the person's own conversations.

**Content** title, New conversation, history search and application filter, then a paged list. Each row shows
the application, when the conversation started, turn count and last activity. Show the
pinned version number when the list response includes it. The default list uses recent
activity order. Selecting an application gives a creation-ordered view and resets paging.
The application filter and accepted search term live in the address. The contract supplies
cursors and no total count. History search uses the published `q` parameter on the owned
cross-application projection; it never searches only the currently loaded browser page.

**Click** the row opens a drawer with its app, version when known, turn count and an Open
conversation link. The row name is a direct link to the detail page. Home's recent rows
use the same destination. For a failed-run notification, read the scoped run first to
resolve its `conversation_id`. A notification with no conversation keeps its run outcome
without a conversation link. The notification payload itself carries only the workflow
and run IDs.

**Dialog** New conversation opens a short form with a published application picker. An app
that cannot execute remains in the picker with a reason and an unavailable state. Submit
uses the selected publication's `version_id`. A stale publication is refreshed and the
new choice is shown before retry. The successful write opens the conversation page.
There is no drawer or second dialog inside the create dialog.

**Empty** first visit explains that conversations start with a published application and
offers New conversation. If there are no published applications, explain who publishes
them and keep the action visible with its reason. A selected application with no history
names that application and offers Clear filter. These are distinct from an absent workspace.
An accepted search with no matches names the term and offers Clear search instead.

### Conversation `/conversations/$workflowId/$conversationId`

**For** the owner of an existing conversation.

**Archetype** detail.

**Primary** Send message.

**Does** opens saved turns and sends a new question to the conversation's pinned publication.

**Content** breadcrumb back to the list, application name, conversation start time and a
status label. Use the application name and formatted creation time as the display title,
since the API supplies no separate conversation name. Tabs are Conversation and Activity.
Conversation shows the question and the server's response for each turn. Load older pages
with the cursor and keep already read turns visible. Activity uses recorded run timestamps,
outcomes and node steps. It does not infer audit actors, locations or tool execution from
an answer. When metadata cannot be read, use a neutral conversation heading and keep the
authorized history visible.

The composer names the application's current availability and uses the server's UTF-8
question byte limit. Show waiting, running, stopping, succeeded, failed, cancelled and
interrupted as text. After admission, poll the accepted run at the installation interval
until it reaches a terminal state. Reload restores the latest page and any active run.
One conversation has one active turn. A request with an unknown outcome keeps the
question and its intent key for an explicit retry. A new send gets a new key.

Render `answer` or the structured `model_list` only when `result_available` is true.
A successful run with a withheld result explains `result_unavailable_code` while retaining
the question and status. The Portal consumes the allowed workflow result and never
introduces document or knowledge management features.

**Click** Send keeps the writer on the conversation and moves the submitted question into
history only when the server accepts it. Stop run acts on the accepted run and keeps its
record. A cancellation acknowledgement shows Stopping until the server returns a terminal
state. If the run settled first, show its actual result. A failed or interrupted run can
prefill its question for a deliberate new send with a fresh intent key.

**Dialog** Delete conversation names the displayed resource, requires typing that name
and asks for the audit reason. The current revision is checked on the server. After a
stale revision or an ambiguous response, refresh the conversation before the user retries.
An acknowledged deletion returns to the list. The server requests cancellation of any
outstanding run and removes the conversation and its turns from Portal access.

### States and acceptance

| State | Behavior |
| --- | --- |
| Loading | Delay skeletons for fast reads. For a slower request, match the list or transcript layout. An admitted run has its own textual state. |
| Empty, first time | The list offers New conversation. An empty transcript invites the first message and names the application it will use. |
| Empty, filtered | The list names the chosen application or accepted search term and offers the corresponding Clear action. The transcript has no result filter. |
| Partial | Keep owned history when metadata or the next page fails. Keep prior turns when the active run cannot be refreshed. A result withheld by the server has its own explanation. |
| Error | Keep unsent input. Explain permission, unavailable publication, concurrency, quota and timeout failures with the appropriate retry or next step. |
| Denied | A session or membership failure offers sign-in or workspace recovery as appropriate. An unknown or another person's ID gets the same unavailable record experience. |
| Not ready | Disabled application or execution policy remains visible with the reason where the person expects to select or send. |

The browser must create, open, send, reload, stop and delete an owned conversation against
the real server with the network disconnected. Verify a pinned publication across a newer
publish, rejection of another person's conversation and another workspace, a lost create
or send response, simultaneous sends, changed membership, a withheld result and a failed
result followed by a new send. Verify keyboard focus, both languages and screenshots of
the two empty states, the active run and the failure path. A supported offline installation
must actually answer through a published text application before POR-102 can close.
A tool-only model list verifies its own result surface.

## Reusable contracts

The existing [Portal application helpers](../../web/packages/api/src/applications.ts) already
provide `listApplications` and `listConversations`. Use generated
`applicationsContract` and `workflowsContract` types with the existing typed transport.
`{cell}` below is `/api/v1/sites/{site_id}/cells/{cell_id}`. `{flow}` adds
`/workspaces/{workspace_id}/workflows/{workflow_id}`. Build scope from the active
workspace and session. Resource IDs select records inside that scope.

| Route | Use and response |
| --- | --- |
| GET `{cell}/applications` | Published apps. `workspace_id`, `limit`, `cursor`. Uses `version_id`, `can_execute`, and `question_bytes`. |
| GET `{cell}/application-conversations` | Owned recent conversations. `workspace_id`, `limit`, `cursor`, and optional `q`. Includes `workflow_name`, `version_number`, `turn_count`, `updated_at`, `can_execute`, and `question_bytes`. |
| GET `{flow}/conversations` | Owned history filtered to one app, ordered by creation. The compact response has `version_id` and `revision`, without the enriched display fields. |
| POST `{flow}/conversations` | Create with `version_id` and an audit `reason`. Requires an idempotency key. Returns a conversation with 201. |
| GET `{flow}/conversations/{conversation_id}` | Owned conversation and revision for detail and destructive confirmation. |
| GET `{cell}/workspaces/{workspace_id}/workflows/configuration` | Read execution policy and question limit for a direct conversation link without list context. A workflow read can supply the app name when accessible. Do not render draft configuration. |
| GET / POST `{flow}/conversations/{conversation_id}/runs` | Read paged run snapshots. Send with `question`, an audit `reason` and an idempotency key. Admission returns 202 and a run ID. |
| GET `{flow}/runs/{run_id}` | Poll or resolve a notification. Provides outcome, result visibility, timestamps and node states. |
| POST `{flow}/runs/{run_id}/cancellations` | Send audit `reason` and read `cancellation_requested_at` and actual state from the returned run. |
| DELETE `{flow}/conversations/{conversation_id}` | Send `expected_revision` and audit `reason`. The contract acknowledges deletion and has no restore operation. |

Create and send carry the same idempotency key with the same body when retrying. Ordinary
create, send and stop can use a fixed audit action reason distinct from the question. Delete
collects a human reason. The typed client sends CSRF headers for writes. The workflow
operations still lack per-operation error declarations at the reviewed revision. Match the
handlers' actual codes and add both locale explanations before shipping the UI. No changes
to those contracts or the formal plan are part of this handoff.

## Published search input and remaining screen boundary

Review on 2026-11-13 found that the draft's earlier search dependency had been satisfied:
`ListApplicationConversations` declares `q` with 1–120 characters, and the owning Applications
service implements it ([contract](../../api/openapi/v1/applications/openapi.json),
[owner](../../services/control/internal/modules/applications/service.go),
[search research](application-conversation-search.md)). The owner records its own registered
TLS/PostgreSQL and disconnected acceptance. That evidence is not a Portal UI run. The generated
contract already includes `q`; the convenience `listConversations` helper still accepts only
limit/cursor and needs the additive typed option when S-302 is adopted. No generated file needs
a manual edit or a new server field.

The per-workflow conversation endpoint has no `q`. Keep application filtering and global
history search visible without inventing their conjunction: while an application is selected,
search is unavailable with a reason and Clear application filter; while a term is active,
application choices stay visible but unavailable until Clear search. An address containing both
must explain the invalid combination and offer to clear either, rather than silently ignore one
or filter a bounded result locally. A future conjunction needs an owner contract before its UI.

An empty search omits `q` and uses the original unfiltered continuation. A submitted term starts
at the first page. Bind the term, account, audience, workspace and credential generation in the
view's query identity; changing term/workspace/session must never reuse a signed cursor or show
old-term results under the new term. A failed next page preserves only earlier pages of that
same authorized search. INVALID_CURSOR offers an explicit first-page restart. A refusal hides
rows and any preview. Preserve the draft and the owning Problem on failure; never substitute an
unfiltered response when search is unavailable, or infer a database-engine reason from a generic
503. Test literal wildcard input, Unicode character limits, blank/over-limit input, term changes,
same-account re-login, cursor substitution, cross-owner/workspace refusal, empty results and
next-page recovery. Use only existing pinned dependencies and their licences above.

The public Applications read declares entrance/error metadata. The reviewed Workflow operations
still have their shared default response and no per-operation `x-error-codes`; matching their
actual handler outcomes remains UX-131 work with that owner. S-302 is still only a table entry
in the mandatory build plan, so this note remains the concrete adoption draft, not authority to
ship a new screen. The next implementation boundary is a complete formal S-302 entry, followed
by the typed helpers and one full create/send/reload/stop/delete path. No conversation or search
UI implementation, browser run or installed workflow execution is claimed by this review.
