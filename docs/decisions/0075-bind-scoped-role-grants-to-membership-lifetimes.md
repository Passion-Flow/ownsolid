# Bind scoped role grants to membership lifetimes

Status: accepted target design, validated for the external note-write experiment;
production schema, interfaces and serving admission remain pending.

## Context

The current Workspace contract has owner, member and viewer roles. New named
and custom roles must reach the actual resource transaction, survive revocation
and preserve tenant isolation. A role name, browser capability list or cached
session claim cannot authorize a write. Removing and re-adding a member must not
restore permissions granted to the previous membership.

## Decision

Use normalized persistent role versions and allow-only assignments with explicit
stable action identifiers. Union the admitted actions of current assignments;
unknown actions and missing assignments deny. Bind assignments to immutable
membership authority IDs and the resource's server-resolved tenant/Workspace.
Inherit only through an admitted persisted parent chain. Keep platform Admin
status and audience as their existing distinct server authority; a custom role
or its display name cannot acquire that property.

Current actor/source, scope, policy and resource checks share the mutation's
selected transaction. Role/grant edits and affected resource writes serialize
through the existing exact Workspace fence before the resource locks. Preserve
shared fences for normal reads; do not add an installation-wide exclusive lock
to every permission check. Restrict delegation to actions the current manager
may grant at that scope and apply the common sensitive-action factor gate when
the management operation is admitted.

Use one evaluator for enforcement and own-decision explain, with bounded current
role/grant provenance. Release no mutation DTO or explain decision on failed or
uncertain commit. Seed explicit equivalent built-in grants for all existing
memberships before replacing legacy permission checks. Integrate and qualify
every resource owner against the full action matrix before claiming platform-
wide role support.

## Evidence and consequences

The [research and external experiment](../research/scoped-role-authorization.md#selected-family-qualification)
record four PostgreSQL roots and thirteen leaf cases passing native race and
disconnected Linux arm64 runs. The actual note writer observed grant revocation
after its lock wait, kept note/audit atomic, rejected foreign scope and old
membership lifetimes, and released no result after actual lost COMMIT replies.
The private source overlay adds no production hook or runtime setting.

An out-of-process policy SDK would introduce another consistency/availability
owner before the first resource family is admitted. Cached/JWT role decisions
would miss current revocation. Wildcards would silently expand stored custom
roles as new actions appear. These are not selected for the first path.
More expressive attribute/relationship policy remains IAM-202 and must preserve
the same scope and outcome rules.

The experiment does not admit the public role/assignment/explain APIs, role
matrix, formal migration, database grants, UI, step-up composition or installed
flow. Those shared owners retain their contracts. IAM-107 and IAM-108 remain open.
