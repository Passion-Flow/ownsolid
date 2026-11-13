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

### Browser claim and captured-actor handoff

The current `claim_repository.go` authenticates the previous browser cookie,
compares its digest, resolves the Gitea provider, and decrypts OAuth PKCE/client
credentials before claiming the row. `flows.go` then exchanges an OAuth code;
its numeric Gitea subject contract is not the new persistent SAML identity.
Do not feed a SAML response through that path with invented OAuth credentials
or skip its previous-session checks when the Lax cookie is absent.

[Chromium's SameSite documentation](https://www.chromium.org/updates/same-site/)
requires Secure for SameSite=None cookies. The dedicated SAML flow cookie must
carry only the short-lived browser proof; it cannot become a local session.
For account linking, capture the authenticated local session reference, token
digest, entrance, account epoch and credential version at the first-party start.
After validating the flow cookie/RelayState, recheck that server-held reference
through Identity's actual source/session authority. Do not accept a session ID
from the POST or add a public resume-by-session-ID operation. Explicit sign-out,
credential/factor changes, idle/absolute expiry and source revocation must stop
that captured authority even when its SAML browser proof remains valid.

The next external experiment must retain the existing current-actor lock order
and exclusive pending-to-claimed transition, with claim COMMIT confirmed before
assertion verification or completion proceeds. A returned verification envelope
also needs the exact request ID checked by the verifier, in addition to its
tenant/connector/trust snapshot. Completion must compare it with the claimed
request and retain bounded signed-assertion replay identity in that lifecycle.
No automatic resend follows COMMIT uncertainty, and a claim error releases no
worker proof or session. Binding, source session and audit remain one completion
transaction; the identity lookup prototype alone proves none of them.

Before serving admission, exercise actual Identity-backed link/login claims,
missing ordinary session cookies on the IdP POST, cross-tenant/audience browser
proof replacement, concurrent claims, source/logout/password interleavings,
restart/replay and actual COMMIT loss. A Go cookie jar does not enforce browser
SameSite behavior; the distinct-cookie path also needs a rendered browser with
verified TLS. Ordinary session/Origin/CSRF policy must remain enforced.

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
The [SQL experiment below](#persistent-identity-sql-qualification) qualifies the
selected exact-identity representation and scope constraints. Actual binding
composition and ARC's formal admission remain open.

### Persistent-identity storage experiment

Normalize the immutable verified IdP/SP context into one tenant/connector scope
row. Store both exact entity IDs there, with the selected persistent NameID
format. The already-qualified verifier resolves omitted qualifiers to those
same configured entities and refuses explicit foreign qualifiers. An identity
row can therefore reference that scope and retain the exact NameID value;
it need not repeat several potentially 4 KiB entity strings in a unique index.
Use a tenant-scoped foreign key and uniqueness over tenant, scope UUID and
the bounded value. A local UUID identifies the stored row, not a replacement
for the upstream identity. Retain and return the full verified identity tuple.
Do not change or admit the OIDC binding representation as part of this experiment.

Use `text COLLATE "C"` with explicit character/byte checks for both entity IDs
and the opaque value. PostgreSQL's
[character-type contract](https://www.postgresql.org/docs/18/datatype-character.html)
allows some overlength `varchar(n)` inputs or explicit casts to truncate,
including excess spaces. That cannot silently change an authenticated NameID.
Avoid bounded-varchar casts, `char(n)`, trimming, case folding, Unicode
normalization and collation changes. Entity rows retain their 1,024-character/
4,096-byte bounds; values retain 256 characters/1,024 bytes. The reduced index
must work at the maximum value size without relying on TOAST compression.
Changing configured issuer/SP identity requires a new scope rather than
rewriting one that already identifies an external principal.

Prototype this outside the product using the existing signed-assertion verifier
as the positive producer and real PostgreSQL as the constraint owner. Cover
full-length Unicode contexts/subjects, exact case/space/normalization identity,
omitted/effective qualifier equivalence, foreign qualifiers and tenants, scope
immutability, duplicate/concurrent resolution, rollback and index lookup.
Direct SQL must refuse overlength values instead of quietly truncating them.
No table row creates a local account, binding, role or session. Actual one-use
browser claims and current-actor/session/audit completion remain separate,
required integration work.

The storage consumer must receive a server-bound verifier result, not an
issuer/NameID tuple alone. Two tenants can configure identical entity names
while approving different signing metadata. Load the tenant/connector context,
revision and approved metadata fingerprint from the trusted store before
constructing the verifier. Verify against that exact metadata, then carry that
server-selected context with the result. Resolution rechecks the same tenant,
connector, context, revision and fingerprint under its current-context lock.
A valid result from the other tenant or trust revision is still refused even
when its issuer/SP/NameID strings match. This fingerprint describes approved
trust metadata; it never replaces or hashes away the exact external identity.
Test shared entity names, foreign valid proofs and stale revisions explicitly.
Trust-metadata or enabled-state changes must advance the current revision;
the database experiment must refuse an unchanged or decreasing revision.

The experiment can reuse the pinned SAML/XML graph and pgx v5.11.0 from the
[qualified OP database inventory](catalog-oidc-provider.md#durable-protocol-prototype-plan-and-additional-licenses).
Resolve and inspect their combined graph before compiling. Preserve all SAML,
MIT/BSD/Apache and PostgreSQL/image notices; no model or dataset is involved.
This representation remains a standalone experiment pending formal admission.

The combined source-inspection graph in `iam104-storage-20261113.edr2wpvk`
resolved 26 modules and 36 legal files. Exact versions/checksums and retained
notices matched the previously inspected SAML/OP inventories except for the
three Go submodules below. Their LICENSE and PATENTS bytes each matched the
official pinned source and were retained with the combined inventory.

| Additional selected version | Primary license |
| --- | --- |
| `golang.org/x/mod v0.38.0` | [BSD-3-Clause](https://github.com/golang/mod/blob/v0.38.0/LICENSE) |
| `golang.org/x/sync v0.22.0` | [BSD-3-Clause](https://github.com/golang/sync/blob/v0.22.0/LICENSE) |
| `golang.org/x/tools v0.48.0` | [BSD-3-Clause](https://github.com/golang/tools/blob/v0.48.0/LICENSE) |

All three permit commercial/offline use and redistribution with their notices.
These graph entries do not enable a tool or service at runtime. This inspection
has not compiled or run the combined protocol/storage candidate and changes no
product module, schema or route.

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

The external adapter passed ten native Go roots and 41 subcases with race
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
trust deadline. The explicit deadline-intersection controls passed for all four
sources. A 256-character/four-byte-UTF-8 identifier is accepted exactly, while
257-character values are refused. Explicit and correctly inferred IdP/SP
qualifiers resolve to the same identity; empty/foreign qualifiers and
SPProvidedID are refused. Distinct Unicode forms remain distinct opaque values.

Two independent positive controls establish the compatibility differences:
the upstream parser accepts the correctly signed Response containing multiple
assertions and the unsigned Response without Destination containing a valid
signed assertion; the strict adapter refuses both. A separate upstream positive
signature check prevents invalid test fixtures from making the negative suite
look successful. The first readonly-module admission refusal and duplicate
signature fixture failures are retained; the fixture now uses upstream
`ConstructSignature` and an indexed tree insertion rather than moving the
unparented child returned by its signing convenience helper.

Evidence set `iam104-protocol-20261113.cknwa8w9`, run `native-00n8jui8`, retains
the exact source, module sums, legal files and raw race results. The source-hash
manifest SHA-256 is
`efdcfcdd00b3c8777c7c0d45caad4e6d8ae00ececb08ce7982150e7e2bc9f844`.
Two live-only roots were explicitly skipped in this native invocation and run
separately against the private provider below. The earlier eight-root source
remains retained separately; it is not the input of this final qualification.
Repeated/concurrent cryptographic verification deliberately creates no durable
request claim or local authority. Those remain with the existing flow owner.
IAM-104 stays open for all product composition below.

## Actual private-provider protocol qualification

Keycloak 26.8.2 passed the actual SAML protocol root and its separate readiness
probe using the exact native-qualified sources above. Two new SP requests each
completed the real IdP password form, received the HTTP-POST response form and
verified its signed assertion over the configured private TLS transport. The
registered callback action and returned RelayState matched exactly. The
persistent subject was stable across the two independently authenticated flows;
their request/assertion IDs differed. Substituting the second request ID or
tampering with the signed subject yielded no proof.

The disposable Linux arm64 provider used `--network none`, no published host
ports, a private CA, approved DNS/SNI and literal loopback dialing. Its manifest
is
`quay.io/keycloak/keycloak@sha256:923a431c08971d49727a857e64870d8d63b5bf294a730e5dde729b5138c26c9e`.
Red Hat OpenJDK 21.0.13 and the image identity match the previously inspected
[provider/image/JDK legal inventory](oidc-sign-in.md#protocol-prototype-qualification).
No image is admitted for product distribution. The fixed provider configuration
uses native RSA-SHA256 Response/assertion signatures, persistent NameID and a
60-second assertion lifetime, with encryption disabled for this first profile.
The attribute names were checked against the pinned
[Keycloak SAML source](https://github.com/keycloak/keycloak/blob/26.8.2/services/src/main/java/org/keycloak/protocol/saml/SamlConfigAttributes.java).

Run `live-gapeddru` retains image/runtime identities, private fixture inputs,
source hashes matching `native-00n8jui8`, raw results and cleanup. The workstation
build lock covered native tests, cross-compilation and the live provider run.
The owned container and its anonymous storage were removed; only dependency
caches and private evidence remain. No test server remains running.

This proves the external protocol approach. The probe extracts and validates the
IdP's POST form; it does not send it to an installed ownsolid ACS or establish
SameSite cookie delivery, a durable one-use browser claim, a binding or a local
session. A real rendered browser, PostgreSQL/current-actor composition, formal
configuration/schema/dependency admission and the public/UI flow remain open.
These are not Linux amd64 installed-flow results. The publication changes
research only; its layout, roadmap and whitespace checks do not substitute for
the future feature's full repository and installed-flow acceptance.

## Persistent-identity SQL qualification

The combined candidate passed fifteen roots, 73 independent leaves and 82
named tests in each of native Go race/loopback PostgreSQL and disconnected
Linux arm64/PostgreSQL. The new storage portion accounts for five roots,
27 independent leaves and 31 names per topology. The other ten roots retain
the qualified XML/signature checks. The two Keycloak-only roots were explicitly
unconfigured and skipped; this run did not repeat the earlier real IdP flow.

Positive storage inputs came from freshly RSA-SHA256-signed fixture assertions
accepted by the actual selected verifier. The fixture uses two unnumbered,
synthetic tables, not product connectors or bindings. Before verification it
loads its server-owned tenant/connector context, revision and metadata digest
in a short transaction; it commits that snapshot before XML verification.
The resulting private envelope carries that authority. Resolution rechecks
the exact current source under a shared lock in the caller's transaction.
It does not commit independently or create any local sign-in authority.

The actual database preserved 1,024-character IdP/SP URIs of 4,084 UTF-8 bytes
each and a 256-character/1,024-byte opaque subject. The full verified identity
tuple round-tripped unchanged. The maximum NameID occupied 1,028 bytes inline,
including its varlena header, without compression. Case, trailing spaces and
precomposed/decomposed Unicode produced distinct identities; omitted and
explicit effective qualifiers resolved to the same stable identity. Direct SQL
refused overlength characters, four-byte values and excess trailing spaces
instead of truncating them. An actual analyzed lookup among 2,001 fixture
identities used an index and returned exactly the maximum-size identity.

Foreign-tenant connector lookup and a direct foreign-context foreign key were
refused. A valid proof from another tenant with the same entity names and
subject could not resolve under the local tenant. Its different approved
signing metadata could not construct a verifier for the local snapshot either.
Immutable tenant/connector/context/entity/format and identity-value rewrites
were refused. Enabled/metadata changes required an advancing revision, and a
decreasing revision was refused. Disable/reenable did not revive the old proof;
fresh verification used the current revision. Foreign qualifier/issuer/SP,
format and expired-result inputs returned no identity.

Actual PostgreSQL uniqueness waits covered an owner committing, rolling back
and a waiting resolver being canceled. Commit returned one shared stable ID;
rollback let the waiter create a new ID; cancellation drained the actual SQL
wait and explicit retry succeeded. Caller rollback left no identity row. These
are identity-resolution transactions, not accepted authentication replays:
the real browser/request one-use claim remains mandatory before completion.

Evidence is `iam104-storage-20261113.edr2wpvk`, run
`qualification-uf5y6rln`, qualified input `de19ee6842d2`. Source-hash manifest
SHA-256 is
`76b31d7bbbb5d1d8a4987baa4627bbcf3f1e8f67b83e7a4495cee63ba40e0a70`.
Frozen/current source and native/disconnected passing names matched. The
26-module/36-notice inventory, source, raw outputs and container/network
inspection remain outside Git. Both owned containers, anonymous storage and
the native bridge were removed. The earlier compile-only XML/snapshot helper
variable collision was corrected without changing the protocol verifier.

No product source, dependency, migration, binding, session, public contract or
UI changed. The experiment's tenant source is its private server fixture, not
actual Identity/current-actor composition. It does not qualify an installed
ACS, browser cookie delivery, durable one-use claim, audited account linking,
session issuance or actual provider-to-storage worker. IAM-104 remains open.

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
