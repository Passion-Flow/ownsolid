# 0019: Theme and density follow the account

Status: accepted.

## Context

UX-118 asks that theme and density follow a user across browsers. The console kept both in
`localStorage`. The server's `user_preferences` row held a fixed `light` or `dark` theme,
seeded as `light` for everyone, so it could not say "this user never chose", and it had no
density. `PATCH /api/v1/preferences` required the whole record.

## Decision

- **The account decides, and `system` is a stored value.** The theme gains `system`, the
  default, meaning each browser follows its operating system. Migration 000034 adds it and
  `density`, and moves rows still at revision 1 to `system`: they were seeded, never chosen.
- **`PATCH` names only what changed.** A browser that changes density must not overwrite a
  theme another browser just saved. `DecodePatchObject` allows fields tagged `omitempty` to
  be absent and still refuses null, unknown and duplicate fields.
- **The browser keeps a copy, not the truth.** `localStorage` lets a page open without a
  flash and styles pages before sign-in. Once signed in, the account's values replace it
  when a page opens and when the user returns to the window. There is no push channel for
  this: the change is not urgent, and a read on focus covers every case a person can see.
- **One save at a time, latest intent wins.** Saves carry the revision they were based on,
  so the client queues them and merges changes made meanwhile. On
  `PREFERENCE_REVISION_CONFLICT` it applies the same change on the newer revision, once. A
  change the server has not accepted blocks reads from replacing it, and a failed save shows
  a retry.
- **PostgreSQL only.** The MySQL and Dameng chains read as comfortable and refuse `system`
  and `compact` before any write, like agent pacing (ADR 0013). CMP-201 and CMP-202 add them.

## Consequences

- A choice made on the sign-in page of a new browser lasts until sign-in; then the account
  applies. This is the rule that keeps two browsers from disagreeing.
- Existing rows at revision 1 move to revision 2 in the upgrade, so a client holding
  revision 1 reloads through the conflict path.
- The toggles switch between light and dark only. Choosing `system` again needs a control
  with three options, which belongs on the account page (UX-119).
