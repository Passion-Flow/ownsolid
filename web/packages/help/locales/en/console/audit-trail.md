# Audit trail

Every privileged action is recorded. The [Audit trail](/audit) shows who did what, to what, when,
from where and with what outcome.

## Finding an event

- Filter by kind of action, outcome, person, request and time. The filters are part of the address,
  so a link shows the same events to a colleague.
- Open an event to see its details, and follow it to the person or to every event of the same
  request.
- Each event has its own link that keeps working.
- The list names each action in your language. Its details keep the action's identifier for scripts
  and support, and a reason as it was recorded.

## A resource's activity

Open **Activity** on a node, model, deployment, account or single sign-on connector to see changes to it.
An account's **Their actions** tab shows what that person did instead.

Activity starts with the last 24 hours. Choose **Choose dates**, enter the start and end in your
time zone, then choose **Read activity** to look further back. Editing dates does not query the
trail; **Cancel** discards those edits. Applied dates stay in the link when you switch tabs or
reload. If the installation refuses a long period, shorten it and read again.

The displayed dates describe the result already read. Returning to the tab keeps that snapshot;
**Read activity** refreshes it. **Clear dates** returns to the recent period. An empty recent
period does not mean the resource has never had activity.

## Audit read not confirmed

Successful reads of this trail and a resource's Activity also record access. If an answer is lost,
the read may already be recorded. Keep the request ID when one is shown. Read again starts a new
request. The page does not retry by itself when you return to the tab or the connection comes back.

When another page cannot load, the events already read stay visible with a warning that the result
is incomplete. Read again retries that page. If its cursor is no longer valid, Read from the start
opens a new first page; the old pages stay until that read succeeds. A permission refusal removes
the earlier events from view. Ask an administrator to check access before trying a new read.
