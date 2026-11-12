# Generic OpenID Connect sign-in (IAM-102)

## Sources and approach

[Keycloak's identity broker](https://www.keycloak.org/docs/latest/server_admin/index.html#_identity_broker)
uses OpenID Connect authorization code flow and gives each connector a separate
callback. [OpenID Connect Discovery](https://openid.net/specs/openid-connect-discovery-1_0.html)
binds metadata to a configured issuer. [OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation)
defines ID token signature, issuer, audience and nonce validation.
[OAuth Security BCP, RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)
requires defenses against code injection, mix-up and redirect attacks.

Use a provider adapter in the existing SSO owner and preserve active Gitea
bindings and their session revocation behavior during upgrade. Use
authorization code flow with S256 PKCE and single-use state and nonce bound to
the connector, browser, tenant and entrance. For a generic connector, use
operator-approved private TLS issuers and bounded metadata and JWKS requests
through the existing transport restrictions. An ID token must match the
registered issuer and client, pass signature and time validation, and bind to
the flow nonce. If UserInfo is used, its subject must match the ID token.
Resolve bindings by issuer and subject, with explicit proof of control of the
local account when linking. Email equality alone cannot link an identity.

The current Gitea adapter uses proprietary user and token endpoints. Reusing
that profile for other providers or accepting an unverified JWT would leave
authentication dependent on provider-specific behavior or unsigned claims.
Implicit flow puts tokens into the browser redirect. Permit no externally
hosted identity provider dependency for the disconnected acceptance path.

## Dependencies and acceptance

The current candidate is [coreos/go-oidc v3.21.0](https://github.com/coreos/go-oidc/releases/tag/v3.21.0),
replacing the earlier research candidate v3.17.0. Its
[module file](https://github.com/coreos/go-oidc/blob/v3.21.0/go.mod)
requires Go 1.25 and pins the dependencies below. This design adds no product
dependency, model or dataset. Resolve and inspect the complete selected module
graph before implementation; do not infer runtime network behavior from its
license or from a successful signature check.

| Dependency | Selected version | License and offline distribution |
| --- | --- | --- |
| `github.com/coreos/go-oidc/v3` | v3.21.0 | [Apache-2.0](https://github.com/coreos/go-oidc/blob/v3.21.0/LICENSE); retain license, attribution and any applicable notices. |
| `github.com/go-jose/go-jose/v4` | v4.1.4 | [Apache-2.0](https://github.com/go-jose/go-jose/blob/v4.1.4/LICENSE); its selected module declares no further requirements. |
| `golang.org/x/oauth2` | v0.36.0 | [BSD-3-Clause](https://github.com/golang/oauth2/blob/v0.36.0/LICENSE); retain copyright and license. |
| `cloud.google.com/go/compute/metadata` | v0.3.0, required by the OAuth module | [Apache-2.0](https://github.com/googleapis/google-cloud-go/blob/compute/metadata/v0.3.0/LICENSE); module-graph dependency only, with no cloud or ambient credential composition in ownsolid. |

These licenses permit commercial use, redistribution and offline use with
their required notices. The private Keycloak acceptance fixture also needs its
exact version, image digest and dependency notices selected before provisioning.

Run a real private Keycloak realm on a disconnected network. The browser must
configure, link and sign in through it. Verify wrong issuer, audience,
signature, nonce, redirect, state and PKCE, key rotation, replay and connector
disable. Substitute a second tenant's connector, binding and flow and prove the
server rejects each without issuing a session or revealing the target.

## Adapter boundary and failure coverage

The selected [verifier source](https://github.com/coreos/go-oidc/blob/v3.21.0/oidc/verify.go)
leaves nonce validation to its caller and does not establish authorized-party
(`azp`) binding merely by checking `aud`. Ownsolid must enforce the exact
issuer, its persisted single-use nonce, subject, `azp` and acceptable times
after signature verification. A token with multiple audiences requires the
registered client as `azp`; any supplied `azp` must match. Keep issuer, client,
expiry and signature checks enabled, with no deployment switches for skipping
them. Bound JWKS work with the owned client timeout and response limits; do not
rely on a verifier context alone to cancel background refresh.

Provider discovery introduces network destinations, not permission to contact
them. The [provider implementation](https://github.com/coreos/go-oidc/blob/v3.21.0/oidc/oidc.go)
can use a caller-supplied HTTP client. All metadata, token, JWKS and optional
UserInfo requests must use the existing approved TLS/dial policy, response
limits, no-redirect behavior and shared concurrency budget. Validate discovered
endpoints against explicit approved destinations before using them. Do not
resolve distributed claims, dynamically register clients or create ambient
cloud clients. Provider errors and raw claims must not enter public error text
or logs.

Use an explicit confidential-client authentication method. The
[OAuth token implementation](https://github.com/golang/oauth2/blob/v0.36.0/internal/token.go)
can retry with another authentication style when automatic detection is used.
Do not use that mode for a single-use code: exchange once with the selected
method, and start a new flow after an uncertain outcome. Never refresh or
persist an upstream access token merely to keep an ownsolid session alive.

The current Gitea subject is a canonical decimal ID. A generic OIDC subject is
an issuer-scoped opaque string; coercing it to an integer, normalizing it or
matching its email would change identity. Keep existing Gitea bindings and
source-session validation unchanged. The OIDC storage/contract admission must
represent exact issuer and subject, a protected flow nonce, and an issuer URL
with a path such as a Keycloak realm. The existing approved network origin and
the logical issuer are separate inputs.

Before changing the serving path, prototype outside product code with a real
private provider. Include signed but wrong/missing nonce, wrong `azp`, multiple
audiences, future/expired tokens, duplicate claims, nonnumeric subjects, key
rotation and unknown-key floods. Malicious discovery endpoints, redirects,
oversized metadata/JWKS and cancellation must make no unapproved request or
unbounded retry. Keep the existing Gitea link/sign-in and revocation tests as
positive upgrade controls. Passing a standalone verifier does not establish
the storage, callback, binding, session or installed browser path.

## Lane boundary

The IAM lane now owns the client, callback and binding code in
`services/control/internal/modules/sso`. Its typed provider allowlist is in
the shared configuration package. The connector schema and browser forms
currently support Gitea only. A complete implementation needs the SSO owner
and additive configuration, contract and UI changes. Adding an unreferenced
OIDC verifier to the identity module would leave the installed login path
unchanged. See [IAM lane boundaries](iam-questions.md).
