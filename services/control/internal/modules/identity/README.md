# Identity and sessions

This owner handles accounts, password credentials, invitations/reset flows, audience-scoped sessions, current actor rechecks, profile and authentication recovery. It is the authority reused by other business modules; it does not make a client-provided tenant or Workspace authoritative.

## Modify through these boundaries

[service.go](service.go) holds the shared configuration, selected storage, password/key/mail and SSO-source owners. [session_repository.go](session_repository.go) owns current-session/source rechecks and session issuance in the borrowed transaction. [authentication_repository.go](authentication_repository.go), [invitation.go](invitation.go), [recovery.go](recovery.go), [profile.go](profile.go) and [limits.go](limits.go) separate the corresponding operations.

Persistent identity state begins in PostgreSQL migration 000001, with selected MySQL repository equivalents. Password/session/source versions and audit/outbox writes are part of the mutation outcome; request loss or commit-unknown must use existing recovery semantics rather than silently create another invitation/session/reset.

[http/handler.go](http/handler.go) currently also composes and dispatches most of the product. That is recorded architecture debt. A future extraction must preserve shared owner identity, route/audience checks, license policy and lifecycle joins; do not move live code during an unrelated feature change.

Resending a failed email ([mail_resend.go](mail_resend.go), ADR 0021) issues anew what it
carried: invitations go through `inviteWithin`, reset emails get a new token that replaces
the unused one. Writes that take an `Idempotency-Key` use `idempotentWithin`; a new
operation also needs its name in the `idempotency_records` check constraint.
Invitation lists and invitation mail use the tenant in the server's completed
installation state. An invitation ID or mail delivery ID from another tenant
returns the same response as an unknown ID. The invitation link itself has the
same tenant boundary during validation and registration.

Changing a password while signed in ([password_change.go](password_change.go), ADR 0023) ends
every session of the account and issues the caller a new one in the same transaction, so the
HTTP handler replaces its cookie. The route needs no license, like password reset.

A read marked `X-Ownsolid-Background: true` authenticates without moving the session's last
activity, and `GET /api/v1/me/session` says what is left ([session_status.go](session_status.go),
ADR 0024). A new poll in a page must send the header, or it defeats the idle timeout.

## Related surfaces and checks

Synchronize the [identity HTTP contract](../../../../../api/openapi/v1/identity/openapi.json),
[account research and acceptance](../../../../../docs/research/local-accounts.md),
[identity frontend](../../../../../web/README.md), [typed config](../../platform/config),
mail/localization and schema changes. SSO session-source changes also require the [sso owner](../sso/README.md).

Focused native checks: go -C services/control test ./internal/modules/identity ./internal/modules/identity/http. Current SQL, cookie/origin, mail, SSO, audience isolation and commit-loss tests require their explicit fixtures/build tags. Native tests alone do not establish those paths.

The original dispatcher now consumes validated effective transport before assets
or APIs. Identity and SSO share the mode-derived cookie policy; current durable
session audience, CSRF/origin, authorization and Workspace checks remain required.
The public runtime exposes only mode/plaintext. The [transport increment](../../../../../docs/execution/mvp/deployment-transport-boundaries.md)
records actual source/loopback tests and pending real SQL/browser/deployment gates.


The optional independent machine socket selects this owner's
`Handler.MachineHandler()` through process composition. Its private selection
precedes the browser resolver and admits existing agent/gateway families,
including registry and training/preparation transfers, plus exact bodyless and
queryless GET health/live and health/ready. The [machine health extension](../../../../../docs/engineering/deployment-transport/machine-health.md)
reuses existing readiness owners; missing dependencies never report ready. It uses
actual direct TLS/authority/SNI and rejects forwarding and browser credentials.
Both entrances retain one active-request budget and the same domain/License/
authorization instances. With dedicated ingress, browser sockets no longer
dispatch machine routes. Without it historical HTTPS Admin dispatch remains;
HTTP cannot weaken machine TLS. See the [frozen machine contract](../../../../../docs/engineering/deployment-transport/machine-ingress.md)
for exact implementation and separate source/SQL/serving qualification cases.


