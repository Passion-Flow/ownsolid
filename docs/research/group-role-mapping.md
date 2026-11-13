# Upstream group-to-role mapping and preview (IAM-110)

## Primary sources and approach

[GitLab SAML Group Sync](https://docs.gitlab.com/user/group/saml_sso/group_sync/)
links exact external group values to existing local groups and roles and
evaluates membership at sign-in. Its [LDAP synchronization](https://docs.gitlab.com/administration/auth/ldap/ldap_synchronization/)
also reconciles directory membership periodically. These are distinct freshness
models. Ownsolid must state which source established each grant and when that
authority expires, rather than treating an old login claim as a live directory.

[Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
separates roles from bindings and uses additive permissions, with explicit
privilege-escalation/bind protections. Follow that separation: an external
group match supplies a scoped binding to an existing authorized role. It does
not define a role or bypass the protected resource's actual evaluator.
IAM-107/IAM-108 remain prerequisites for custom roles, inheritance and explain.

Use the existing SSO and directory identity owners as the only group sources.
The source must be a complete IAM-103 generation or claims from an actually
verified IAM-102/IAM-104 flow. A request body, arbitrary JWT, email domain,
displayed group label or connector-test text is not membership evidence.
Implement one real Workspace role path through that Workspace's current
authority before adding more target families. A mapping helper with no serving
consumer does not implement IAM-110.

## Group identity and completeness

Scope every external group reference by tenant, connector and issuer/profile.
For a directory, use its stable group identifier from the complete generation;
keep the display name separate. For a verified token's configured string-valued
group claim, match exact values and document their provider-defined semantics.
Group-name claims are not immutable IDs: rename/recreation can change their
meaning. Do not silently convert a label into a stable directory identity or
assume that group paths have identical semantics across providers. Prefer
stable IDs where the actual source supplies them.

[Entra's group-claim guidance](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/how-to-connect-fed-group-claims)
documents bounded group claims and overage behavior that can omit the group
list. Missing, malformed, capped or overage claims therefore cannot be treated
as a verified empty membership set. No overage URL or distributed-claim URL
grants authority to contact another service. The first offline path requires a
complete bounded claim or a complete approved directory generation. Otherwise
refuse the mapping-dependent sign-in/update with a clear retryable outcome.

Distinguish a valid empty group set from unavailable membership. Bound group
count, individual value bytes, aggregate bytes and traversal work. Reject
duplicate/ambiguous claim fields and invalid types. Directory nested groups
use the bounded, cycle-checked closure of the completed generation. Never
assume a direct token group claim includes transitive membership unless the
selected provider profile establishes that contract.

## Rule and privilege boundary

A rule binds one exact external group reference to an existing role and
server-resolved target scope. Persist its revision, enabled state, creator and
reason. The current actor must be authorized both to manage that target's
mapping and to bind the selected role there; possessing a lesser role cannot
delegate greater authority. Resolve tenant and workspace from authenticated
context and owned resources, never from a client-supplied tenant. Return the
same refusal for unknown and foreign targets.

Use explicit role IDs and scope, not a global string comparison of names such
as `admin`. Multiple rules contribute bindings to the actual role evaluator.
Do not invent one total ordering for unrelated custom roles or infer that
the longest role name is strongest. Privileged platform/tenant bindings need
their actual privileged authority and required local step-up proof. Preserve
the last local recovery administrator and Workspace ownership safeguards.

Track manual and mapped grant provenance separately. A completed mapping
reconciliation can withdraw only the grants that source owns; it cannot erase
manual or another connector's grants. Disabling/removing a rule must reconcile
its managed grants and fence affected authority in the same defined lifecycle,
not leave an unexplained permanent assignment. Retain enough attribution for
operators to explain the effective result without copying source tokens.

Directory grants are refreshed only by complete generations. Login-claim grants
have a persisted validity bound and require new verified authentication after
that bound; do not persist an upstream refresh token merely for group mapping.
The server evaluator must refuse expired mapped authority on every protected
operation. Rule/role/source revisions and account/connector disable must also
invalidate stale cached authority. A failed synchronization retains the last
complete generation but does not extend the grants' established validity.
Manual local recovery authority is independent of this external freshness.

## Preview and atomic application

The preview uses an authorized local account, complete trusted membership,
current rule/role revisions and the actual target evaluator. Show matched
external groups, resulting scoped grants, effective additions/removals,
unchanged manual grants and fixed explanations for refused/unavailable inputs.
Limit the response to the caller's authorized scope. Keep source tokens,
protected attributes and other tenants' memberships out of the response and
audit payload. An explicitly hypothetical sample can never become live
membership evidence or mutate a grant.

Application rechecks the current actor, required step-up, source generation,
policy/role revisions and target scope, recomputes the result and commits its
managed binding delta with the audit event. A stale preview returns a revision
conflict and requires a new preview; it does not apply the old client-side
result. Use an idempotency key for writes, one current source reconciliation
per target and the existing canonical lock order. An audit failure rolls back
the delta. An uncertain commit exposes neither a false success nor an automatic
second mutation. No separate approval/receipt ledger belongs to this item;
IAM-203 owns two-person approval.

## Qualified inputs and remaining composition boundary

The [directory generation candidate](directory-login.md#storage-qualification)
now persists complete scoped generations and retains the last successful head
after a failed scan. Its group graph is a bounded observation with detected
churn controls, not an LDAP-wide snapshot. A mapping input must identify that
exact successful run, connector revision/auth version, stable external user and
group IDs, completion digest and unchanged validity bound. Never label an
archived graph as a fresh serving-worker observation or extend its validity
because a preview or reconciliation was retried.

The [scoped-role experiment](scoped-role-authorization.md#selected-family-qualification)
now reaches actual `ChangeNote` authorization, current membership lifetime,
role/grant versions and the note/audit transaction. It admits no managed-source
provenance or expiry yet. Copying directory rows into its ordinary manual grants
would incorrectly preserve access after source expiry and let one source replace
another source's or an operator's grant. That is not the next implementation.

The current [GitLab LDAP synchronization guidance](https://docs.gitlab.com/administration/auth/ldap/ldap_synchronization/#ldap-servers-with-rate-limits)
describes group-membership removals after a directory rate-limit error. Ownsolid
selects its already stated complete-generation policy: a partial or unavailable
scan is not an empty membership set, and failure does not refresh old authority.
Existing mapped authority remains bounded by its original validity; after that
bound it denies on the server. A truly complete empty generation may withdraw
only grants belonging to that source. This distinction needs failure and retry
coverage in the actual consumer, not only a parser or preview test.

There is a transaction-order boundary to prove before composing these candidates.
The directory owner currently takes actual current-actor/source and sorted
source/target connector fences before installation and generation state. The
Workspace owner takes actor/source, installation/tenant and then exact Workspace
fences before reading current membership. Do not append a target connector lock
after the Workspace fence, nor publish a reconciliation that inverts those
orders against connector disable or role edits. The eventual owner must establish
the complete actor/source/target set first and reuse the exact Workspace fence,
then re-read source generation and policy revisions inside the same transaction.
The existing declaration of both orders is not a measured combined wait graph.

The next admissible experiment must join the actual source, managed-grant
provenance/expiry and resource evaluator, then demonstrate a Portal note write
before/after mapping and expiry. It must preserve manual and other-source grants,
refuse stale preview and membership-lifetime substitution, retain one atomic
audit/managed delta, and observe both source-disable/resource-write lock orders
with independent PostgreSQL connections. Formal source/grant ownership,
IAM-107/IAM-108's production evaluator and public/UI contracts remain prerequisites.
No mapping code, public operation, grant or runtime dependency is introduced by
this review, and IAM-110 remains open.

## Dependencies, alternatives and acceptance

This design adds no dependency, model or dataset. Existing Go standard-library
code is BSD-3-Clause, pgx is MIT and PostgreSQL uses the PostgreSQL License;
commercial use, redistribution and offline use retain their existing notices.
GitLab, Kubernetes and Microsoft documentation are design references, not
copied code or hosted runtime dependencies. Provider-library licenses remain
in the OIDC, SAML and directory research notes.

Reject email/domain auto-grants, unscoped group names, raw claim submission as
proof, blanket replacement of manual membership, partial-source removals and
browser-only preview enforcement. Do not add an independent evaluator that
the protected Workspace never calls or synchronize directly into a single
unattributed role column.

The finished item needs real PostgreSQL and actual provider/directory evidence,
an authorized Admin preview/configuration flow and a Portal operation whose
permission really changes. Test the exact cross-tenant connector, group,
account, rule, role, target and preview substitutions on every public operation;
cross-audience and revoked/expired actors; stale step-up and policy revisions;
claim omission/overage, full empty generations, nested cycles and interrupted
sync; manual-plus-multiple-source grants; removal/disable/expiry propagation;
last-owner safeguards; concurrent reconcile and idempotent retry; audit refusal
and actual wire COMMIT uncertainty. Run disconnected and verify the original
unprivileged account cannot perform the protected operation before a valid
mapping or after its authority expires.

Public contracts, formal source/grant schema and UI admission must move with
the actual Workspace consumer. Until those prerequisites and provider paths
are qualified, keep IAM-110 open and preserve the existing local roles.
