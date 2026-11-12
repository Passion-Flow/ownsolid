# Scoped roles and resource authorization (IAM-107, IAM-108)

## Sources and existing authority

[Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
separates role rules from scope-bound assignments and checks escalation when
roles are created or bound. [GitLab custom roles](https://docs.gitlab.com/user/custom_roles/)
expose named permissions and distinguish member and administrative authority.
[Kubeflow profiles](https://www.kubeflow.org/docs/components/central-dash/profiles/)
combine user-facing workspaces with namespace and access-control ownership.
Use those separation and scope patterns; none makes a browser-supplied tenant
or upstream group claim authoritative for ownsolid.

The current [Workspace owner](../../services/control/internal/modules/workspaces/README.md)
resolves the installed tenant and exact Workspace, waits for its owning lock,
then reads current membership. The [public contract](../../api/openapi/v1/workspaces/openapi.json)
admits only `owner`, `member` and `viewer`. Platform Admin status is a separate
account property and the Admin entrance has its own session audience. New role
rows or another identity-only evaluator would leave those real operations and
published enums unchanged.

Preserve current membership lifetime IDs, last-owner protection, disabled
Workspace policy, current session/source checks and tenant isolation. Do not
reinterpret a tenant admin or custom role as `is_platform_admin=true`, and do
not infer its authority from its display name. A new Admin-console role also
needs explicit entrance authorization and the actual operation's permission
checks; a role label alone cannot open that entrance.

## Chosen integration approach

Use explicit, stable action identifiers and normalized persistent role versions
and scope-bound grants. Built-in roles are documented action sets; custom roles
select admitted actions rather than arbitrary source code, URLs or SQL. Unknown
actions deny. Adding a future action does not silently expand a saved custom
role through a wildcard. Keep the roadmap's platform admin, tenant admin,
workspace admin, developer, operator, member, viewer and auditor roles distinct,
with an explicit permission matrix covering their actual operations.

The first resource family should be existing Workspace notes and membership
operations, whose service, transaction and UI flows already exist. Bind role
and resource grants to the installed tenant and the actual Workspace. Resolve
the note's parent through its persisted composite scope; no requested parent,
tenant, role name or stale `Auth.Me.Workspace` value can supply that ownership.
Resource grants inherit only along an admitted, server-owned parent chain.
An inherited grant cannot leave its tenant or Workspace, revive a disabled
account or broaden the session audience. Reject ambiguous, cyclic or unsupported
parent relationships before exposing a resource.

Existing Workspace operations already hold shared installation/tenant fences
before locking the exact Workspace and reading membership. Reuse that ordering
for inherited role authority, and serialize corresponding policy/grant edits
through the owning scope before the affected resource locks. Concurrent normal
reads should retain shared fences; a new installation-wide exclusive lock on
every authorization check would undermine the fleet design. Prove the actual
wait graph with the resource and protocol owners before admitting a changed
lock order. Re-read role revisions after waiting, in the same transaction that
authorizes the mutation and appends its audit event.

Role creation, permission changes and assignment are themselves privileged
operations. Require current grant-management authority and prevent assigning
permissions the actor cannot delegate at that scope. No custom role can acquire
platform administration, factor recovery, license administration or another
tenant's authority by adding an action string. Apply the common sensitive-
action step-up policy when that path is admitted; no special role-edit login
or configurable authorization bypass is selected.

Use the same evaluator and authoritative inputs for enforcement and explain.
Explain returns the selected action/resource, outcome, current role/policy
revisions and bounded provenance for the grants that justify it. It must not
become an enumeration API for other users, hidden resources or foreign roles.
Own-decision explain and administrative explain require distinct explicit
authority. Missing and foreign scopes retain the owning resource's uniform
refusal. A UI preview is informative; the actual write checks current state
again and can refuse after a concurrent revocation.

## Alternatives, dependencies and failure coverage

Do not replace the existing current-actor/Workspace fences with a browser
capability list, JWT role snapshot or cached allow decision. An out-of-process
policy service would add another availability and consistency boundary before
there is one accepted resource path. The first normalized RBAC evaluator can
use existing Go and PostgreSQL, with no new policy SDK, model or dataset. More
expressive attribute/relationship rules remain IAM-202; their later admission
must preserve these scope and revocation rules rather than change P1 defaults.

Existing Go BSD-3-Clause, pgx MIT, Google UUID BSD-3-Clause and PostgreSQL License
terms allow commercial use, redistribution and disconnected operation with
their notices. Kubernetes, GitLab and Kubeflow are design references; no code,
model, dataset or deployment artifact is copied or introduced by this note.
Any future policy dependency needs its own exact-version graph and license
review before implementation.

## First selected-family experiment

Prototype note update/delete before adding production role interfaces. Keep
the repository unchanged: a private Go source overlay can replace only this
owner's note permission selection and insert the candidate evaluator into the
actual `ChangeNote` transaction. Reuse its current actor/source, installed
tenant, exact Workspace and note locks, optimistic version, update and audit.
Do not inspect private SQL statement fields or add a production testing hook.
The overlay and candidate SQL remain outside the product until the approach
and shared contracts are admitted.

Use allow-only action sets and scope-bound assignments; union the admitted
actions of current grants. Unknown actions and missing grants deny. A note
grant names the note under its persisted tenant/Workspace; a Workspace grant
inherits only to its actual notes. Role labels convey no authority. Bind each
assignment to the current immutable membership lifetime so removing/re-adding
the same user cannot revive it. Role/assignment edits use the existing exact
Workspace fence and current owner authority, with bounded delegation. An
explicit grant may authorize a current viewer to update the selected note;
membership still cannot be skipped. Platform administration remains its
existing distinct server property and audience.

Use the same evaluator for the candidate's own-decision explain and the actual
overlaid write. Observe both competing transactions with an independent
PostgreSQL connection: a grant edit that owns the Workspace fence first must
make the waiting writer observe the new decision; a writer that already owns
it may commit before the waiting edit. Record exact note/grant versions and
audit postimages, not elapsed sleeps. Test rollback, cancellation and real
commit uncertainty before exposing a mutation or explain response.

This experiment does not implement note creation, read/list filtering, the full
built-in role matrix, general role management or their public/UI contracts.
The eventual migration must seed explicit equivalent built-in grants for all
existing memberships before replacing any legacy permission check. A partial
overlay cannot qualify platform-wide resource permissions or change current
production behavior. No experimental evaluator or SQL has run at this checkpoint.

Verify real PostgreSQL role/grant persistence, current membership and exact
resource ownership. Cover foreign tenant, Workspace, role, subject, binding
and note substitutions at each actual operation. Exercise role and grant
revocation during a real lock wait, concurrent policy edits, delegated-grant
escalation, last-owner races, stale versions, disabled subjects, revoked
sessions, unsupported actions and forged actor context. Audit refusal and
uncertain COMMIT must expose no accepted mutation or explain result that
claims an unconfirmed new policy. Explicit retries follow the existing owned
mutation/idempotency contract.

## Concrete completion and owner boundaries

A user must create and inspect a custom role, assign it to a real member,
exercise one allowed and one denied operation, inspect the corresponding
explain view, revoke the grant and observe the actual operation refuse it.
The UI must cover all named built-in roles, resource inheritance, loading,
empty, denial, conflict and retry states in both languages. Run the complete
flow with the network disconnected and after restart. Then integrate and
qualify the remaining actual resource owners against the documented action
matrix; a notes-only evaluator does not finish platform-wide roles.

ARC owns schema/catalog and typed startup admission. DEV owns the role,
assignment, explain and changed Workspace contracts; Web owns their real
forms and permission states. Each resource owner must invoke the same current
authority in its real transaction. The first selected-family prototype must
prove grant-edit and resource-write serialization before product integration.
No new role, permission, schema, route or acceptance result is claimed here.