Current repository-tagged integrity assertions live in
current_acceptance_integration_test.go. The fixed
[identity quality owner](../../../../../tools/quality/identity/README.md) selects
actual native SQL roots and retains observed final locks/authority postimages.
These test-only additions do not change domain or transport authority. Individual
whole-case acceptance remains in the [current M01 index](../../../../../docs/execution/mvp/m01-current-cases.json).


The same fixed quality owner selects current HTTP assertions from
http/handler_test.go and http/current_account_integration_test.go. The shared test
client now supplies matching DNS SNI with its exact generated TLS certificate;
production resolver/authorization rules are unchanged. Its closed-pool outage
fault preserves original fixture cleanup. Separate helper consumers retain their
own pending actual qualification; see the [HTTP evidence](../../../../../docs/execution/mvp/current-m01-http-gates.md).


Current security tests use real labeled runtime stack observation and structured
transport-log capture only inside isolated test processes; no production profiling
hook/listener is added. Fixed quality security selection binds exact cookie,
recovery, audit and CSRF assertions. System mail-cancellation audit owns an
operational UUID distinct from its HTTP caller; tests verify exact terminal
state/target and never invent a browser actor. [Evidence](../../../../../docs/execution/mvp/current-m01-security-gates.md)
keeps pending Origin/browser/deployment assertions explicit.


The fixed Origin quality group adds valid-body missing/hostile/duplicate Origin
and cross-site refusals across all account mutations, plus explicit recovery
redirect-field rejection and configured link authority. It reuses prior binding
and success evidence without modifying runtime transport rules. [Evidence](../../../../../docs/execution/mvp/current-m01-origin-gates.md)
keeps exact local acceptance and remaining browser/deployment qualification apart.

The fixed `capacity` quality group owns
`http/current_capacity_integration_test.go`: actual default two hash workers and
16waiting places, observed through real synthetic HTTP/Argon2 work, admission
refusal/timeout/no-session and explicit recovery. This is a test-only scope;
production password cost, queue and authority remain unchanged.

`security/password.go` applies one bounded NFC normalization boundary to new
password policy, hashing and verification. The fixed4096-byte wire ceiling is
checked before conversion;128-rune/512-byte credential maxima apply afterward.
Only new-password policy enforces minimum/blocklist rules; verification keeps
generic rejection. Existing PHCs/costs, queue and session authority remain. See
the [normalization repair contract](../../../../../docs/engineering/deployment-transport/current-m01-password-normalization.md).

Email validates the existing320-byte ASCII input ceiling and ASCII grammar before
case conversion, then retains trimmed display/canonical address semantics. It
preserves plus/dot identities and ASCII punycode. The fixed quality `email` group
and security/email_test.go own unsupported Unicode/fixed-recipient regression
checks; see the [email contract](../../../../../docs/engineering/deployment-transport/current-m01-email-canonicalization.md).

Current mail HTTP tests own an actual isolated dispatcher-child crash at an
identified completion pause, followed by real TLS duplicate delivery and one-use
registration. Additional tests cover encrypted-payload failures, obsolete sources
and bounded relay outage metadata. These add no production hook. See the
[mail recovery contract](../../../../../docs/engineering/deployment-transport/current-m01-mail-recovery.md).

