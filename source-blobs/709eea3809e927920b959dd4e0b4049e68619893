# SSO connector and flow authority

This owner manages configured connectors, account bindings, protected flow state and the identity session's source. The current generated provider contract is Gitea with S256 PKCE; the code must not be described as arbitrary OAuth/OIDC-provider support.

[provider.go](provider.go) performs the bounded provider interaction; [provider_generated.go](provider_generated.go) comes from the provider contract generator. [flows.go](flows.go), [start_repository.go](start_repository.go), [claim_repository.go](claim_repository.go) and [completion_repository.go](completion_repository.go) separate start, exclusive claim and completion. A claimed flow is not a completed login.

[crypto.go](crypto.go), [private_file.go](private_file.go) and [keyring_operator.go](keyring_operator.go) manage protected material; [maintenance.go](maintenance.go) owns bounded cleanup. Source/binding changes must revalidate existing sessions through identity.
PostgreSQL [migration 17](../../../migrations/postgresql/000017_sso.up.sql)
adds these owners after the immutable sixteen-entry prefix and retains local sessions.

Failures, replay, stale connector revisions, result loss and keyring changes retain explicit outcomes; never resend uncertain authentication mutations or accept another callback origin to “repair” a failure.

Synchronize the [versioned SSO contract](../../../../../api/openapi/v1/sso/openapi.json),
identity/session authority, typed service configuration, the [SSO frontend](../../../../../web/README.md)
and deployment callback mapping.

Focused unit check: go -C services/control test ./internal/modules/sso. Real provider/browser/SQL/maintenance and start/completion commit-loss cases require their separate fixtures; a provider double is not live provider qualification.

Identity and SSO now share the validated transport-derived cookie issue/read/clear
owner. Current enabled provider/callback configuration still requires HTTPS;
explicit HTTP can use local accounts but cannot enable this SSO profile. A trusted
proxy does not bypass state, PKCE, Origin/CSRF or durable flow/session-source checks.
Existing historical IP-URL/DNS-Host fixtures need proper DNS/SNI/SAN/CA migration
before new current-source SQL qualification; see the [transport increment](../../../../../docs/execution/mvp/deployment-transport-boundaries.md).

## Administration surface

The console's Single sign-on page uses the connector routes plus two more.
`GET /api/v1/admin/sso/configuration` reports what the installation allows and opens no connection.
`POST /api/v1/admin/sso/connectors/{id}/tests` ([connector_check.go](connector_check.go),
[provider_check.go](provider_check.go)) sends Gitea's token endpoint an authorization code that cannot
exist, over the sign-in transport and its exchange slots, and reads which of client ID, secret or
callback it refused. Only Gitea's exact refusal of the code passes, so a changed wording reads as
`unexpected_answer`, never as a pass. It changes no connector, flow or session and is audited as
`sso.connector_tested`. The reasoning, and why introspection cannot do this, is in
[the research note](../../../../../docs/research/sso-page.md).

## Native Dameng repository boundary

The repository files now select explicit PostgreSQL, MySQL or native Dameng SQL;
[dameng_repository.go](dameng_repository.go) owns native projections, exact time
admission and bounded literal limits. No statement translator or implicit
PostgreSQL fallback is used. Native shared-authority fences use the stronger
`FOR UPDATE OF <column>` in the existing writable transaction and lock order.
Statement failure returns immediately; a poisoned native transaction is never
used to reconcile an insert conflict. Connector/binding intent JSON binds as
text, while fingerprints, nonces and ciphertext remain binary. Application UUIDs,
single-row confirmation and same-transaction postimages retain mutation identity.

[dameng_secrets.go](dameng_secrets.go) reads startup ciphertext in ordered,
bounded pages under the existing catalog page capacity. It preserves the
connector/pending-flow inventory caps without exceeding the native driver's
10,000-row result ceiling. Each page is closed before the next allocation;
decryption errors or nonprogress abort startup, and decrypted bytes are cleared.
The existing operation deadline bounds the entire scan. This startup read is not
a cross-process snapshot or live-provider readiness assertion.

Native connectors, bindings, flows, immutable intents, session source columns and
single-issuance constraints are owned by the migration coordinator's
`account_license_v2` fragment. This module adds no DDL, configuration authority,
provider, account provisioning or email-based binding. Identity's actual
`transaction.FromDameng` store and SourceGuard must be composed together; a native
repository implementation alone does not enable the serving engine selector.

Claim/start/completion failures release no private DTO or session. Bounded commit
outcome inspection precedes ordinary cancellation; an uncertain completion does
not issue a second cleanup mutation. Restore invalidation borrows its caller's
transaction. Maintenance validates limits and UTC microsecond cutoffs, locks a
bounded native row set, and retains terminal flows referenced by sessions.

