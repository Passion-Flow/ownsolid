# Bind SAML results to request and browser authority

Status: accepted target design, qualified through external browser transport,
current-actor request claiming and signed-result staging experiments. Formal
product admission and local authentication completion remain pending.

## Context

An IdP form POST is cross-site. A dedicated SameSite=None proof can arrive
while ordinary Lax/Strict session cookies do not. A no-referrer policy can
also produce Origin null. Neither a claimed request nor a valid signed
persistent NameID alone authorizes a local account binding or session.
Rechecking only a captured server session cannot detect a browser that has
switched accounts while the old session remains active.

## Decision

Start through ordinary first-party Origin/CSRF and current-actor checks.
Use a separate short-lived Secure, HttpOnly, host-only SAML flow proof;
keep ordinary session-cookie authority and transport policy. Bind the request
to the installation-resolved tenant, entrance, exact configured callback,
connector revision, immutable identity context and approved metadata revision.
Account linking additionally captures actual password-confirmed actor/session,
token digest, account epoch and credential version. Callers provide no tenant
or resume-by-session-ID authority.

Claim pending requests once under the existing user, canonically sorted
source/target connector, exact session and installation fences. Recheck actual
current actor and source/credential authority even when the IdP POST has no
ordinary session cookie. Release no worker handle after failed or unknown
COMMIT, and never automatically resend an uncertain authentication mutation.

Use the qualified strict upstream signature/profile verifier and retain the
exact request ID it validated. Match that private proof to the claimed request
and its context/revision/digest. Reserve scoped assertion replay identity as
part of that flow's lifecycle. Resolve the exact external identity, store the
minimized verified result and random continuation digest, change lifecycle
state and append the audit atomically. Expire the result at the earliest
request, assertion or trust deadline; retain replay authority through the full
acceptance window. No assertion, attributes, key or raw continuation belongs
in a result URL, diagnostic, audit or persisted result payload.

Before local completion, use a fixed first-party endpoint to reacquire the
ordinary browser cookie and compare it with the initiating digest and current
actor. A cross-site redirect GET may carry Lax cookies while still reporting
cross-site fetch metadata and omitting Strict cookies. It cannot be assumed
to satisfy an ordinary first-party CSRF gate automatically. The admitted
completion adapter must enforce its explicit protocol/browser proof and the
existing ordinary Origin/CSRF rules wherever they apply. These invariants are
never optional configuration switches.

Binding, source session and audit must share one completion transaction with
current profile, account, credential, membership and replay authority. Admit
actual SAML session-source ownership before issuing a session. Do not invent
OAuth PKCE/client credentials, pretend a SAML request is a Gitea flow or issue
an unsourced local session to avoid that work. An IdP authentication context
does not automatically satisfy local MFA/step-up policy.

## Evidence and consequences

The [research](../research/saml-sign-in.md) records six real browser transport
cases, seven actual-Identity claim roots/31 leaves, and the subsequent combined
23-root/96-leaf native race and disconnected PostgreSQL qualification. The
combined experiment checks exact signed request/context proof, current actor
and source fences, cross-tenant refusal, one-use staging, scoped assertion
replay, audit rollback and actual COMMIT loss. The browser and signed-result
experiments are not one installed end-to-end flow.

A captured actor alone misses browser account switching. An exact IdP Origin
requirement rejects valid null-Origin forms and does not authenticate their
contents. Broadening the ordinary session cookie to SameSite=None expands its
transport without supplying the missing request, source or signature proof.
Returning verified identity claims to the browser adds an unnecessary bearer
surface. Those approaches are rejected.

Formal schema/configuration/dependency and source ownership, maintenance and
restore, public contracts, MFA composition, local completion and product UI
remain with their admitted owners. The experiments create no product route,
local binding or SAML-sourced session and do not complete IAM-104.