The current HTTP quality owner also has two narrowly selected T007/T046 closure
roots in `http/current_account_integration_test.go`: fresh-key canonical email
conflicts and known private resource IDs/overposting. They reuse actual TLS/SQL
and compare private authority plus scope/audit/idempotency postimages in memory.
They change no production authority or configuration and remain source-only until
measured execution/readback. See the [fixed HTTP assertion contract](../../../../../docs/engineering/deployment-transport/current-m01-http-gates.md#focused-t007t046-closure--2026-10-24)
for exact 404/401/400 distinctions and the existing two-leaf execution boundary.

## Native Dameng account contribution

The wave5 repository contribution adds explicitly authored Dameng statements for
bootstrap, password sessions, invitations/idempotency, profile/status, rate
admission, reset/recovery and administrative lists. Membership authority UUIDs
are generated by the application. JSON responses bind as CLOB text, timestamps
must be exactly representable at microsecond precision, and bounded list limits
are validated before SQL construction. Commit uncertainty takes precedence over
cancellation and never returns a successful session or mutation DTO.

`dameng_account_native_integration_test.go` is an opt-in real native repository
chain, gated by `cgo && odbc_manager && dameng_account_integration`. It requires
an explicitly designated empty account schema and the shared session-source
extension; missing prerequisites fail, not skip. It performs no DDL or SMTP.
See [the wave5 handoff](../../../../../docs/execution/mvp/parallel-wave5-account.md)
for exact configuration inputs, coordinator commands, retained state and pending
qualification. Source/gofmt checks are not native database acceptance. Production
HTTP composition, License and SSO qualification remain with their owners.


## Bounded account resource workload

The fixed `--group workload` reuses the current M01 builder/native PostgreSQL
owner for real TLS HTTP login/getMe/listUsers at 1/10/50 request concurrency,
with a separate existing canonical hash saturation leaf. Its declared synthetic
data, raw event reader, resource/error metrics and exact commands are in
[the workload contract](../../../../../docs/engineering/deployment-transport/current-m01-resource-workload.md).
Source availability does not mean the workload was executed; selected leaves do
not establish whole-case, sustained-memory or target-capacity acceptance.

## Current-source lock order

`RecheckWithin` itself acquires the current user, current SSO source fences and
then the exact live session. `RecheckOrderedWithin` remains a compatibility
entrance to that same owner. Ordinary profile/invitation/list/Workspace callers
therefore use the same order as modules already using the ordered entrance.
Multi-user or multi-connector operations must prelock their complete sets in
canonical order before rechecking; repeated checks retain the same authority.
A source failure returns before any session-lock statement. Local sessions keep
their user/session checks and need no provider assets.

`session_lock_order_test.go` executes a bounded connector/session contention
model with an explicit old-order negative control. This is a behavioral unit
regression, not native SQL qualification; the SSO owner also supplies controlled
actual Dameng transaction interleavings.

The explicit account profile uses `http/account.go` for a narrow account/license
route set and owning readiness. It reuses every current account service,
authorization, CSRF, cookie and audit operation. Local-only session source columns
reject external-source claims without constructing an SSO service. Unknown or
later routes fail before dereferencing absent capability owners.

Minimal `workspaces` composition routes its entire Workspace namespace directly
to the constructed M02 handler after shared CSRF/license/session admission.
That owner rejects unknown descendants; the full platform's overlapping module
predicates must not interpret a minimal-profile namespace. Enabled platform
dispatch retains its original precedence. The authenticated PostgreSQL regression
lives in `workspaces/http/disabled_routes_test.go`.

Cumulative minimal routing admits exact Workspace/domain leaves. Disabled
Workspace mutations retain Origin/CSRF validation before structured404, then stop
before actor SQL or absent-owner dispatch. GET refusals remain early. M02 and M09
composition checks exercise both refusals with actual SQL/login and positive
owner controls.


Current PostgreSQL actor/session/membership fences use `FOR NO KEY UPDATE` on
immutable user identities. This still serializes status/epoch/profile/session
mutation but permits the foreign-key `KEY SHARE` checks of concurrent node result
writes. A full `FOR UPDATE` user fence inverted with the machine-owned Workspace
lock during deployment removal. User IDs are never changed by these operations;
MySQL/Dameng statements retain their existing supported locks. The deterministic
`deployments/authority_lock_test.go` observes the actual server wait graph, verifies
foreign-key progress and continued epoch-write blocking, then executes the actual
stop/removal result path. See [PostgreSQL18 row lock conflicts](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS).


## Current cumulative M11 source integration — 2026-10-26

The explicit `sso` profile extends the accepted M10 lifecycle graph with only
Gitea account linking/sign-in. `LifecycleRuntime` retains preparation, holdout and
four-store behavior in both profiles. The exact forward catalog is67 tables/17
entries; use `upgrade-sso` with the existing runtime principal. The immutable
sixteen-entry prefix remains unchanged. Current execution and limitations are in
[the integration contract](../../../../../docs/execution/mvp/m11-cumulative-integration-20261026.md);
source integration alone is not current provider/build/installation acceptance.

## Cumulative M12 source integration

The explicit `knowledge` profile retains M01–M11 (SSO source validation and joined
maintenance, preparation/holdout and all four stores) and adds only the existing
knowledge owner. `SSORuntime`/`LifecycleRuntime` select inherited responsibilities;
exact `SSOOnly`/`LifecycleOnly` predicates remain distinct. Forward account18 and
`upgrade-knowledge` preserve the17-entry prefix/existing principal; the expected
catalog is74tables/18entries, subject to actual current SQL verification.
Protected `knowledge-policy.env.example` maps every installed reference/policy
through the existing typed `--env-file`, with strict booleans and a protected
gateway inventory. The cumulative restore calls knowledge last in the same
transaction. See the [current integration contract](../../../../../docs/execution/mvp/m12-cumulative-integration-20261026.md)
for pending executable evidence; source integration alone is not acceptance.

## Current cumulative M13 source integration

The explicit `workflows` profile extends accepted knowledge through forward
account19, six schema1 workflow tables and `upgrade-workflows`. KnowledgeRuntime
retains knowledge plus all inherited SSO/lifecycle/four-store owners. Current
workflow SQL uses the original schema1 fields; later plugin/tool result columns
are absent and schema2 is refused. Startup owns the joined workflow executor;
readiness and exact HTTP routes use this owner. Common restore appends workflow
invalidation last in its existing transaction. The expected80table/19entry
catalog, role-preserving interrupted upgrade and late-owner rollback require
actual current SQL evidence. See [the current integration](../../../../../docs/execution/mvp/m13-cumulative-integration-20261026.md); source availability is not acceptance.

## Full PostgreSQL CPU profile boundary

The explicit `mvp-postgresql-cpu-v1` profile uses the existing complete HTTP graph,
with startup/readiness strict runtime-role admission owned by cmd/server. It does
not enter the bounded account/release branches. Public SSO callback origins use
`FullPlatformRuntime` and retain exact canonical deployment values, duplicate
suppression and the original HTTPS/provider restrictions. No provider keys or
private configuration are projected. The full readiness owner still verifies the
configured ReleaseView's actual stored projection; a missing or invalid owner
cannot become ready. See the [central activation contract](../../../../../docs/engineering/deployment-transport/mvp-postgresql-cpu-v1-activation.md).

## Installation audit-read access

The existing Admin audit route calls `ListAuditEventsForRequest` with its
server-generated correlation ID. Its standard and verified-assistant views
reach the same current-actor transaction in `listForActor`. The bounded page is
read before `audit.events_read` is appended; only a confirmed transaction can
release it. Append failure, cancellation and commit uncertainty return no page
or assistant attribution. An explicit retry is another accounted read.

The access event contains the current human actor, installation scope, page
limit, returned count and selected representation. It contains no original
rows, free-form reasons, raw query, cursor, email filter or protected metadata.
The public page retains its existing allowlist. Use the existing `category=audit`
filter to inspect the new family; the exact-action enumeration remains with
the HTTP contract owner. Authenticated Go callers of the global audit list
also reach this append, using a fresh operational UUID when no HTTP ID exists.

The explicit workspace Go view still needs SCI's scoped access writer; the
installation/default-workspace append cannot supply that attribution. Action
localization, contract metadata and installer collection remain with their
owners. See [the identity consumer research](../../../../../docs/research/identity-audit-read-access.md)
for acceptance and these remaining AUD-108 boundaries.