[dameng_native_integration_test.go](dameng_native_integration_test.go) provides an
opt-in native fixture for connector intents/catalog, local-password linking,
one-time claim/completion, source sessions, unlink/reactivation, credential
rotation, cleanup and restore recovery. It uses synthetic subjects directly at
the repository boundary and makes no provider request. Its post-commit wrapper
fault is not native wire-loss evidence. Unit and native tests were authored but
not run in this lane; see the [wave6 handoff](../../../../../docs/execution/mvp/parallel-wave6-account.md)
for exact admission, commands, evidence and outstanding qualification.

## Review repair: shared current-actor ordering

Authenticated claim now uses `actorTransaction`, including the previous user's
immutable source and the target connector in one canonically sorted connector
set. It takes the exact session fence before installation/flow authority.
`commitFlow` also rechecks its previous actor and credential before the late
installation fence. Identity owns the same source-before-session order for all
ordinary `RecheckWithin` callers. Current-source failures during claim retain
`SSO_BROWSER_CHANGED`; indeterminate transaction outcomes still take priority.

`dameng_lock_order_integration_test.go` extends the existing exclusive native
fixture with three controlled real-transaction races: ordinary recheck versus
bulk revocation, authenticated claim versus completion, and a claim whose source
connector differs from its target versus source revocation. It uses actual SQL
results/commits, bounded barriers and independent durable readback. The revocation
fixture executes the production lock dependency and scoped revoke statements; it
does not claim HTTP/Admin-route coverage. Configure at least three open and three
physical native connections. The fixture remains opt-in and unexecuted until the
coordinator admits the actual target. No production hook or new setting is added.


## Current cumulative M11 source integration — 2026-10-26

The explicit `sso` profile extends the accepted M10 lifecycle graph with only
Gitea account linking/sign-in. `LifecycleRuntime` retains preparation, holdout and
four-store behavior in both profiles. The exact forward catalog is67 tables/17
entries; use `upgrade-sso` with the existing runtime principal. The immutable
sixteen-entry prefix remains unchanged. Current execution and limitations are in
[the integration contract](../../../../../docs/execution/mvp/m11-cumulative-integration-20261026.md);
source integration alone is not current provider/build/installation acceptance.

The durable browser proof includes the exact configured callback origin, port,
audience and path. A changed deployment must begin a new flow. After a known
claim refusal, `result_repository.go` may read that same proof solely to select
the fixed login/account error page and clear the temporary cookie. It cannot
claim, retry an exchange or create a session. Unknown commit outcomes retain the
existing no-probe fixed recovery path. This prevents revoked callbacks from
leaving provider code/state on the browser result URL.

## PostgreSQL keyring recovery candidate

`NewWithKeyringRecovery` composes a recoverable service explicitly. The existing
`New` constructor retains strict startup. Only a missing keyring under a verified
private parent or unreadable scoped ciphertext enters recovery; invalid files,
provider trust, configuration and storage scans still fail. The loaded keys remain
fixed until restart. `ConnectorPage.UnreadableConnectorIDs` reports affected rows
to authenticated Go callers; the published HTTP response has no such field yet.

Local Admin sessions can disable affected connectors. Replacing their complete
credential pair requires a protected active key and the existing tenant, session,
revision, idempotency and audit transaction. Unreadable credentials cannot start
sign-in or authorize sourced sessions. Callback refusal consumes the pending
flow without contacting the provider. See the [research and production handoff](../../../../../docs/research/sso-keyring-recovery.md)
for acceptance and the remaining startup, API and UI owners.

## Interactive connector sign-in candidate

`NewConnectorSignInTests` checks the explicitly selected PostgreSQL diagnostic
projection. Its `Start`, `Complete` and `Get` operations bind the result to the
same current Admin session and connector revision. They work on disabled
connectors and have no binding or session-issuance path. Start retries recover
the same pending encrypted intent; a consumed exchange requires a new start.

The HTTP adapter is selected by assigning the owner to
`identityHandler.SSO.ConnectorTests`. The default handler leaves these candidate
routes closed. The proposed Admin namespace is
`/api/v1/admin/sso/connectors/{id}/sign-in-tests`: POST starts a test,
`/{test_id}` reads its result and `/callback` consumes the exact provider reply.
The start uses the existing secure Admin SSO-flow cookie; one browser has one
active SSO intent. A successful callback redirects to `/sso?test_id={test_id}`
without issuing or rotating its login cookie. Errors use a fixed first-party
redirect and existing stable codes.

The composition owner must join `ConnectorSignInTests.Run` to maintenance and
shutdown. Candidate DDL is under `testdata/connector_sign_in_tests.*.sql` for
isolated qualification. Global numbering, catalog fingerprints, grants,
backup/restore and the published HTTP/UI composition require their owners'
admission. See [the research and acceptance](../../../../../docs/research/sso-interactive-connector-tests.md)
and [ADR 0070](../../../../../docs/decisions/0070-isolate-connector-sign-in-tests-from-session-authority.md).
