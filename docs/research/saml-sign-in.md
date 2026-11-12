# SAML browser sign-in (IAM-104)

## Primary sources and selected path

The [OASIS browser SSO profile](https://docs.oasis-open.org/security/saml/v2.0/saml-profiles-2.0-os.pdf)
and its [errata](https://docs.oasis-open.org/security/saml/v2.0/sstc-saml-errata-2.0-cd-04.html)
require a valid bearer assertion, exact recipient and request correlation,
time checks and verification of the signatures relied upon. Use that profile
with an SP-initiated AuthnRequest and an HTTP-POST response. Keycloak's
[SAML administration](https://www.keycloak.org/docs/latest/server_admin/index.html#_saml)
provides a private provider reference, including client URLs, signatures and
key configuration. Real IdP password/consent and signed response acceptance
must precede product integration.

Keep SAML inside the existing SSO connector, explicit binding, claim and
session-source authority. It cannot install a separate session subsystem or
replace ownsolid's server-side revocation checks. SP-initiated POST is the
first complete path; additional bindings, unsolicited sign-in and federated
logout require their own complete acceptance and remain unavailable until
implemented. Local logout and connector/session revocation still work.

## Provider and key trust

An administrator imports bounded IdP metadata or approves one configured
private TLS metadata endpoint. Select exactly one expected entity ID and
approved SSO binding/endpoint. Browser input, assertion KeyInfo, redirects and
metadata cannot introduce a new network destination or trust anchor. Reject
remote key references, external entities and external XML signature references.
Do not refresh metadata into new endpoint or signing-key authority without an
audited connector revision and explicit trust policy.

Pin the configured IdP signing certificate set, with explicit validity and
rotation windows. Signature trust is not the server's TLS CA trust. Preserve
an intentional overlap during key rotation and refuse removed keys after that
overlap. Store SP signing/decryption keys only as protected file references.
They have no relationship to entitlement issuance or license signing keys.
Use a fixed SP entity ID and exact metadata/ACS URLs per configured entrance;
request Host or forwarded headers cannot redefine them.

Use RSA-SHA256 or a separately qualified modern signature algorithm. Do not
accept unsigned assertions, SHA-1, arbitrary transform chains, detached/external
references or a custom verifier that returns success without identifying the
validated signed subtree. Assertion encryption needs a separately selected
modern encryption/key-transport path and key rotation proof; encrypted data
still needs signature and subject validation. Do not equate encrypted content
with authenticated content or silently fall back to obsolete algorithms.

## Flow, browser proof and CSRF

Create a fresh server-held request ID and random RelayState for the exact
tenant, connector revision, audience and browser. Linking additionally binds
the current local session, credential epoch and password proof, as the existing
Gitea path does. Keep issuer and connector identity immutable. Return only the
configured provider URL or a bounded first-party form, with no arbitrary return
URL. A terminal or uncertain flow is never exchanged or issued again.

An IdP's POST is cross-site. The current Lax SSO cookie cannot be assumed to
arrive on it. Prototype a distinct short-lived, Secure, HttpOnly, host-only
SAML flow cookie with SameSite=None, bound to the exact audience and protocol.
It conveys only an unguessable browser proof, never a login session. Start the
flow through the normal first-party Origin/CSRF checks. The ACS validates its
server-held request, browser proof, RelayState, IdP signature and exact
recipient before issuing any authority. That protocol-specific CSRF proof is
mandatory; it is not a configuration switch or a global Origin/CSRF bypass.
The ordinary Admin/Portal session cookies keep their existing authority and
transport policy. Cross-site browser cookie behavior needs real browser tests,
including an unavailable-cookie retry outcome.

Require response and bearer-subject `InResponseTo` to match the one claimed
server request, exact response Destination and bearer Recipient to match the
selected ACS, and issuer/audience to match the configured IdP/SP. Check all
applicable Conditions and subject-confirmation times with bounded skew, and
honor a shorter session limit from a used authentication statement. Require
the expected successful status and one unambiguous authenticated subject.
Reject unsolicited responses in this path. Persist the one-use claim before
completion; append the audit and issue the sourced session atomically. Keep
bounded replay identity with the existing flow lifecycle rather than a second
receipt ledger. Replay protection must outlive the assertion's acceptance
window, including clock skew, and survive restart.

The identity key is the configured IdP entity plus persistent NameID format,
qualifiers and exact value under the connector. Validate qualifier scope against
the SP and IdP. No case folding, email matching or transient/email-format
fallback can silently replace it. Require explicit control of an existing
local account to bind it. Attributes are display/input data; group claims do
not become privileges until IAM-110's mapping and the real resource evaluator
authorize them. Provider failure does not make a local account into an Admin.

[SAML Core sections 8.3.6 and 8.3.7](https://docs.oasis-open.org/security/saml/v2.0/saml-core-2.0-os.pdf)
bound entity identifiers to 1,024 characters and persistent identifiers to
256 characters. Validate absolute entity URIs and count Unicode code points,
not just UTF-8 bytes; the persistent value additionally has a 1,024-byte bound.
An omitted NameQualifier/SPNameQualifier can be inferred from this directly
verified IdP/SP context. Resolve it to those exact effective qualifiers before
identity comparison; an explicitly foreign qualifier is refused. This does not
normalize the opaque value. Affiliations, forwarded identity and SPProvidedID
renaming require separate profiles and remain unavailable in this first path.

The OIDC candidate's 255-character subject is not SAML storage admission.
Persist the exact bounded UTF-8 value under immutable connector/IdP/SP scope,
with deterministic binary identity comparison and explicit profile constraints.
Do not concatenate arbitrarily long qualifier strings into a unique index or
truncate/hash away the original identity to make it fit: PostgreSQL documents
[B-tree entry size limits](https://www.postgresql.org/docs/18/btree.html) and
[collation-specific index semantics](https://www.postgresql.org/docs/18/indexes-collations.html).
The candidate storage representation, maximum-byte cases and foreign-tenant
binding uniqueness still need a real SQL prototype and ARC's formal admission.

## Candidate dependency and license inventory

The selected research candidate is
[crewjam/saml v0.6.3](https://github.com/crewjam/saml/tree/v0.6.3), exact tag
`c5725cd857625e4c1f6dbbe5e0b3822def94b581`, published 2026-11-08.
Its [module file](https://github.com/crewjam/saml/blob/v0.6.3/go.mod) requires
Go 1.25. Resolve the final product module graph before admission. The following
versions and source licenses were inspected outside the product; no dependency,
model or dataset is added by this research.

| Module | Selected version | Source license |
| --- | --- | --- |
| crewjam/saml | v0.6.3 | [BSD-2-Clause](https://github.com/crewjam/saml/blob/v0.6.3/LICENSE) |
| beevik/etree | v1.8.1 | [BSD-2-Clause](https://github.com/beevik/etree/blob/v1.8.1/LICENSE) |
| golang-jwt/jwt/v5 | v5.3.1 | [MIT](https://github.com/golang-jwt/jwt/blob/v5.3.1/LICENSE) |
| google/go-cmp | v0.7.0 | [BSD-3-Clause](https://github.com/google/go-cmp/blob/v0.7.0/LICENSE) |
| jonboulle/clockwork | v0.5.0 | [Apache-2.0](https://github.com/jonboulle/clockwork/blob/v0.5.0/LICENSE) |
| russellhaering/goxmldsig | v1.6.1 | [Apache-2.0](https://github.com/russellhaering/goxmldsig/blob/v1.6.1/LICENSE) |
| golang.org/x/crypto | v0.55.0 | [BSD-3-Clause](https://github.com/golang/crypto/blob/v0.55.0/LICENSE) |
| gotest.tools/v3 | v3.5.2 | [Apache-2.0](https://github.com/gotestyourself/gotest.tools/blob/v3.5.2/LICENSE) |
| davecgh/go-spew | v1.1.1 | [ISC](https://github.com/davecgh/go-spew/blob/v1.1.1/LICENSE) |
| kr/pretty | v0.3.1 | [MIT](https://github.com/kr/pretty/blob/v0.3.1/License) |
| pmezard/go-difflib | v1.0.0 | [BSD-3-Clause](https://github.com/pmezard/go-difflib/blob/v1.0.0/LICENSE) |
| stretchr/testify | v1.11.1 | [MIT](https://github.com/stretchr/testify/blob/v1.11.1/LICENSE) |
| golang.org/x/net | v0.57.0 | [BSD-3-Clause](https://github.com/golang/net/blob/v0.57.0/LICENSE) |
| golang.org/x/sys | v0.47.0 | [BSD-3-Clause](https://github.com/golang/sys/blob/v0.47.0/LICENSE) |
| golang.org/x/term | v0.45.0 | [BSD-3-Clause](https://github.com/golang/term/blob/v0.45.0/LICENSE) |
| golang.org/x/text | v0.41.0 | [BSD-3-Clause](https://github.com/golang/text/blob/v0.41.0/LICENSE) |
| gopkg.in/check.v1 | 10cb98267c6c | [BSD-2-Clause](https://github.com/go-check/check/blob/10cb98267c6c/LICENSE) |
| gopkg.in/yaml.v3 | v3.0.1 | [MIT and Apache-2.0 by file](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE); retain NOTICE |

These licenses permit commercial use, redistribution and offline use while
retaining their required copyright, license and applicable notices. The actual
SAML package import closure resolves 18 modules and 25 legal files, including
the ten transitive/test rows above. Preserve the additional BSD-3-Clause
`gotest.tools/v3/internal/difflib/LICENSE` and Go subrepository PATENTS files.
The pinned versions remain the latest tagged SAML and goxmldsig versions at
the 2026-11-13 check. Resolving imports is source inspection, not compilation
or a protocol run. The
private Keycloak fixture's complete application and image dependency notices
also apply; see the [selected provider inventory](oidc-sign-in.md#dependencies-and-acceptance).
Do not copy it into ownsolid or describe the whole provider image as BSD or
Apache only.

## Library integration boundaries

Use the [ServiceProvider protocol owner](https://github.com/crewjam/saml/blob/v0.6.3/service_provider.go),
not the default [samlsp session middleware](https://github.com/crewjam/saml/blob/v0.6.3/samlsp/middleware.go).
The latter creates its own request and JWT session cookies and can replay
forms after login; those are not ownsolid sessions or mutation authority.
Keep form replay and unsolicited sign-in unavailable. Do not register library
metadata, logout or artifact endpoints merely because its middleware provides
them.

Validate a strict bounded XML envelope before expensive DOM/signature work:
encoded/decoded bytes, depth, node/attribute counts, unique IDs, namespace and
allowed message structure. Reject DTDs, entity declarations, external resources,
ambiguous duplicate IDs, wrapping variants and parser round-trip differences.
Read the actual signed element, not another assertion with a matching-looking
subject. Exercise the library's round-trip and signed-subtree behavior using
malicious but correctly signed fixtures; a modified invalid signature alone
does not prove resistance to wrapping.

The pinned source has a per-ServiceProvider `Clock`; use that owner rather than
the deprecated global clock. Its issue/skew and artifact limits still include
package globals. Do not mutate them per connector or concurrently. Select a
stricter per-adapter policy whose accepted window is within the library's hard
acceptance, then check the verified assertion against it.

Several compatibility defaults require explicit constructor policy:
`RejectSHA1Signatures`, `RejectUnknownConditions` and
`RejectExpiredIDPMetadata` must be true, and `IgnoreCertificateExpiry` and
`AllowIDPInitiated` false. They are implementation invariants, not customer
switches. Advertise only `HTTPPostBinding`. Keep encryption unavailable in this
first path; any later encrypted profile also sets `RequireSignedResponseForCBC`
and qualifies its chosen authenticated encryption separately.

The default signature verifier returns the canonical element actually covered
by the signature. Keep it; a custom `SignatureVerifier` without
`SignedElementVerifier` retains the original unverified element instead. The
library can accept a signed Response with several assertions and return the
first, and can accept a missing Destination on an unsigned Response. The strict
outer profile must require one assertion, its own signature and an exact
nonempty Destination before invoking it. Exercise both differences with signed
fixtures. Its `InvalidResponseError.Response` retains the complete XML even
though `Error()` is fixed text; project it immediately to a fixed ownsolid
error and never serialize/log the library error object.

The optional `ReplayCache` is not ownsolid's durable flow/session transaction.
Do not substitute an in-memory cache or an independent pre-completion write
for the one-use flow claim and final transactional assertion/session/audit
authority. That composition and replay retention still need formal admission.
No artifact/SOAP network exchange belongs to the first POST response path. A
nil/default HTTP client is not an approved destination policy. Any later network
binding must use the existing verified private TLS, no-redirect,
byte/deadline/concurrency and endpoint restrictions.

## Native protocol prototype qualification

The external adapter passed eight native Go roots and 29 subcases with race
detection. The final prototype/test import graph still has 18 modules and
25 legal files; no new product dependency is admitted. Its streaming guard
limits the fixture profile to 128 KiB, depth 24, 2,048 elements and 32 attributes
per element, with a separate token budget. It requires unique XML IDs, one
plaintext assertion with its own RSA-SHA256 signature, the exact request and
Destination, one persistent NameID and one bearer confirmation. It delegates
cryptographic verification to the pinned upstream signed-element verifier.

Correctly signed wrong issuer/audience/Recipient/request values, expired/future
and overlong acceptance windows, ambiguous subjects/confirmations, unsupported
algorithms, multiple assertions and wrapping were refused. Positive persistent
subjects retain their exact case, punctuation and qualifiers. Trusted old/new
keys work during an explicit overlap; removing the old key refuses it. Parallel
verifiers passed without package-global clock/parser changes. The returned proof
expiry honors the earliest assertion, bearer, authentication-statement and local
trust deadline.

Two independent positive controls establish the compatibility differences:
the upstream parser accepts the correctly signed Response containing multiple
assertions and the unsigned Response without Destination containing a valid
signed assertion; the strict adapter refuses both. A separate upstream positive
signature check prevents invalid test fixtures from making the negative suite
look successful. The first readonly-module admission refusal and duplicate
signature fixture failures are retained; the fixture now uses upstream
`ConstructSignature` and an indexed tree insertion rather than moving the
unparented child returned by its signing convenience helper.

Evidence set `iam104-protocol-20261113.cknwa8w9`, run `native-dhejf_yw`, retains
the exact source, module sums, legal files and raw race results. The source-hash
manifest SHA-256 is
`95efecaac50af45645db39c8dd650c1bce19b03b2db9a7bffffc4d91865cce93`.
Two live-only roots were explicitly skipped in this native invocation. The
Keycloak/password/HTTP-POST protocol test has not yet run at this checkpoint;
neither a browser-cookie claim nor an installed-flow result is established.
The subsequent 256-character/effective-qualifier corrections and additional
deadline-intersection fixtures are prepared outside the product and have not
yet been rerun under the workstation build lock. The eight-root result applies
only to the exact retained source above, not those follow-up changes.
Repeated/concurrent cryptographic verification deliberately creates no durable
request claim or local authority. Those remain with the existing flow owner.
IAM-104 stays open for the live prototype and all product composition below.

## Alternatives, prototype and finished-item acceptance

Reject a hand-written XML signature implementation, email-only linking,
generic JWT session cookies from the middleware, URL-derived tenant scope,
unsolicited-response shortcuts and disabling signature/time/recipient checks
to make an IdP fixture pass. OIDC is a separate provider path, not a conversion
of SAML assertions into unverified claims.

Prototype outside the product first, with the selected private Keycloak and
actual SP-initiated browser/password/signature exchange over verified TLS.
Exercise a positive persistent subject and independently inspect its exact
qualifiers. Separately test a signed wrong issuer, audience, Destination,
Recipient and request ID; expired/future/bounded-skew assertions; missing and
wrong signatures; nested/duplicate IDs, namespace and wrapping attacks;
oversized/deep XML; unsupported algorithms; old/new/removed signing keys;
missing browser proof, RelayState replacement and concurrent replay. Verify
that protected assertions, attributes, cookies and keys appear in no log,
Problem or audit payload.

Product acceptance additionally requires a current PostgreSQL migration,
typed configuration and key inventory/restore behavior; server-resolved tenant
and current-session checks on every connector, binding and flow operation;
audit rollback and actual COMMIT-uncertainty behavior; durable one-use replay
protection and sourced-session revocation; and a disconnected Admin/Portal UI
for configuration, explicit linking, sign-in, clear refusal and retry. DEV,
ARC and Web own their shared interfaces. The library, a protocol fixture or a
standalone parser passing does not complete IAM-104.
