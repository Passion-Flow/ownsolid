# Audit trail page

Research behind AUD-110: the administrator's page for reading what happened, who did it and how it
ended.

## How others solve it

- **GitLab** ([audit event reports](https://docs.gitlab.com/administration/compliance/audit_event_reports/)):
  an admin page under Monitoring, filtered by user, group, project and date range. The record names
  author, entity, target, action and IP address. "Export as CSV" exports the current view, up to
  100,000 events.
- **GitHub** ([reviewing the audit log](https://docs.github.com/en/organizations/keeping-your-organization-secure/managing-security-settings-for-your-organization/reviewing-the-audit-log-for-your-organization)):
  shows actor, affected user, action and time; filters by `actor:`, `action:` (`action:team.create`,
  and `-action:hook` to leave out a family), `operation:` and `created:` with a date range; text search
  is not offered; JSON and CSV export.
- **Kubernetes** ([auditing](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/)): records
  go to a file with `maxage`, `maxbackup` and `maxsize` limits or to a webhook with a bounded buffer
  that drops on overflow, and counters report events and errors.

Keycloak's auditing section, the OWASP logging cheat sheet and Vault's audit documentation could not
be read for this note. None of the sources read describes tamper evidence, which AUD-102 covers.

## Choices

- **Who, not only an ID.** The API returned user IDs, which a person cannot read. The list joins the
  users table for the email of the actor and the subject; a null actor is the system.
- **Filter by family, person, outcome, time and request.** A new `category` selects an action family
  (`license` is `license.*`), the reverse of GitHub's `-action:hook`, because the existing exact-action filter
  lists only some actions. A new `actor_user_id` shows one person's actions. `request_id` connects to
  the request ID that every error notice lets people copy.
- **Action codes stay codes,** the names GitHub's filters use too: they are the stable names an auditor quotes and searches
  for. Categories, outcomes and entrances are translated. A new action needs no translation to appear.
- **Time is a window the server pins.** Presets of 24 hours, 7 days and 31 days (the default
  `audit_max_days`) anchor when the list loads; the cursor carries the window, so later pages omit it
  and a page cannot shift under the reader. A custom window sits in the address as exact times, and
  an event's link uses a window around its own time, so it still opens the event next month.
- **No single-event endpoint.** An event's link names a narrow window and the row, which the list
  serves. A row outside the list says so and offers to widen the window.
- **Reads only.** Nothing on the page changes data; audit records cannot be edited or removed
  (AUD-105).

Rejected: exporting from this page, because AUD-107 requires approval and a record for exports; text
search across reasons, which GitHub also omits and which needs an index that AUD-101 should decide;
translating every action, which would hide new ones until someone wrote a string.

## Licences

No new dependency.

## Failure modes and tests

- A window longer than the policy allows, or reversed: checked in the form; the server's
  `VALIDATION_FAILED` is explained; browser flow and repository test.
- A category or actor that is not valid: refused by the server; unit test.
- A stale cursor when filters change: filters are part of the query, so paging restarts; the existing
  drift test covers the server, extended for the new filters.
- An actor who no longer resolves: the email is null and the ID still shows; PostgreSQL test.
- A link to an event outside the list: says so, with a way to widen; browser flow.
- The portal and a non-administrator: refused by the server (existing tests); browser flow.

## What done means

From the navigation an administrator reads events with their time, person, action, target and
outcome; filters by family, outcome, person, request and time from a shareable link; opens an event's
details and follows it to its request or its person, in both languages, with loading, empty and error
states.
