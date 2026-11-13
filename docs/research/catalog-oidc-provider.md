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

### Authorization-code crypto composition

Inspecting the actual [authorization-code builder](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/auth_request.go)
adds an important constraint before serving composition: `BuildAuthRequestCode`
encrypts the authorization request ID through the same `Crypto` interface used
for access bearers. The qualified first adapter deliberately accepts only the
token-ID/subject bearer shape. It cannot be wired unchanged as the complete
provider's crypto; the earlier constructor test qualifies its explicit override,
not authorization-code generation.

Prototype a purpose-selecting composition with separate, disjoint code and
bearer GCM key rings. Server-created request IDs use an `ar_` prefix and 32
random bytes in canonical base64url. The code encrypter accepts only that
shape; the bearer encrypter retains its two opaque identifiers. Reject shared
key IDs or key material across the rings. Incoming `Crypto.Decrypt` admits
only the bearer ring; code redemption locates the complete ciphertext's digest
in the owned database, rather than decrypting a client-supplied request ID.
Neither purpose admits CFB or broad arbitrary-string encryption. This uses the
same SDK GCM implementation and unchanged licensed module graph.

The next controls must call the actual SDK code builder, prove generated codes
and access bearers use their selected rings, and refuse code-as-UserInfo,
bearer-as-code-shape, wrong-purpose/unknown/retired keys and shared material.
Native shape/key separation is not one-use code redemption: the real PostgreSQL
owner still must bind code expiry, client, redirect, PKCE and current authority
and atomically consume the digest with token creation/signing/audit. Do not
weaken the first bearer's shape validation merely to make SDK callbacks pass.

The purpose composition subsequently passed eleven native race roots and the
same eleven roots in a disconnected Linux arm64 container: 72 named tests or
68 independent leaves in each. The actual SDK `BuildAuthRequestCode` encrypted
the server request ID using the code ring, while `CreateBearerToken` used the
separate bearer ring. Code ciphertext was refused by incoming provider
decryption and the UserInfo boundary. Invalid request-ID shapes, shared key
IDs/material, caller mutation of supplied key arrays and private-ring rotation/
retirement controls passed without relaxing the original bearer tests.

This evidence is `iam106-purpose-20261113.99n43dmw`, run
`qualification-xk8yyrkh`, source-hash-manifest SHA-256
`936ee2aeafbaf3f31e2b9715a5ddcb715db5e5015f92a19c68a6ef253049727c`.
Source/module hashes match both runs and the owned container was removed.
The first run, `qualification-n6p7s0th`, retained a failed assertion whose
constant-time comparison result was interpreted backwards in the caller-key
copy test; correcting that test did not change key ownership or encryption.
No serving authorization request, client, code row or token was created.

Code ciphertext is matched by digest during future redemption, so removing a
code-encryption key alone does not revoke an already stored code. Its database
expiry, consumed state and current client/installation authority must do that.
The native private-ring retirement control is not proof of code revocation.
The complete PostgreSQL and RP/OP HTTPS prototype remains open.

### Durable protocol prototype plan and additional licenses

The complete standalone experiment will use two verified private-CA HTTPS
servers and a disposable PostgreSQL database. Its synthetic account/password
form tests protocol composition only; it is not a second ownsolid login owner
or evidence of Portal/MFA consent integration. Client, installation, principal,
source session and membership rows have explicit scope/revisions and stable
opaque subjects. All credentials, signing/GCM keys and the database connection
enter through private fixture files. No examples' plaintext passwords,
insecure cookies, permissive callback handling or token logging are copied.

Persist the browser-bound authorization request before login. Require its exact
browser cookie and CSRF/Origin proof to approve it; require only the admitted
`openid` code/S256 profile before the SDK. The SDK accepts several built-in
scopes independently of `Client.IsScopeAllowed`, so that getter alone is not
the registered-claim boundary. Save the generated code's digest once with a
bounded expiry. Bind its subject, source-session epoch, current membership
lifetime, exact client/installation revisions, redirect and challenge.

At token exchange, the strict parser and current confidential-client check run
before code-row locks. An owned request transaction then carries the resolved
client through all SDK storage calls. Use a nonlocking scoped lookup only to
resolve the complete authority lock set, acquire current client/installation,
principal/session/membership fences and the exact code row, then recheck all
bindings and expiry. Create the hashed opaque token ID, sign the ID token,
consume the code and append the minimized event in that transaction.

Buffer the SDK response under a configured byte limit. Release a successful
token response only after the database confirms COMMIT. SDK errors, signing
failure, cancellation and oversized output roll back the transaction; actual
wire COMMIT loss releases a fixed error and no token, with no automatic resend.
Store methods map failures to fixed protocol errors before SDK serialization
and logging. UserInfo rechecks the token's bound subject/client and current
installation, source session, membership lifetime and account epoch; the
ciphertext's subject never supplies tenant scope. Uninstall, disable, rotation
and removal must fence existing tokens as well as unconsumed codes.

The RP will use the same pinned SDK's actual RP discovery, signature/token-hash
and nonce verifier with a bounded approved-destination HTTPS client. A complete
flow must survive OP restart with retained client, issuer, subject and keys.
Tests must prove wrong/foreign client, secret, redirect, PKCE and browser refusal;
duplicate redemption; signing failure and retry; revocation/removal propagation;
wrong CA/name, response/request budgets and actual blocked-I/O cancellation.
Signing rotation retains old public keys through issued-token expiry. Disabled
grants and SDK routes remain explicitly closed rather than receiving success
implementations. Native wire-loss tests reuse the repository's actual
PostgreSQL forwarding helper outside the product, not simulated SQL responses.

