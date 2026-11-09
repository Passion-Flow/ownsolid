# Catalog OpenID Connect provider (IAM-106)

[OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html#CodeFlowAuth)
defines the authorization code flow and the ID token.
[OpenID Connect Discovery](https://openid.net/specs/openid-connect-discovery-1_0.html#ProviderConfig)
defines the metadata and key discovery contract.
[RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html) requires exact
redirect matching and recommends PKCE for confidential clients as well as
public clients. [RFC 7636](https://www.rfc-editor.org/rfc/rfc7636.html)
specifies the S256 challenge and verifier.

[Gitea](https://docs.gitea.com/development/oauth2-provider/) validates
requests against an application registration and gets user consent before
issuing the code. [Ory Hydra](https://github.com/ory/hydra) separates OAuth
protocol handling from its login and consent integration. An extra Hydra
service would require a second credential, storage and deployment boundary
for the first catalog path.

## First path

Use a confidential server-side catalog client registered by an authorized
operator in the tenant that owns the installed app. Use a configured
intranet HTTPS issuer with an exact redirect allowlist and a file-backed
asymmetric signing key. Use authorization code with S256 PKCE and one
explicit authorization for that client from a live Portal session. The
first scope is `openid` and the token conveys the minimum identity needed
by the installed client. Add profile claims only when the relying party
requires them. Discovery describes only the implemented grant, algorithms
and client authentication method.

Bind the request to the tenant, client and registration version, session
and user authorization epoch, redirect URI, nonce, PKCE challenge and
expiry. Client registration and current tenant come from server authority.
Persist only an opaque code digest. At exchange, lock and revalidate
authority, create the scoped token, consume the code and write the audit
event before releasing a token response. The issuer's keys are distinct
from entitlement and session secrets. App identity tokens convey no
Admin or Portal authority.

## Library and prototype

Prototype [ZITADEL OIDC v3.51.17](https://github.com/zitadel/oidc/releases/tag/v3.51.17)
first. It covers discovery, authorization, token and key endpoints in a Go
library and its [Apache-2.0 license](https://github.com/zitadel/oidc/blob/v3.51.17/LICENSE)
allows commercial offline use and redistribution with required notices.
Its [upstream advisory](https://github.com/zitadel/oidc/security/advisories/GHSA-j8gq-92xf-382c)
is a check on the chosen pin. Evaluate the exact build dependency graph,
licenses and advisories before adding it to the product. Use synthetic
accounts and test keys. No dataset, model or external service is needed.

The [token response implementation](https://github.com/zitadel/oidc/blob/v3.51.17/pkg/op/token.go)
creates an access token, signs an ID token and deletes the authorization
request in separate storage calls. Prove that a bounded request transaction
can cover these calls and commit before the response becomes visible.
[Fosite](https://github.com/ory/fosite) is another embeddable Apache-2.0
option with [authorization code replay handling](https://github.com/ory/fosite/blob/v0.49.0/handler/oauth2/flow_authorize_code_token.go).
Revisit it if the first adapter cannot meet the commit boundary.
A handwritten provider adds protocol validation and interoperability
burden beyond the tenant and catalog adapter.

The first scratch probe used ZITADEL's provider and relying-party packages
over TLS on Linux arm64 with PostgreSQL 18.6 and `--network none`.
It preauthorized a synthetic client and user, then used one database
transaction for the code row lock, access-token write, code consumption
and audit. It held the token response until commit. The relying party
resolved discovery and verified the returned JWT signature, issuer,
audience and nonce. Missing and wrong S256 proofs were refused.
Injected signing and audit failures returned no tokens and left the code
retryable. A retry raced through two TLS handlers with independent pools
and committed exactly once. The probe passed three consecutive runs.

The scope callback has to preserve the authorized subject when populating
the ID token. Leaving UserInfo empty for the `openid` scope erased the
subject and the relying party rejected the token. Keep errors at the
protocol boundary free of database details. Use a bounded response buffer
and transaction deadline in the product adapter.

The scratch build's license files permit commercial offline use and
redistribution. Preserve the applicable license and notices:

- Apache-2.0 covers ZITADEL OIDC, go-jose, logr, stdr and OpenTelemetry.
  The OpenTelemetry core, metric and trace distributions also carry
  BSD-3-Clause notices for Go-derived code.
- BSD-3-Clause covers Google UUID, Gorilla securecookie, httpforwarded,
  ZITADEL schema and the Go oauth2, sync and text modules.
- MIT covers doublestar, xxhash, chi, pgx, pgpassfile, pgservicefile,
  puddle and rs/cors.
- gu uses the Unlicense. Go uses BSD-3-Clause and PostgreSQL uses the
  PostgreSQL License.

The next prototype boundary is browser authorization with real Portal
authority and client registration. Verify state and exact redirects,
cross-tenant and cross-client requests, expiration, a revoked Portal
session and a disabled registration before adding the adapter to the
product. Product acceptance also needs the real catalog registration and
approval UI, file-key rotation and an installed catalog client completing
the entire flow.
