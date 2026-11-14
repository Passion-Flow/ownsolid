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

The first rendered transport attempt found an invalid fixture assumption: an
IdP page with `Referrer-Policy: no-referrer` sends `Origin: null` on its
cross-site form POST, even though its dedicated flow cookie arrives and its
ordinary Lax and Strict cookies do not. The
[Fetch Origin-header algorithm](https://fetch.spec.whatwg.org/#append-a-request-origin-header)
explicitly sets that header to null for this policy. An exact IdP Origin is not
an authentication mechanism and cannot be required for all valid SAML POSTs.
The next transport-only probe will cover both exact-origin and null-origin
forms, with missing flow-cookie and wrong ordinary-session-cookie controls for
each. It will accept null only at this owned fixture endpoint and grant no
identity authority. Product ACS admission still requires the immutable
server-request/browser-proof/RelayState and signed-response checks above;
ordinary first-party Origin/CSRF enforcement stays mandatory. Missing or
unexpected explicit Origin values remain separately reviewable signals, never
substitutes for the SAML proof. This is a specification reference, with no
runtime dependency, copied implementation, model or dataset.

The target product flow stages a verified response at the ACS and
redirects to a fixed first-party completion endpoint before changing an account
binding or session. The ordinary Lax session cookie can then be checked again
against the initiating cookie digest and actor. Rechecking only the captured
server session on the IdP POST cannot detect that the browser now carries a
different local account's cookie while the old session is still active.
Preserve that browser-change refusal rather than broadening the ordinary
session cookie to SameSite=None.

ASP.NET Core demonstrates this separation in its
[remote authentication handler](https://github.com/dotnet/aspnetcore/blob/v10.0.0/src/Security/Authentication/Core/src/RemoteAuthenticationHandler.cs)
and [external-account linking callback](https://github.com/dotnet/aspnetcore/blob/v10.0.0/src/Identity/UI/src/Areas/Identity/Pages/V5/Account/Manage/ExternalLogins.cshtml.cs):
the remote result is staged, then a GET callback reacquires the local user and
checks the expected account before linking. This is an architectural reference,
not a new dependency or copied implementation. ownsolid's staged result must
remain server-side, one-use, bound to the exact request and source revision,
and expire no later than the verified assertion. The browser receives only a
protected random continuation proof, never an assertion or identity claim set.
The finish URI is fixed by the server, with no provider data or arbitrary return
URL. The [rendered transport probe](#rendered-cross-site-cookie-qualification) now
qualifies cookie delivery and swapped-local-cookie refusal in its private
fixture. Actual Identity-backed completion remains unproven.

The rendered transport probe will reuse the repository's existing Web QA trust
pattern: exact owned fixture certificate key pins, fixed literal hostname
mapping, and `ignoreHTTPSErrors: false`. Precheck the generated leaf chain,
hostname and validity, refuse every page origin outside the owned fixture,
and require an unpinned-certificate negative control. Do not modify the system
trust store or use a personal browser profile. Use distinct registrable sites
and observe the browser's `Sec-Fetch-Site` as well as actual cookie delivery;
two subdomains of one site would not test this boundary. The Mac probe is not
a disconnected installed-flow result.

This uses existing internal QA tools only: Go 1.27.3 (BSD-3-Clause), Node
26.13.0 (MIT with bundled notices), Playwright/playwright-core 1.64.1
(Apache-2.0 plus their NOTICE/ThirdPartyNotices), and the already installed
Chrome for Testing 156.0.8078.4. Google's
[testing-browser documentation](https://developer.chrome.com/docs/automation-and-testing/chrome-for-testing)
describes versioned testing binaries without auto-update. The executable is
subject to [Chrome's terms](https://www.google.com/chrome/terms/) and its bundled
open-source credits, not a blanket BSD license. Retain those exact notices
and runtime identity in the private QA evidence. The probe uses no media and
only owned loopback content; no browser binary is copied into the product,
redistributed, downloaded at runtime or admitted as an installer dependency.
This research adds no product dependency, model or dataset.

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
through Identity's actual source/session authority, then require the initiating
ordinary browser cookie again at first-party completion. Do not accept a session ID
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

### Isolated claim boundary

The isolated claim experiment is limited to authenticated account-link request claims.
Use a separate unnumbered test table, anchored to the actual scoped connector,
user and session, and the existing Identity password-confirmation/current-actor
owners. Store only request identity, browser/RelayState digests, immutable
captured actor and callback scope, connector revisions, expiry and one claim ID.
A server-owned tenant is resolved from installation authority; callers supply
no tenant or captured actor. The IdP POST path deliberately has no ordinary
session-cookie argument. It reconstructs only the stored private actor and
rechecks actual user/source/session/credential authority through the existing
user -> sorted source/target connectors -> exact session -> installation order.
Under that transaction it exclusively changes pending to claimed, and returns
a private request handle only after confirmed COMMIT. No PKCE/client secret,
OAuth exchange, signed assertion, verified-result stage, binding or session is
invented. This tests one prerequisite, not an authentication implementation.

Exercise Admin and Portal, local and different-connector sourced actors,
foreign tenant/connector/audience and browser/RelayState substitutions, expiry,
logout, account/credential epoch changes, source/binding revocation and stale
target revisions. Force actual concurrent row waits, cancellation, rollback,
process-owner reconstruction/replay and both PostgreSQL COMMIT loss modes;
errors must return no private claim handle and never automatically resend.
Use real audit rollback only for an event actually written by this experiment,
with synthetic action names confined to its private fixture. Acceptance of
this boundary requires matching native race and disconnected PostgreSQL cases,
not additional fake protocol successes. The first-party cookie comparison and
signed-result lifecycle remain separate work. Existing first-party start
Origin/CSRF checks are not replaced by direct repository tests.

This adds no product dependency, model or dataset. It reuses the repository's
pinned Go/pgx/UUID/test-fixture graph and its recorded licenses; no new SAML
library enters the claim-only prototype. Existing Identity/SSO source is the
authority for its current actor, lock order and transaction outcome semantics,
not a copied identity model or a configurable bypass.

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

## Rendered cross-site cookie qualification

An owned Mac arm64 Chrome for Testing 156.0.8078.4 process with Playwright
1.64.1 exercised six actual form submissions between distinct registrable
HTTPS sites. One native Go race-test root passed without a skipped case. Both
`no-referrer`/null-Origin and `strict-origin`/exact-IdP-Origin variants observed:

- The ACS received POST with `Sec-Fetch-Site: cross-site`, the dedicated
  SameSite=None flow cookie, and neither ordinary Lax nor Strict cookies.
- A 303 to a fixed SP completion GET recovered the original ordinary Lax
  cookie and a new host-only Lax continuation cookie. That GET still reported
  cross-site fetch metadata; being an SP endpoint does not imply same-site
  navigation provenance. Strict cookies remained absent on the redirect.
- Removing the flow cookie returned ACS 403 and never reached completion.
- Replacing the ordinary local cookie while at the IdP still reached the ACS,
  but the completion GET returned 403 against the initiating cookie value.

The fixture cookies were Secure, HttpOnly, host-only and Path=/, with separate
flow/ordinary/Strict/continuation roles. Generated TLS certificates passed
chain, DNS and validity checks before their exact leaf-key pins were supplied
to the disposable QA browser. Contexts kept `ignoreHTTPSErrors: false`, service
workers were blocked and page requests were restricted to the owned origins.
A separately generated unpinned certificate was actually refused. No system
trust or personal browser profile changed. The runtime's complete bundled
credits were captured with SHA-256
`695e9c5e9e3c04a10f895ee501e501940eb7a75808269708517bcbb4c2860fef`.
The private license inventory retains the QA-tool notices and credits; no
browser or new dependency enters the product or delivery bundle.

Evidence is `iam104-browser-20261113.44wtq89s`, run
`qualification-puevh9h9`, input `b3c2ef795698`. Its source-manifest SHA-256 is
`43e2c23944a1ca897e55678296282a9c339a31ae8696e3535e5eb1a855e8f239`.
Frozen and working source hashes matched. Sanitized server observations,
browser cookie attributes, exact runtime versions, raw race output and both
failed exact-Origin-assumption attempts remain outside Git. All owned Node,
browser and Go test processes exited and released their loopback listeners
and workstation lock.

This is a browser transport qualification only: a synthetic IdP form carried
no SAML assertion, and the fixture verified no signature, contacted no
Identity/SQL authority and created no login, binding or session. It has no
one-use durable claim or restart/replay proof. The native Mac run was not
network-isolated or an installed Linux amd64/disconnected acceptance. Actual
Identity-backed account switching, atomic claim/completion, signed-provider
composition, public configuration and rendered product UI remain open. No
product path, schema, public route or roadmap tick changed.

## Captured current-actor claim qualification

The isolated claim-only candidate passed seven roots, 31 independent leaves
and 36 passing names in both native race and disconnected Linux arm64
PostgreSQL 18.6/pgvector 0.8.7 runs. The complete passing-name sets and frozen
versus working source hashes matched. It adds one unnumbered private test
table through a Go test overlay, leaving product source, dependency files,
formal schema, startup and public routes unchanged.

The start actually authenticates an ordinary local or sourced session, confirms
its current local password, rechecks the opaque confirmation and captures
user/session/token digest, epoch, credential version, audience, exact configured
callback and target revisions. Its tenant comes from actual installation
authority. The dedicated browser proof is purpose-, tenant-, connector-,
audience- and callback-bound; raw browser/RelayState values are never stored in
the request table. SQL prevents rewriting captured authority or a claimed row.
No OAuth credential or PKCE verifier is invented. Claim has no ordinary
session-cookie or caller-supplied tenant/actor argument.

Admin/Portal local and different-connector sourced actors passed through the
actual Identity current-actor and SSO SourceGuard owners. Under the existing
user -> sorted source/target connectors -> exact session -> installation order,
claim rechecks credential and target revisions, exclusively changes pending to
claimed and appends its synthetic audit in the same transaction. Only a
confirmed COMMIT returns the private request handle. Owner reconstruction
refused durable replay; this is not a process-restart test. Independent counts
confirmed that claiming created no local binding or session.

Actual PostgreSQL cases refused replaced browser/RelayState, wrong entrance or
connector, changed configured callback, an otherwise copied foreign-tenant
request, a foreign-tenant start, wrong password, captured-actor rewrite and
expired requests. Logout, isolated idle/absolute expiry, disabled account,
changed account epoch/password version, disabled or changed source connector,
revoked source binding, stale/disabled target and changed installation tenant
all returned no handle and retained pending state without a claim-success
audit. Source/target faults are controlled repository fixtures, not additional
public administration acceptance.

Controlled row waits exercised two concurrent claimers and a canceled waiter:
there was exactly one winner, cancellation drained the worker without changing
the request, and explicit retry succeeded. Independent diagnostic connections
observed actual PostgreSQL blocking without competing for the fixture's two
application connections. A source revoker holding its connector could acquire
and revoke sessions while claim waited for that connector; the later claim was
refused. An actual audit trigger failure rolled back request claiming, and
explicit repair/retry passed. A real PostgreSQL wire forwarder dropped COMMIT
before forwarding and suppressed its acknowledgement after server success.
Each observed exactly one COMMIT, returned `COMMIT_UNKNOWN` with an empty
handle, and independent readback found zero/one atomic claim plus audit.

Evidence is `iam104-claim-20261113.5tuta2y1`, run
`qualification-ynqxh50s`, qualified input `bb101552d8c4`. Source-manifest
SHA-256 is
`4bdee7d4c795f61fe8fcedef4d8e21c8e594b83c8ee4c728f6b9ebdcff96740f`.
The SQL, overlay, pinned base-owner hashes, raw outputs, actual wire controls,
container inspections and matching names remain outside Git. Earlier failures
retained macOS temporary-directory alias rejection, bounded fixture WAL
exhaustion, invalid fault injections against existing SQL constraints and an
observer queued behind the two-connection pool. The final driver resolves its
temporary path and bounds WAL in its owned disposable database. Those changes
neither relax private-file/SQL checks nor change product configuration. Owned
containers, anonymous storage and the native bridge were removed and checked
independently. No test-server or peer resource was changed.

The claim-only code reuses the existing pinned product Go graph: pgx is MIT,
Google UUID is BSD-3-Clause, Go and its crypto/system dependencies retain their
BSD notices, and PostgreSQL/pgvector retain their PostgreSQL-license notices.
No new dependency, model, dataset or installer redistribution is introduced.
This is not a signed SAML response, actual AuthnRequest, metadata/trust-profile
admission, staged verified result, first-party completion, account binding,
sourced SAML session, MFA flow or installed browser acceptance. The actual
SourceGuard in these tests protects existing Gitea-sourced sessions. It does
not admit SAML as a serving provider. Structured telemetry for a future product
path is also unqualified. IAM-104 remains open.

## Signed-result/request composition approach

Compose the already qualified strict verifier and persistent identity resolver
with the actual-Identity claim overlay in a new external fork. Use an alternate
private Go module file and sums, with the existing Control graph and the exact
previously reviewed SAML versions, rather than changing product dependencies.
Record the new graph and verify every added module's notices against the prior
inventory before compilation. Do not downgrade shared Control dependencies.

At start, capture the immutable identity-context ID, its revision and approved
metadata digest under the existing target fence. Claim and staging must compare
that source again. Retain the exact verified request ID in the verifier's private
envelope; a valid assertion for another claimed request is not transferable.
Stage only after strict upstream signature/profile verification, then recheck
actual captured actor, credential, connector and profile authority. In one
transaction, resolve the exact external identity, reserve the scoped signed
assertion ID, store a minimized verified result and a random continuation digest,
change claimed to verified and append the synthetic stage audit. Expire the
result no later than either the request or the verified assertion. No XML,
attributes, private key or raw continuation is stored as a result payload.

The scoped assertion reservation belongs to the flow/result lifecycle, not a
second receipt ledger. Keep it until the assertion can no longer be accepted,
including any applicable clock skew. The experiment has no maintenance deletion
path and therefore makes no retention/cleanup production claim. One-time staging
and context fences must survive owner reconstruction. Correctly signed wrong
request/context inputs, changed profile/source authority, duplicate assertion
IDs on different requests, audit rollback, actual concurrent staging and both
COMMIT wire-loss modes must return no continuation on failure. No automatic
resend or signed-result fabrication is permitted. Product admission still needs
first-party completion with the initiating ordinary cookie, local binding/session
and source ownership, MFA policy, formal schema/contracts/startup and UI.

The private alternate graph preserves the workspace's newer testify 1.12.1,
`x/net` 0.61.0 and OAuth2 0.37.0 selections rather than using the Control module's
lower standalone requirements. Its seven differences from the workspace
baseline are etree 1.8.1 (BSD-2-Clause), crewjam/saml 0.6.3 (BSD-2-Clause),
clockwork 0.5.0 (Apache-2.0), kr/pretty 0.3.1 (MIT), goxmldsig 1.6.1
(Apache-2.0), objx 0.5.3 (MIT), and gotest.tools/v3 3.5.2 (Apache-2.0 plus its bundled difflib license). Six exact-version license inventories match the earlier private SAML
inventory. The newly selected
[objx 0.5.3 license](https://github.com/stretchr/objx/blob/v0.5.3/LICENSE) was
read from its checksum-verified source archive. All seven retain eight legal
files in the new private evidence. These licenses allow commercial and offline
use and redistribution with their required notices; test-only graph membership
does not remove those obligations. No private alternate module file or dependency
archive is copied into the product or repository.

The [signed-result staging experiment](#signed-result-staging-qualification) now
qualifies this private graph and the bounded composition described below. The
external experiment retains the existing MIT/BSD/Apache notices for its exact
SAML graph and PostgreSQL licenses for its fixtures; it adds no model, dataset,
product binary or runtime network access.

## Signed-result staging qualification

The external composition passed 23 roots, 96 leaf names and 111 complete
passing names in both native race and disconnected Linux arm64 PostgreSQL
runs, without a skipped test. Both passing-name sets and frozen/current source
hashes matched. This comprises the ten strict protocol roots/46 leaves, seven
actual-Identity claim roots/33 leaves and six staging roots/17 leaves. The two
new claim cases refuse a profile revision change or disable after start. The
protocol fixtures sign actual XML with generated private IdP keys and use the
selected upstream signature verifier; this run starts no Keycloak or product
HTTP/browser worker.

A private alternate module/sum pair and Go source overlay compose the actual
Identity/SSO owners with the existing strict verifier and exact persistent
identity resolver. The seven-module/eight-legal-file difference matches its
reviewed inventory exactly. The workspace's newer selected dependency versions
remain selected; no product module, sum file or dependency changed. The verifier
now retains its exact validated request ID in its private proof, and request
start/claim additionally bind the approved identity-context ID, revision and
metadata digest. Those fields have SQL immutability and scoped foreign keys.
The identity resolver borrows the selected transaction through its executor,
without opening or committing an independent identity write.

Staging actually rechecks the captured current actor/credential, target and
profile under their existing fences; checks the exact claimed request/claim ID,
audience, callback and source snapshot; resolves the exact external identity;
reserves the scoped assertion ID; stores a minimized verified result and a
continuation digest; changes claimed to verified; and appends a synthetic audit
in one transaction. The continuation is generated randomly and returned only
after confirmed COMMIT. The result stores no XML, attributes, raw continuation
or private key. Independently inspected request-shorter and assertion-shorter
cases used the exact minimum deadline, and session/binding counts did not grow.

Correctly signed proofs for another actual claimed request were refused.
A proof produced under an independent actual foreign Identity/installation
owner and approved signing context was also refused locally. Profile revision
or disable, logout, changed password version, stale target revision, changed
callback and a signed proof whose deadline had passed the actual database
clock all returned no continuation and created no identity/result/stage audit.
Reusing an assertion ID on two separately signed requests in the same scope
was refused; an explicit fresh signed assertion succeeded. Owner reconstruction
refused staging replay. This is not a process-restart qualification.

Two concurrent stagers selected one continuation and retained one result,
identity and stage audit. An actual audit trigger failure rolled all four
changes back; explicit repair/retry passed. Both actual PostgreSQL COMMIT
wire-loss modes returned `COMMIT_UNKNOWN` and an empty continuation with one
observed COMMIT and no automatic resend. Independent readback found zero or
one atomic verified flow, external identity, result and stage audit. Previous
actual source-before-session, cancellation and claim-loss cases passed again
under the composed graph.

Evidence is `iam104-stage-20261113.ywp4mphe`, run
`qualification-xnnbmxs7`, qualified input `3094de1e7a59`. Source-manifest
SHA-256 is
`597e4cd89e6f492e011ad7aca0504dd9e78e9abc1a4185ac4c7c67bb5c34a4fe`.
Raw outputs, exact source/module hashes, delta legal files, matching case names,
wire controls and container inspections remain outside Git. The owned
containers, storage, native bridge and test processes were removed. Earlier
attempts retained a copied fixture-name collision and an invalid nested XML
fixture lookup; fixes changed the test helpers, not signature/profile checks.
An optional whole-module tidy failed offline on unrelated uncached dependency
tests; it was not a passing check. The actual selected-module readback and
both qualification builds/tests ran with the proxy and checksum service off.

This introduces four private unnumbered tables across context, external
identity, request and verified result. It adds no product source, formal
migration, startup dependency or public route. Its source actors are existing
local/Gitea sessions, and the signed response is a generated IdP fixture.
No real AuthnRequest/provider POST/ACS is joined to the earlier browser probe,
and this run is not an installed Linux amd64 acceptance. No first-party finish,
account linking, sourced SAML session, MFA policy, maintenance/restore ownership,
public configuration or product UI is qualified. Result retention is not yet
bounded by a production cleanup owner. Structured telemetry remains unqualified.
[ADR 0084](../decisions/0084-bind-saml-results-to-request-and-browser-authority.md)
records the target boundary. IAM-104 remains open.

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