The SDK's [discovery builder](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/discovery.go)
also advertises implicit response/grant types and endpoint locations that its
router always registers. The prototype must project that generated metadata
to the actually admitted code profile and expose only its selected routes.
Close implicit, refresh, device, token exchange, request objects, dynamic
registration, introspection and end-session paths for this first experiment;
do not advertise those closed paths as implemented. This limits prototype
qualification, not the roadmap capability's eventual scope.

The legacy SDK's [authorization error handler](https://github.com/zitadel/oidc/blob/v3.52.0/pkg/op/error.go)
logs the protocol state; `WithLogger` no longer supplies a provider-local
logger. Avoid global logger changes or relying on SDK error redirects for the
authorization/login callback. Compose the SDK's request parser/client validator,
storage owner, code-response builder and redirect encoder directly at those
two routes, returning fixed local errors before any sensitive state is logged.
Keep the SDK token, UserInfo, revocation and key handlers behind their strict
boundaries and fixed storage errors. Capture and inspect protocol logs in the
prototype; do not copy upstream sample token-printing callbacks.

Loading `pkg/op`, `pkg/client/rp` and pgxpool selected 45 modules and 57 legal
files in `iam106-provider-20261113.q_ov5ppw`. The preceding 40 exact versions
and notices are unchanged; the five additions are below. Module downloads were
checksum-verified and each added primary LICENSE matched the downloaded source
bytes. This is metadata/license preparation only, not a build or protocol run.

| Additional module | Version | Primary license |
| --- | --- | --- |
| `github.com/jackc/pgx/v5` | v5.11.0 | [MIT](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE) |
| `github.com/jackc/pgpassfile` | v1.0.0 | [MIT](https://github.com/jackc/pgpassfile/blob/v1.0.0/LICENSE) |
| `github.com/jackc/pgservicefile` | v0.0.0-20240606120523-5a60cdf6a761 | [MIT](https://github.com/jackc/pgservicefile/blob/5a60cdf6a761/LICENSE) |
| `github.com/jackc/puddle/v2` | v2.2.2 | [MIT](https://github.com/jackc/puddle/blob/v2.2.2/LICENSE) |
| `github.com/stretchr/objx` | v0.1.0 | [MIT](https://github.com/stretchr/objx/blob/v0.1.0/LICENSE) |

All five permit commercial/offline use and redistribution with preserved
copyright/license text. The objx row is a graph/test dependency, not an enabled
service. The test database/image and native build-tool notices retain the
already inspected pinned PostgreSQL/pgvector and Go build-base obligations.
This experiment adds no product dependency, model or dataset. Product admission
must still resolve its complete graph and compose actual ownsolid authorities.

### First private RP/OP flow qualification

The standalone prototype passed fourteen non-worker roots and 82 independent
leaves in each of two topologies: native Go with race detection against an
owned loopback PostgreSQL, then a Linux arm64 binary, its child OP processes
and PostgreSQL in one `--network none` container with no published ports.
Both passed 88 named tests including parent groups. The worker-only root
skips in the parent suite and runs explicitly in real child processes.
The native Mac client is not egress-isolated; only the second topology is
disconnected. The database uses the prototype's nine synthetic authority,
request, token and event tables, not the product's Identity or audit owners.

An actual RP HTTPS server and a separate OP HTTPS process used verified
private-CA certificates and approved literal dial addresses. The RP used the
pinned SDK's discovery, code exchange, signed ID-token/nonce/token-hash
verification and UserInfo client. The synthetic password/CSRF form bound the
request to its browser and source-session cookies. The first sign-in returned
the registered opaque subject. Replaying its consumed code returned no token.
The OP process then exited cleanly and a new PID started on the inherited
owned listener with retained database/key files. The first access token still
passed UserInfo and a fresh sign-in retained the same subject while issuing
different code/token credentials. This is an actual process restart, not an
in-memory provider reconstruction.

Wrong secret, foreign-tenant client, another local client, redirect, verifier,
tenant query and duplicate encoded code fields were refused without token
creation or code consumption. Explicit corrected exchanges succeeded. Client
disable/reenable and secret rotation, installation disable/reenable, account
epoch changes, session revocation and membership delete/recreate fenced both
existing UserInfo and pending codes. Repair did not revive that old authority;
a fresh verified flow succeeded. The discovery response omitted closed routes.

Evidence is `iam106-provider-20261113.q_ov5ppw`, first-flow run
`qualification-fxtc85y1`, qualified input `ebeca4b66e22`. Its source-hash
manifest SHA-256 is
`2275d25830cad4d63b896cafe755a72476ec06720e51e450ba17f0c1991a1cb2`.
Exact prototype source/module files, race/disconnected output, image/network
inspection and container/storage/bridge cleanup remain outside Git. Child
logs were still temporary in this first run; retained child-log inspection,
signing/audit failure, cancellation, actual wire COMMIT uncertainty, concurrent
redemption, key rotation and the remaining failure matrix are not qualified
by this run. The earlier compile-only `rp` package/helper naming conflict was
corrected with a package alias before this qualification.

No product source, dependency, key, formal schema, API or UI changed. Synthetic
authority rows and a fixture password form do not implement actual ownsolid
Portal consent, catalog registration, scoped grants or revocation composition.
IAM-106 remains open and its complete external qualification continues.

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
