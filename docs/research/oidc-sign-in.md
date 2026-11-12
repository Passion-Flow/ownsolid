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
requires Go 1.25. Select the patched dependency versions below explicitly; the
OIDC module's minimum requirements alone are not the admission policy. This
design adds no product dependency, model or dataset. Resolve and inspect the complete selected module
graph before implementation; do not infer runtime network behavior from its
license or from a successful signature check.

| Dependency | Selected version | License and offline distribution |
| --- | --- | --- |
| `github.com/coreos/go-oidc/v3` | v3.21.0 | [Apache-2.0](https://github.com/coreos/go-oidc/blob/v3.21.0/LICENSE); retain license, attribution and any applicable notices. |
| `github.com/go-jose/go-jose/v4` | v4.1.9, overriding the OIDC module's v4.1.4 minimum | [Apache-2.0](https://github.com/go-jose/go-jose/blob/v4.1.9/LICENSE); its selected module declares no further requirements. |
| `golang.org/x/oauth2` | v0.36.0 | [BSD-3-Clause](https://github.com/golang/oauth2/blob/v0.36.0/LICENSE); retain copyright and license. |
| `cloud.google.com/go/compute/metadata` | v0.3.0, required by the OAuth module | [Apache-2.0](https://github.com/googleapis/google-cloud-go/blob/compute/metadata/v0.3.0/LICENSE); module-graph dependency only, with no cloud or ambient credential composition in ownsolid. |

These licenses permit commercial use, redistribution and offline use with
their required notices. [go-jose v4.1.5](https://github.com/go-jose/go-jose/releases/tag/v4.1.5)
fixes parser-depth, numeric-date and malformed-key handling;
[v4.1.6](https://github.com/go-jose/go-jose/releases/tag/v4.1.6)
additionally fixes GHSA-mgcc-cww5-fp2q by limiting parsed RSA moduli to 4,096
bits. Do not admit the earlier v4.1.4 minimum or raise the new global modulus
limit. The later [v4.1.7 security release](https://github.com/go-jose/go-jose/releases/tag/v4.1.7)
also fixes RSA thumbprint validation, embedded-field JSON decoding, protected
header precedence, certificate RSA bounds and JSON JWS/JWE work limits.
[v4.1.9](https://github.com/go-jose/go-jose/releases/tag/v4.1.9) retains those
fixes and repairs opaque-signer and unsupported-key error compatibility.
Select v4.1.9 for admission and repeat the protocol qualification with that
exact graph. The earlier v4.1.6 run below establishes only its recorded input;
it is not the current dependency admission. Keep independent document,
key-count and algorithm bounds as well.
The private provider prototype uses
[Keycloak 26.8.2](https://github.com/keycloak/keycloak/releases/tag/26.8.2),
selected from its current official release rather than an unversioned image.
The Quay OCI index is
`sha256:c68e286cacd87b942ac1939a0a1481461109d6acd3a57dc496969a319a76bbf0`;
its Linux arm64 manifest is
`sha256:923a431c08971d49727a857e64870d8d63b5bf294a730e5dde729b5138c26c9e`.
Arm64 protocol evidence does not establish the Linux amd64 installed product.

Keycloak is [Apache-2.0](https://github.com/keycloak/keycloak/blob/26.8.2/LICENSE.txt).
Its complete versioned [third-party notice](https://github.com/keycloak/keycloak/releases/download/26.8.2/third-party-notice-keycloak.html)
enumerates every bundled application dependency and its license, including
0BSD, Apache-2.0, BSD-2-Clause, BSD-3-Clause, BouncyCastle, EPL-1.0, EPL-2.0,
GPL-2.0-only, GPL-2.0-with-classpath-exception, ISC, LGPL-2.1-or-later, MIT,
MIT-0 and UPL-1.0. This is not an Apache-only distribution. The notice identifies
MySQL Connector/J 9.7.0 and Nashorn 15.4 under GPL-2.0-only, the classfile
backport under the classpath exception, MariaDB Java Client 3.5.10 under LGPL
and the H2/Jakarta components under EPL. These licenses allow commercial and
offline use; redistribution can require corresponding source, relinking rights
and preserved license/notice material. Keep the unmodified provider solely as
a private test fixture. Do not package it with ownsolid or copy its code under
the assumption that the top-level license covers every component. Any future
provider distribution also requires the selected image's OS and JDK notices.
The prototype adds no product dependency, model or dataset.

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

## Private prototype admission

Run the fixed provider image on a network-none disposable fixture, without
publishing ports or creating a second installation. Use its documented
[realm import](https://www.keycloak.org/server/importExport), explicit
[hostname](https://www.keycloak.org/server/hostname) and
[PEM TLS configuration](https://www.keycloak.org/server/enabletls). Generate
the private CA, test user password and confidential-client secret in protected
files outside the repository. No credential belongs in an argument, source,
test result or committed fixture. The probe runs in the provider's network
namespace and dials only verified loopback TLS with the configured DNS/SNI.
It must exercise the actual password form and authorization code exchange;
the password grant does not stand in for that flow.

The selected verifier's [remote key implementation](https://github.com/coreos/go-oidc/blob/v3.21.0/oidc/jwks.go)
reads the response without a byte limit and refreshes on an unknown key. Its
single-flight coordination does not cap sequential unknown-key requests, and
the refresh uses the key set's retained context. Qualify a bounded owned key
set or transport with explicit refresh admission before allowing it into the
serving path. A request timeout alone is not a byte, key-count or refresh-rate
bound. The [claim verifier](https://github.com/coreos/go-oidc/blob/v3.21.0/oidc/verify.go)
also unmarshals JSON using last-value behavior, does not require the flow nonce
or authorized party, and allows a five-minute `nbf` skew. Test those boundaries
explicitly with independently signed fixture claims. Reject duplicate claim
and JOSE header names and differently cased aliases of standard fields. Go's
JSON struct decoding can otherwise treat an uppercase unknown claim as a
lowercase standard claim. Keep the existing SSO case-alias guard when composing
the new adapter. Reject noncanonical compact-token characters, missing/empty
subjects, future `iat` and disallowed
algorithm choices before issuing any local authority. Keep that policy in the
owned adapter; do not change library skip-check switches to accept a fixture.

Qualify both a new `kid` and replacement key material under the same `kid`.
A failed signature with a cached key may trigger one refresh only when the
shared refresh budget admits it; it must not make every invalid token a new
network request. Preserve valid cached-key verification during a failed
refresh, bound cache age, and stop recognizing a removed key after accepting
the replacement set. A throttled refresh needs a new explicit sign-in attempt,
not an automatic second authorization-code exchange.

Record real-provider results separately from hostile signed-token and transport
fixtures. An unsuccessful case retains its original log and is not a completed
prototype. Only after both classes pass should connector storage and the real
SSO callback consume the adapter.

## Storage and real-flow admission

The current typed provider inventory already has separate `origin` and `issuer`
fields. It permits one inventory entry per origin; the serving owner still
constructs only a Gitea client. A generic provider selector must preserve that
legacy interpretation and represent distinct approved issuers that share one
network origin. An issuer path is an identity, not an endpoint path prefix.
Discovery strips a terminal slash only when locating its metadata document;
the configured issuer and ID token comparison retain the original exact value.
Root and trailing-slash issuers and sibling endpoint paths need positive tests.
All endpoint origins still require explicit approved transport policy. The
first private-provider path uses one origin, canonical endpoints without
queries, and RS256; additional transport and algorithm profiles need explicit
admission, not automatic fallback.

The current PostgreSQL connector permits only Gitea and stores a UUID client
ID. Its binding subject is a canonical decimal number of at most 19 characters.
Preserve those existing identities and immutable intent fingerprints. The new
profile needs an immutable exact issuer, a bounded opaque client ID and a
case-sensitive subject of at most 255 ASCII characters. Do not cast an OIDC
subject into the old numeric column. Expanding the shared column without
retaining provider-specific validation would also weaken Gitea's contract.
Include upgrade/restore with active Gitea bindings and pending flows, not only
a fresh schema. ARC owns formal migration admission and engine-specific catalogs.

Use a fresh random OIDC nonce per flow. Store its one-way digest with that
flow's immutable request scope and compare the signed returned nonce against
it in constant time. The nonce does not require a second credential key or
plaintext token store. The existing encrypted PKCE verifier stays in its
existing purpose and representation, and the exclusive claim still erases it
before provider I/O. A new OIDC flow must have its nonce digest; a legacy Gitea
flow must retain its existing semantics. Prototype and test that storage/claim
boundary against real PostgreSQL before composing the callback.

The existing start transaction constructs a Gitea transport without contacting
the provider. Do not turn that constructor into a discovery request while
holding user, source, session or connector locks. Resolve the approved connector
and obtain bounded metadata outside the authority transaction, then recheck its
exact current revision and actor before persisting a start. Bind the selected
profile/issuer to that intent. Claim once in the database, exchange/verify
outside the transaction, then recheck current source and connector authority
in completion before binding or issuing a session. Metadata/JWKS failure cannot
prevent local-password startup or authorize an unapproved destination.

The published connector and configuration contracts need the provider kind,
issuer and opaque client-ID distinction; Web needs the actual provider fields
and returned identity/result behavior. Use one explicit confidential-client
authentication style, including the required form encoding before HTTP Basic
encoding for generic client IDs and secrets. A colon or other allowed character
must not change which credential was sent. Honor the configured exact callback
and validate the authorization-response issuer when supported, as well as
state, browser proof and nonce; none can be replaced by an email match.
Shared owners admit those contracts before any route presents generic support.

The actual route tests must substitute foreign tenant connectors, bindings and
flows and independently observe that no provider exchange or session issuance
occurred. Preserve the existing source-session, connector-disable, keyring-loss,
audit-failure and real COMMIT-uncertainty cases. A protocol prototype does not
replace those database and browser acceptance paths.

### PostgreSQL storage prototype boundary

Prototype the upgrade separately before admitting product DDL. Retain the
three existing connector/binding/flow tables and their actor, audit, source
session and immutable-intent relationships. Add the connector's immutable
issuer, widen its client ID and the case-sensitive binding subject, and give
bindings/flows a provider discriminator whose legacy default is Gitea.
A composite foreign key containing tenant, connector and provider prevents an
OIDC record from borrowing a Gitea profile or another tenant's connector.
Keep Gitea's canonical UUID client and decimal subject checks in its branch;
only the OIDC branch accepts bounded opaque client IDs and ASCII subjects.

An OIDC flow has a 32-byte nonce digest, unique within its connector scope;
a Gitea flow has none. Bind that discriminator and digest to the existing
immutable flow fields through claim, completion and session issuance. Preserve
the existing pending Gitea verifier ciphertext byte for byte and its one-use
clear-on-claim transition. There is no new token or credential ledger and no
change to the number of product tables.

Verify a real PostgreSQL upgrade with existing connectors, numeric bindings,
pending flows and retained intents; exact opaque/case-sensitive values after
upgrade; invalid-length/non-ASCII/profile/tenant substitutions; nonce reuse and
mutation refusal; and preserved claim/single-issuance constraints. Independently
read committed results. A downgrade with any retained OIDC connector must fail
clearly and leave data untouched, requiring a consistent pre-upgrade restore;
it cannot delete or reinterpret OIDC identities as Gitea. A legacy-only
upgrade/downgrade must preserve its original data and constraints.

ARC owns the formal migration number, catalogs, engine admission and startup.
The first storage prototype is PostgreSQL, not an assertion that MySQL or
Dameng already support this profile. The shared Go module must also admit the
qualified OIDC/JOSE dependency versions before a product adapter imports them.
No default serving constructor or public connector selection changes as part
of the isolated storage proof.

The isolated storage prototype passed 54 real PostgreSQL checks against the
exact migration-17 SSO definitions and minimal prerequisite tables. It kept
the seven fixture tables unchanged in count, preserved legacy row snapshots
through upgrade/downgrade, admitted distinct opaque subjects and enforced
profile/tenant foreign keys, bounded fields, nonce uniqueness/immutability,
claim and single-session issuance, and data-preserving downgrade refusal.
The first missing prerequisite `sessions.revoked_at` was a fixture setup error;
its original failure was retained before the corrected run. Evidence is
`iam102-20261113.1jjfl_s4/storage-prototype/run-id2d6xyc`.

That seven-table structural fixture is not the installed schema. The next
admission test must apply identical candidate DDL to the current complete
repository fixture and exercise real Gitea secret verification, retained
idempotency replay, catalog/binding reads, pending starts and sourced-session
revocation, with independent committed snapshots. Test the OIDC constraints
and a real nonce-uniqueness race there as well. Candidate DDL under SSO testdata
and opt-in repository tests remain qualification inputs, not serving schema
or generic provider support.

## Lane boundary

The IAM lane now owns the client, callback and binding code in
`services/control/internal/modules/sso`. Its typed provider allowlist is in
the shared configuration package. The connector schema and browser forms
currently support Gitea only. A complete implementation needs the SSO owner
and additive configuration, contract and UI changes. Adding an unreferenced
OIDC verifier to the identity module would leave the installed login path
unchanged. See [IAM lane boundaries](iam-questions.md).
## Protocol prototype qualification

The standalone private-provider prototype passed eight native Go test roots
with race detection, including signed nonce/authorized-party/issuer/audience
and time refusals, duplicate/case-alias fields, exact opaque subjects,
canonical discovery, TLS trust refusals, bounded JWK structure and parsing,
new-key and same-key-ID replacement, unknown/known-key floods, concurrency and
actual request cancellation. Three live-only roots were skipped by that native
unit invocation and exercised separately where applicable below.

Two actual Keycloak 26.8.2 protocol roots then passed in a disposable Linux
arm64 container with `--network none` and no published host ports. The probe
performed the real password form and one S256 authorization-code exchange,
verified the signed realm issuer/nonce/audience and exact nonnumeric subject,
and matched UserInfo to that subject. The client ID and secret contained
characters requiring form encoding before HTTP Basic encoding. Wrong PKCE,
redirect and secret, token issuer/audience/nonce/signature substitution and
consumed-code replay were refused, with no automatic second token exchange.
The separate startup probe also passed.

The fixed image manifest above ran Red Hat OpenJDK 21.0.13. Its complete
application third-party notice, 35 image-package license files and 38 JDK legal
files were retained and read, including the GPL, classpath/assembly and GCC
runtime exceptions and component notices. These materials are for the private
unmodified fixture; they do not admit a provider image for product distribution.
The first LibreSSL implicit-serial pathname failure is retained separately;
explicit serial generation and certificate-chain verification corrected that
fixture setup before the successful provider run.

The durable local evidence set is `iam102-20261113.1jjfl_s4`, successful run
`qualification-j4mivx8l`. It retains exact prototype source hashes, selected module
sums, binary and image identities, native and real-provider logs, independent
request counts, component licenses and cleanup results. The provider and its
owned anonymous storage were removed; the pinned image remains a dependency
cache. No test server remains running.

This establishes the external protocol approach. It adds no product dependency,
connector type, binding, local session, route, database schema or browser UI.
It is neither a rendered-browser nor Linux amd64 installation acceptance.
IAM-102 stays open for the real-flow/storage handoff above and shared owners'
contract, configuration and UI admission. The publication changes research
only; its layout, roadmap and whitespace checks do not replace that future
feature's full repository and installed-flow gates.
