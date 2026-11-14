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

A candidate verifier is [coreos/go-oidc](https://github.com/coreos/go-oidc),
licensed [Apache-2.0](https://github.com/coreos/go-oidc/blob/v3.17.0/LICENSE),
which permits commercial use and offline redistribution with its notices. The
implementation must verify the selected release and transitive licenses
before adding it. This design adds no dependency, model or dataset.

Run a real private Keycloak realm on a disconnected network. The browser must
configure, link and sign in through it. Verify wrong issuer, audience,
signature, nonce, redirect, state and PKCE, key rotation, replay and connector
disable. Substitute a second tenant's connector, binding and flow and prove the
server rejects each without issuing a session or revealing the target.

## Lane boundary

The existing client, callback and binding code live in
`services/control/internal/modules/sso`. Its typed provider allowlist is in
the shared configuration package. The connector schema and browser forms
currently support Gitea only. A complete implementation needs the SSO owner
and additive configuration, contract and UI changes. Adding an unreferenced
OIDC verifier to the identity module would leave the installed login path
unchanged. See [IAM lane boundaries](iam-questions.md).
