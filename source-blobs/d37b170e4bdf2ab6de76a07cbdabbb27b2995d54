# Workspaces page

Research behind IAM-116: the administrator's page for workspaces and who belongs to each.

## How others solve it

- **GitLab** ([members of a project](https://docs.gitlab.com/user/project/members/)): a table shows each
  member's account, source, role, expiration and activity, and a direct member's role is changed in
  place. Adding takes an address and a role; removing is a "Remove member" action on the row, and only
  an Owner can remove an Owner. The page states no rule for the last owner.
- **GitHub** ([roles in an organization](https://docs.github.com/en/organizations/managing-peoples-access-to-your-organization-with-roles/roles-in-an-organization)):
  owners assign roles to people and teams, and the owner role "should be limited, but to no less than
  two people", which is guidance rather than an enforced rule on that page.
- **Kubernetes** ([namespaces](https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/)):
  a `default` namespace exists so a new cluster can be used at once. Only part of this page could be
  read verbatim, and it was not checked whether `default` can be deleted.

No source read describes a protected last owner or a protected default scope as a rule. Both come from
this repository: `LAST_WORKSPACE_OWNER` and `DEFAULT_WORKSPACE_PROTECTED` already exist on the server,
and the page explains them.

## Choices

- **A list and a page per workspace,** not a drawer: the members are a table of their own, and an undo
  toast needs no modal to close first.
- **Every change states a reason,** as the API requires, so the audit trail says why. Renaming,
  disabling, a role change and removing a member can all be taken back, so each offers undo and none
  asks to type a name ([undo and confirmation](undo-and-confirmation.md)). Undoing a removal adds the
  person back with the role they had.
- **The server's rules are explained, not copied.** The last owner (`LAST_WORKSPACE_OWNER`) and a stale
  version (`VERSION_CONFLICT`) come back as notices with the next step, and the page reloads the row.
  The default workspace does not offer disabling, since it would always be refused.
- **An owner is chosen by address.** The API takes a user ID; a person knows an address. The form starts
  with the signed-in administrator and looks up another address exactly, saying so when no account
  has it, with the Users page a step away.
- **No server filters yet.** The API lists all workspaces in pages; an installation has few, and name
  search joins the list when a real one outgrows that.

Rejected: deleting a workspace, which the API does not offer because models and nodes hang from it;
disabling only, without undo, which makes a reversible action look final.

## Licences

No new dependency.

## Failure modes and tests

- A name in use: `WORKSPACE_EXISTS`, explained in the form, which keeps what was typed; browser flow.
- An address with no account: said in the form on create and on add; browser flow.
- Someone already a member: `MEMBERSHIP_EXISTS`, explained; browser flow.
- Removing or demoting the last owner: `LAST_WORKSPACE_OWNER`, explained; browser flow and live run.
- A stale version: `VERSION_CONFLICT`, and the page reloads; browser flow.
- The default workspace: no disable offered, and the server refuses it; browser flow, existing tests.
- A link to a workspace that does not exist: says so, with a way back; browser flow.
- The portal cannot reach the page; browser flow, and the server refuses it (existing tests).

## What done means

From the navigation an administrator lists workspaces, creates one with an owner, renames it, disables
and enables it, adds a person, changes a role and removes someone, with undo and a reason each time, in
both languages, with loading, empty, error and permission states, and the workspace switcher follows.
