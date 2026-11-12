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
is admitted or added to the product by this note. The complete candidate graph
and license inspection below precede any prototype build; security, import and
runtime-network qualification remain required. No model or dataset is involved.

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

The legacy [code exchange](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/token_code.go)
loads the authorization request before authenticating the confidential client.
Qualify a bounded outer client-authentication gate before code-row locks, then
carry that server-resolved client and transaction through the storage calls.
Its [form parser](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/token_request.go)
uses `Request.Form`, which merges URL and body inputs. Enforce one registered
authentication method, bounded body-only fields, duplicate refusal and no query
credentials before the SDK. Test header/body conflicts and Basic form encoding;
client method metadata alone is not evidence that those boundaries are enforced.

The legacy [UserInfo handler](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/userinfo.go)
can read a bearer from form/query data and can fall back from encrypted tokens
to JWT verification. The initial opaque-token profile must explicitly close
those alternate paths and accept the Authorization header only. Generate a
random token ID, hash that identifier in the store and compare its bound subject,
client and current installation authority after decryption; the payload's subject
does not supply tenant scope. Its colon-separated bearer payload also means
the selected opaque subject encoding must contain no colon. Project storage
failures to fixed protocol errors before the handler's JSON serialization.
These are source-derived prototype requirements, not a qualified OP adapter.

