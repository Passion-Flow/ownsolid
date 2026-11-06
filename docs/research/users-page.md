# Users page

Research behind IAM-115: the administrator's list of accounts, invitations, and disabling an account.

## How others solve it

- **Keycloak** ([server administration guide](https://www.keycloak.org/docs/latest/server_admin/index.html),
  "Managing users"): users are created by an administrator, by self-registration or by federation;
  search matches exact or partial values and searches federated stores too; deleting a user deletes
  their profile and data, so it is a separate, confirmed action.
- **GitHub organizations**
  ([inviting users](https://docs.github.com/en/organizations/managing-membership-in-your-organization/inviting-users-to-join-your-organization)):
  an invitation is its own object, distinct from a member. It expires after seven days and can be
  edited or cancelled until it is accepted; expired and failed invitations are listed apart and can be
  retried or cancelled, also in bulk.
- **GitLab** ([moderate users](https://docs.gitlab.com/administration/moderate_users/)): the admin
  list filters by state. "Block" keeps the account's data and stops sign-in, and unblocking restores
  it; deletion is separate and asks for the username to be typed. An account that is a group's only
  owner cannot be deleted.

## Choices

- **Accounts and invitations on one page,** with invitations above the list while any wait. They are
  different objects, as on GitHub, but an administrator looking for a person looks in one place.
  Pending and expired invitations show; accepted ones are accounts, revoked ones are history.
- **Disabling asks for a reason and offers undo.** It ends the account's sessions at once and keeps
  its data, like GitLab's block, so it is reversible; the reason goes to the audit trail. Enabling
  happens at once. The installation's first administrator cannot be disabled (`PROTECTED_ACCOUNT`), and
  the page does not offer disabling your own account.
- **Revoking an invitation is typed.** Its link cannot be brought back, so the person types the address
  (ADR on undo and confirmation, [research](undo-and-confirmation.md)). The email page and this page
  share one dialog.
- **Resend uses one idempotency key per invitation until it succeeds,** so a retry cannot send two
  emails, as the email page does.
- **Filter on the server:** status and an email prefix, which the API already serves. Deleting accounts
  is not offered: the API has no deletion, and IAM-101 owns the account lifecycle.

Rejected: a separate invitations page, which splits one task in two; disabling without a reason,
which leaves the audit trail without the why.

## Licences

No new dependency.

## Failure modes and tests

- Disabling the first administrator: `PROTECTED_ACCOUNT`, explained; browser flow and live run.
- An invitation to an address with an account or a pending invitation: `ACCOUNT_EXISTS` or
  `INVITATION_EXISTS`, explained with the next step; browser flow.
- A resend after the invitation was accepted or revoked: `INVITATION_STATE_CONFLICT`, and the list
  catches up; browser flow.
- A stale version on disable: `VERSION_CONFLICT`; browser flow.
- A failed resend retried with the same key; browser flow.
- The portal cannot reach the page; browser flow, and the server refuses it (existing identity tests).

## What done means

From the navigation an administrator lists accounts, filters them by status and email from a link,
opens an account's details, disables it with a reason and undoes that, enables it, invites someone,
sees the invitation and its email's state, resends it and revokes it, in both languages, with
loading, empty, error and permission states.
