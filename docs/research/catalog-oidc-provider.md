# Catalog application OpenID Provider (IAM-106)

## Protocol and product authority

[OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html#CodeFlowAuth)
defines the authorization-code flow and authenticated subject claims.
[Keycloak's application integration](https://www.keycloak.org/securing-apps/oidc-layers)
exposes discovery, authorization, token, UserInfo, revocation and introspection
as separate operations. [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)
requires exact registered redirects, code-injection defenses and restricted
token authority. Follow that proven protocol shape with ownsolid's existing
account, session and Workspace authority.

The first complete path is one confidential server-side catalog application,
registered for one authorized installation, using code flow with S256 PKCE.
Discovery and JWKS are local HTTPS resources. The issuer is an explicit typed
deployment value and never derived from an untrusted Host or forwarding header.
The [catalog integration boundary](ops-lane-questions.md#cat-104-automatic-sign-in)
requires the installation owner to register and revoke the client and project
its credential reference. A valid catalog signature or client ID cannot prove
that an installation exists or that a user can access its Workspace.

Keep ownsolid's Portal login as the human authentication authority. The
authorization request becomes an expiring, browser-bound server record before
login or consent. On approval and again on code redemption, recheck the actual
current session, account epoch, tenant, active installation, Workspace and
required membership through their owners. Admin cookies do not replace that
Portal authority. Missing MFA proof must use the common factor gate; do not
issue a second provider-specific password session.

The client registration resolves its tenant, Workspace, installation and exact
callback from authorized server records. Neither a query parameter nor a token
claim supplies authoritative tenant or Workspace scope. The browser receives
only an opaque request reference and the approved application's public name
and requested claims. Consent writes require existing Origin and CSRF proof.
Validate the client and redirect before redirecting any protocol error; an
unknown or invalid redirect receives a local error.

Use a stable, opaque issuer-scoped subject, never email as an account key.
Agree its tenant/user lifetime with the identity owner before persisting the
registration model. Return only registered, consented claims. Any Workspace
claim describes the authorized installation; it cannot replace a current
permission check at a resource server. Catalog tokens are not ownsolid browser
sessions, Distribution keys or model-gateway credentials.

## Persistence, revocation and key custody

Hash high-entropy authorization codes and opaque access tokens at rest. Bind
each code to its client revision, exact redirect, S256 challenge, requested
scope, nonce, subject, source session and installation scope. Redeem it once
under an actual database lock. Token creation, code consumption and audit must
commit together; a failed or uncertain commit releases no token and performs
no automatic second exchange. A client begins a new authorization attempt
after an uncertain response. Expiry and capacity limits enter through typed
validated policy, with bounded cleanup and per-client request limits.

The first access token is opaque and revocable. UserInfo and confidential
introspection verify its exact client and current installation/user authority.
Revocation, account disable, source-session invalidation, membership removal,
client rotation and uninstall must end access without waiting for a long JWT
lifetime. An ID token remains a short-lived signed authentication statement;
it cannot guarantee immediate revocation of an application's own session.
The real application integration must define its session expiry and logout
behavior and prove that they meet the configured policy.

Signing keys are dedicated OpenID Provider keys, supplied as protected file
references. They are unrelated to entitlement verification and contain no
license-issuance material. Publish only public JWKs. Retain retiring public keys
through the maximum issued-token lifetime and allowed skew, then remove them.
Restart, replica agreement, rotation and backup/restore must preserve issuer,
subject and key continuity. No key fetch, dynamic discovery, telemetry exporter
or external clock service is needed at runtime.

Start with registered `authorization_code`, `code`, S256 and one explicit
confidential authentication method. Reject implicit/password grants, wildcard
callbacks, client-selected claims or audiences, dynamic registration and
unregistered grant types. Refresh/offline access, public/native clients and
additional logout profiles require separately admitted persistence and real
consumer coverage; advertising a library capability does not implement it.

## Library candidates and license admission

The source-inspection candidate is
[zitadel/oidc v3.52.0](https://github.com/zitadel/oidc/releases/tag/v3.52.0),
whose [OP storage interface](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/storage.go)
allows owned persistence and signing, while its
[client interface](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/client.go)
allows exact registered redirects and disabled development mode. Do not
implement its optional redirect-glob interface. Its
[README](https://github.com/zitadel/oidc/blob/v3.52.0/README.md) identifies RP
certification; that is not certification of ownsolid's proposed OP composition.

The library is [Apache-2.0](https://github.com/zitadel/oidc/blob/v3.52.0/LICENSE),
allowing commercial use, redistribution and offline operation with preserved
license, attribution and applicable notices. Its
[module](https://github.com/zitadel/oidc/blob/v3.52.0/go.mod) requires Go 1.26,
JOSE v4.1.9 and OAuth v0.38.0, as well as routing, form, cookie and telemetry
packages. This is a larger graph than the relying-party prototype. No library
is admitted or added to the product by this note. Before any prototype build,
resolve the exact complete graph outside the product and inspect every
dependency license, notices, security patches and offline behavior. Retain the
inventory here before selecting that graph; a top-level Apache license does
not establish the licenses or network behavior of its dependencies. No model
or dataset is involved.

Source inspection exposes a required transaction prototype: the legacy
[token response](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/token.go)
creates access state, signs the ID token and only then deletes the authorization
request. These storage calls do not themselves establish one database commit.
Its opaque bearer is an encrypted token ID and subject, not the random token
value returned directly by storage. Qualify an explicit owned transaction and
bounded response release before accepting that composition, and specify which
random credential identifier is hashed in the store. The newer
[Server interface](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/server.go)
is explicitly experimental; a fresh interface must not be mistaken for a
proven transactional implementation. A signing or deletion failure, two
redeemers and a lost commit acknowledgement are required rejection controls.

[Ory Fosite](https://github.com/ory/fosite) is an alternative OAuth/OIDC SDK
with owned storage interfaces. Compare its code-consumption and transaction
composition against the same real PostgreSQL tests if the candidate cannot
preserve atomic ownsolid authority. Do not choose a handwritten OAuth server:
JWT signing alone omits redirect, grant, request, consent and replay rules.
Running another complete identity server as a mandatory catalog dependency
would introduce another user/session authority and deployment lifecycle; the
first path should instead integrate a protocol library with ownsolid's owners.
The candidate still needs an external prototype before product implementation.

## Failure coverage and concrete completion

The external prototype must use separate real RP and OP HTTPS servers, verified
private CA trust and actual PostgreSQL persistence. Prove discovery through code
exchange, signed ID-token verification and matching UserInfo subject; then
restart the OP and repeat with retained client and subject identity. Verify
wrong client/secret, redirect, issuer, PKCE, nonce, source browser, expired code,
simultaneous redemption, lost COMMIT reply, signing-key rotation and bounded
request bodies. Token and authorization pages use `no-store`; logs and metrics
contain no code, token, secret, cookie, raw claim set or credential-bearing URL.

Product acceptance additionally needs a real installed catalog application:
an authorized operator registers it, a Portal user signs in without re-entering
credentials, and the app obtains only its allowed identity. Substitute another
tenant's client, installation, consent request, code and token at every public
operation. Observe refusal and unchanged committed grants, not only a hidden
UI control. Concurrent disable, membership removal and uninstall must be
observed at the actual exchange and UserInfo boundaries. Show clear retryable
errors without opening an invalid callback.

Finally exercise the same flow in a rendered browser with the network
disconnected, including consent refusal, expired requests, client credential
repair and sign-out behavior. ARC owns formal schema, configuration and key
startup admission; Catalog/Delivery own the installation identity and secret
projection; DEV owns the public protocol contract and Web owns consent and
registration screens. An unconnected SDK, synthetic JWT or discovery page
does not finish IAM-106.