[Ory Fosite](https://github.com/ory/fosite) is an alternative OAuth/OIDC SDK
with owned storage interfaces. Compare its code-consumption and transaction
composition against the same real PostgreSQL tests if the candidate cannot
preserve atomic ownsolid authority. Do not choose a handwritten OAuth server:
JWT signing alone omits redirect, grant, request, consent and replay rules.
Running another complete identity server as a mandatory catalog dependency
would introduce another user/session authority and deployment lifecycle; the
first path should instead integrate a protocol library with ownsolid's owners.
The candidate still needs a complete external protocol prototype before product
implementation.

### Complete source-inspection graph

Resolving the actual `pkg/op` import closure outside the product selected the
following 40 modules, including graph-only/test dependencies. All 52 license,
notice and patent files were retained with module sums and SHA-256 hashes in
`iam106-20261113.f1ldyrpx`; every primary license link matches the inspected
module bytes. The inventory stage loaded only source/import metadata with Go
1.27.3; compilation belongs to the later strict-boundary experiment below.
This is the isolated candidate graph, not
the product's final minimal-version selection, which must be checked again
before admission.

Go's lazy module loading matters: the initial root-only metadata list contained
27 modules and omitted 13 selected after loading the real OP imports. Retain
both inputs and use the import-closed inventory below before a prototype build.
Listing the SDK's module file alone is not a complete dependency inventory.

| Module | Version | License material |
| --- | --- | --- |
| `cloud.google.com/go/compute/metadata` | v0.3.0 | [Apache-2.0](https://github.com/googleapis/google-cloud-go/blob/compute/metadata/v0.3.0/LICENSE) |
| `github.com/bmatcuk/doublestar/v4` | v4.10.4 | [MIT](https://github.com/bmatcuk/doublestar/blob/v4.10.4/LICENSE) |
| `github.com/cespare/xxhash/v2` | v2.3.0 | [MIT](https://github.com/cespare/xxhash/blob/v2.3.0/LICENSE.txt) |
| `github.com/davecgh/go-spew` | v1.1.1 | [ISC](https://github.com/davecgh/go-spew/blob/v1.1.1/LICENSE) |
| `github.com/go-chi/chi/v5` | v5.5.0 | [MIT](https://github.com/go-chi/chi/blob/v5.5.0/LICENSE) |
| `github.com/go-jose/go-jose/v4` | v4.1.9 | [Apache-2.0, BSD-3-Clause](https://github.com/go-jose/go-jose/blob/v4.1.9/LICENSE) |
| `github.com/go-logr/logr` | v1.4.4 | [Apache-2.0](https://github.com/go-logr/logr/blob/v1.4.4/LICENSE) |
| `github.com/go-logr/stdr` | v1.2.2 | [Apache-2.0](https://github.com/go-logr/stdr/blob/v1.2.2/LICENSE) |
| `github.com/golang/mock` | v1.6.0 | [Apache-2.0](https://github.com/golang/mock/blob/v1.6.0/LICENSE) |
| `github.com/google/go-cmp` | v0.7.0 | [BSD-3-Clause](https://github.com/google/go-cmp/blob/v0.7.0/LICENSE) |
| `github.com/google/go-github/v31` | v31.0.0 | [BSD-3-Clause](https://github.com/google/go-github/blob/v31.0.0/LICENSE) |
| `github.com/google/go-querystring` | v1.2.0 | [BSD-3-Clause](https://github.com/google/go-querystring/blob/v1.2.0/LICENSE) |
| `github.com/google/gofuzz` | v1.2.0 | [Apache-2.0](https://github.com/google/gofuzz/blob/v1.2.0/LICENSE) |
| `github.com/google/uuid` | v1.6.0 | [BSD-3-Clause](https://github.com/google/uuid/blob/v1.6.0/LICENSE) |
| `github.com/gorilla/securecookie` | v1.1.2 | [BSD-3-Clause](https://github.com/gorilla/securecookie/blob/v1.1.2/LICENSE) |
| `github.com/jeremija/gosubmit` | v0.2.8 | [MIT](https://github.com/jeremija/gosubmit/blob/v0.2.8/LICENSE) |
| `github.com/kr/pretty` | v0.3.1 | [MIT](https://github.com/kr/pretty/blob/v0.3.1/License) |
| `github.com/kr/text` | v0.1.0 | [MIT](https://github.com/kr/text/blob/v0.1.0/License) |
| `github.com/muhlemmer/gu` | v0.3.1 | [Unlicense](https://github.com/muhlemmer/gu/blob/v0.3.1/LICENSE) |
| `github.com/muhlemmer/httpforwarded` | v0.1.0 | [BSD-3-Clause](https://github.com/muhlemmer/httpforwarded/blob/v0.1.0/LICENSE) |
| `github.com/pmezard/go-difflib` | v1.0.0 | [BSD-3-Clause](https://github.com/pmezard/go-difflib/blob/v1.0.0/LICENSE) |
| `github.com/rogpeppe/go-internal` | v1.14.1 | [BSD-3-Clause](https://github.com/rogpeppe/go-internal/blob/v1.14.1/LICENSE) |
| `github.com/rs/cors` | v1.11.1 | [MIT](https://github.com/rs/cors/blob/v1.11.1/LICENSE) |
| `github.com/stretchr/testify` | v1.12.1 | [MIT, BSD-3-Clause, ISC](https://github.com/stretchr/testify/blob/v1.12.1/LICENSE) |
| `github.com/zitadel/oidc/v3` | v3.52.0 | [Apache-2.0](https://github.com/zitadel/oidc/blob/v3.52.0/LICENSE) |
| `github.com/zitadel/schema` | v1.3.2 | [BSD-3-Clause](https://github.com/zitadel/schema/blob/v1.3.2/LICENSE) |
| `go.opentelemetry.io/auto/sdk` | v1.2.1 | [Apache-2.0](https://github.com/open-telemetry/opentelemetry-go-instrumentation/blob/sdk/v1.2.1/LICENSE) |
| `go.opentelemetry.io/otel` | v1.46.0 | [Apache-2.0, BSD-3-Clause](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/LICENSE) |
| `go.opentelemetry.io/otel/metric` | v1.46.0 | [Apache-2.0, BSD-3-Clause](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/LICENSE) |
| `go.opentelemetry.io/otel/trace` | v1.46.0 | [Apache-2.0, BSD-3-Clause](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/LICENSE) |
| `go.yaml.in/yaml/v3` | v3.0.5 | [Apache-2.0, MIT](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE) |
| `golang.org/x/crypto` | v0.53.0 | [BSD-3-Clause](https://github.com/golang/crypto/blob/v0.53.0/LICENSE) |
| `golang.org/x/mod` | v0.41.0 | [BSD-3-Clause](https://github.com/golang/mod/blob/v0.41.0/LICENSE) |
| `golang.org/x/net` | v0.55.0 | [BSD-3-Clause](https://github.com/golang/net/blob/v0.55.0/LICENSE) |
| `golang.org/x/oauth2` | v0.38.0 | [BSD-3-Clause](https://github.com/golang/oauth2/blob/v0.38.0/LICENSE) |
| `golang.org/x/sync` | v0.24.0 | [BSD-3-Clause](https://github.com/golang/sync/blob/v0.24.0/LICENSE) |
| `golang.org/x/text` | v0.43.0 | [BSD-3-Clause](https://github.com/golang/text/blob/v0.43.0/LICENSE) |
| `golang.org/x/tools` | v0.51.0 | [BSD-3-Clause](https://github.com/golang/tools/blob/v0.51.0/LICENSE) |
| `gopkg.in/check.v1` | v1.0.0-20201130134442-10cb98267c6c | [BSD-2-Clause](https://github.com/go-check/check/blob/10cb98267c6c/LICENSE) |
| `gopkg.in/yaml.v3` | v3.0.1 | [Apache-2.0, MIT](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE) |

All listed licenses permit commercial and disconnected use and redistribution
subject to their required notices and terms. Preserve the Unlicense text as
well as the MIT, BSD-2-Clause, BSD-3-Clause, ISC and Apache material. The JOSE JSON fork also carries
BSD-3-Clause. Testify includes BSD-3-Clause `difflib` and ISC `spew` material;
the three OpenTelemetry API modules include BSD-3-Clause code in their Apache
license files. YAML has per-file MIT and Apache coverage, not a blanket choice
of either license. Retain the SDK/YAML notices and the Go subrepositories'
patent grants. Graph-only GitHub clients, test generators and telemetry APIs
do not authorize outbound services or runtime exporters. Their actual import
and network behavior still need the prototype's disconnected qualification.

## Failure coverage and concrete completion

### First strict transport and crypto slice

The pinned SDK's [provider constructor](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/op.go)
creates AES-GCM encryption with a legacy AES-CFB decryption fallback. Its
[crypto implementation](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/crypto.go)
explicitly documents that CFB ciphertext is unauthenticated. Ownsolid has no
legacy OP ciphertext to migrate. Supply `WithCrypto` with an owned, bounded,
compact-JWE-only adapter around the SDK's AES-256-GCM implementation; never
admit that compatibility fallback. Pin `A256GCMKW`/`A256GCM`, exact protected
key IDs and the active/retiring key set. Refuse duplicate/unknown protected
headers, noncanonical encodings, compression/critical/remote-key options,
oversized input and invalid token-ID/subject shape before decryption. Keys
come from the eventual private key-file owner; they are unrelated to licenses.

For the confidential-client code profile, implement an outer request parser
before starting storage transactions. Require a bounded POST form with exactly
the admitted code-flow fields and one Basic Authorization header; refuse all
query inputs, duplicate/unknown/body credential fields and mixed authentication.
[RFC 6749 section 2.3.1](https://www.rfc-editor.org/rfc/rfc6749.html#section-2.3.1)
requires form encoding the client ID and password before constructing Basic
credentials; prove that special characters reach the SDK identically. Enforce
the [RFC 7636 verifier syntax](https://www.rfc-editor.org/rfc/rfc7636.html#section-4.1)
and S256 profile, then authenticate the exact registered client before any
authorization-code row lookup. UserInfo accepts only a bounded Authorization
bearer and the strict local JWE profile, so the SDK's query/form/JWT fallback
cannot select another verification path.

Native tests must include positive SDK interoperability controls as well as
legacy CFB/default-fallback refusal, key rotation/retirement, protected-header
tampering, malformed/oversized compact input and concurrency. Request tests
cover malformed percent/form/Basic encodings, encoded duplicate names, unknown
grants, body/header conflicts, verifier bounds and early authentication refusal.
No native parser/crypto test qualifies PostgreSQL consumption, Portal consent
or actual RP/OP HTTPS behavior. Those remain the subsequent complete prototype.
Reuse the exact 40-module/52-notice graph above, with no new model, dataset,
dependency or product import. Reject SDK-default compatibility merely for
convenience, handwritten encryption and admitting unsupported protocol paths
through an otherwise permissive SDK router.

### Strict-boundary qualification

The external adapter passed nine native test roots with race detection and
the same nine roots in a Linux arm64 container with `--network none` and no
host ports. Both passed 70 named tests, including parent groups, or 66
independent leaves. This uses the unchanged inspected module files and actual
pinned SDK constructor, crypto and token-request parser functions. It does not
serve an OP endpoint or supply a serving storage implementation.

Positive controls verified that the adapter's GCM output decrypts with the SDK
and the SDK's GCM output decrypts with the adapter. A default SDK provider
accepted legacy CFB, including altered ciphertext without an authentication
failure. The strict adapter and explicit `WithCrypto` constructor refused both.
Retiring GCM keys remained readable until removed; unknown/retired key IDs,
altered compact segments, duplicate/escaped-duplicate protected headers,
algorithm changes, compression, critical/remote-key options, padded/full-JSON
input, invalid plaintext shape and configured limits returned no plaintext.
Thirty-two concurrent encrypt/decrypt operations passed under the race detector.
Static issuer construction ignored hostile Host/Forwarded values.

The strict token parser and real SDK parser agreed on form-encoded Basic
credentials containing spaces, colon, plus, slash and percent characters.
Malformed/duplicate/query/body credentials, unknown fields, duplicate encoded
form names, unsupported grants, invalid verifier/callback syntax and bounded
body failures returned no parsed credentials. The staged owner callback was
not reached before successful client authentication; invalid or canceled
requests did not even invoke that authentication callback. UserInfo accepted
only a header bearer with the strict compact profile and refused query, form,
JWT-shaped, legacy, duplicate and missing bearer variants.

Evidence is `iam106-boundary-20261113.sotincc4`, run
`qualification-wb_q0iv_`, source-hash-manifest SHA-256
`5d5934271b217a633dc14b3ea715d17776fac6b3c22eefc8460f16963e551012`.
Exact source/module files, inherited legal hashes, native/disconnected logs,
container inspection and cleanup were retained outside Git. The owned container
was removed. Product source, dependencies, keys, schema, routes and UI did not
change; publication runs layout/roadmap/whitespace checks only.

This qualifies local parser/crypto composition, not registered-client database
authentication, S256 code redemption, consent, authorization, signing, token
revocation, transaction response release, actual RP/OP HTTPS or installed
catalog integration. HTTP read deadlines and socket cancellation still need
real-server qualification. IAM-106 remains open, and its next experiment must
qualify the complete durable code/JWT/UserInfo path described below.

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
