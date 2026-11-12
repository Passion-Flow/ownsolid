# Directory login and group synchronization (IAM-103)

## Primary sources and scope

[Keycloak's LDAP federation](https://www.keycloak.org/docs/latest/server_admin/index.html#_ldap)
separates directory connections, identity mapping and full or changed-user
synchronization. Ownsolid needs the complete administrator configuration,
connection test, explicit account binding, user sign-in and synchronization
result path. A successful bind alone does not implement that capability.

[RFC 4513](https://www.rfc-editor.org/rfc/rfc4513.txt) distinguishes anonymous,
unauthenticated and authenticated binds and requires protection for password
authentication. [Microsoft's session security guidance](https://learn.microsoft.com/en-us/troubleshoot/windows-server/active-directory/ldap-session-security-settings-requirements-adv190023)
distinguishes TLS simple bind from SASL channel binding: TLS simple bind uses
the verified TLS channel, while the SASL CBT setting does not apply to that
mode. Do not advertise a TLS simple-bind implementation as Kerberos, NTLM or
SASL channel-binding support, and never recommend weakening directory policy.

The first path uses LDAPv3 simple bind over verified LDAPS with a read-only
search identity, bounded searches and separate connections for user binds.
Use real OpenLDAP for the first disconnected protocol prototype. Qualify AD
attribute and group semantics separately; an OpenLDAP fixture is not a Windows
domain-controller run. StartTLS and SASL remain additional paths, not insecure
fallbacks selected after a failed LDAPS connection.

## Identity and authentication

An administrator selects a typed, approved directory origin, dial addresses,
CA file, search base, search identity and credential file. Browser input selects
an existing connector, never a host, CA, base DN or search expression. Reuse the
existing current-session, tenant, connector-revision and audit boundaries.
The directory reader receives the minimum attributes and read privileges.

Bind the search identity, look up one user with a server-approved attribute and
an [RFC 4515 escaped assertion value](https://www.rfc-editor.org/rfc/rfc4515.txt),
and require exactly one complete result. Reject empty passwords locally before
opening a socket. Authenticate with that returned DN on a separate verified
TLS connection. Do not rebind a shared search connection as different users,
construct DNs by interpolating usernames, follow referrals, or interpret a
partial search result as an authenticated identity. Map provider diagnostics
to fixed public errors without disclosing DNs, credentials or directory text.

Keep the directory identity separate from mutable display attributes.
[RFC 4530 entryUUID](https://www.rfc-editor.org/rfc/rfc4530.txt) identifies an
entry across renames; AD's [objectGUID](https://learn.microsoft.com/en-us/windows/win32/adschema/a-objectguid)
is a system-assigned immutable 16-byte value. Persist the connector and stable
identifier, with an explicit syntax for each directory profile. Preserve raw
AD GUID bytes through a canonical encoding; do not accidentally reinterpret
their mixed-endian display form. A DN, email, UPN or login name is not a stable
binding key. Deleted and recreated entries with the same name cannot inherit
the old binding.

Preserve the existing explicit binding to a local account and proof of its
control. Email equality cannot link identities. New directory enrollment must
use an explicitly reviewed provisioning policy; it cannot silently create a
platform administrator. Existing local recovery accounts remain usable during
a directory outage. Directory passwords are never retained, logged, audited
or retried automatically after an indeterminate bind. Disabling a directory
connector or local account must fence sourced sessions on the server.

## Complete and bounded group synchronization

Use an explicit page loop with [RFC 2696](https://www.rfc-editor.org/rfc/rfc2696.txt)
controls, fixed search semantics and a deadline. Bound page bytes, entries,
total groups, total memberships, cookie bytes, number of pages and concurrent
operations through validated policy. Require the expected paging response;
reject repeated/nonprogressing cookies and referrals. The library's
[SearchWithPaging](https://github.com/go-ldap/ldap/blob/v3.4.15/v3/search.go)
buffers the entire result and can return collected entries with an error.
It is not the synchronization owner and cannot turn a partial scan into a
successful generation.

AD [range retrieval](https://learn.microsoft.com/en-us/previous-versions/windows/desktop/ldap/searching-using-range-retrieval)
is independent of entry paging. A group can return `member;range=...` instead
of all `member` values. Read contiguous bounded ranges until the terminal `*`,
and reject overlapping, missing, nonprogressing or malformed ranges. Never
assume a fixed server limit or interpret an empty ordinary `member` attribute
as an empty group when ranged values are present. Resolve membership references
to stable IDs, with bounded nested-group traversal, cycle detection and a
clear refusal when references cannot be resolved consistently.

Stage a complete candidate generation under the server-resolved tenant and
connector revision. Publish its group/membership changes atomically only after
all pages, ranges and references succeed and the connector authority is
rechecked. An interrupted, capped, expired or invalid scan retains the last
complete generation and exposes a retryable failure. In particular, an outage
must not publish an empty directory or mass-remove memberships. A completed
scan can identify real removals; define and audit their local binding/session
effect before granting authority from the synchronized data.

Synchronization mirrors external group identity and membership. IAM-110 owns
the reviewed group-to-role mapping and preview; IAM-107/IAM-108 own the actual
role and protected-resource evaluator. A mirrored group name alone grants no
role. Manual and scheduled synchronization share one bounded job owner,
single active run per connector, durable result and cancellation semantics.
No new receipt ledger is needed.

## Stable references and nested-group prototype

The [Keycloak 26.8.2 LDAP mapper documentation](https://github.com/keycloak/keycloak/blob/26.8.2/docs/documentation/server_admin/topics/user-federation/ldap.adoc)
separates group inheritance, membership attributes and recursive membership
retrieval. Its configured reader needs access to both user and group membership
attributes; a directory can hide attributes without returning an access error.
The owning operator must qualify that read policy. A successful search cannot
prove that an administrator has granted complete visibility.

[RFC 2696 section 3](https://www.rfc-editor.org/rfc/rfc2696.html#section-3)
does not promise a snapshot when entries change during paging: entries can be
omitted or repeated. Describe the result as a bounded, complete observation
under the approved reader's visibility, not a global point-in-time directory
snapshot. The next external experiment will collect all group pages, resolve
every member by a base-object read on the same verified reader connection,
and require a stable ID and an unambiguous approved user/group class. Refuse
aliases, referrals, out-of-base references, unresolved objects and groups absent
from the collected set. DNs locate entries; only stable IDs identify graph nodes.

For the OpenLDAP profile, require the entry's `entryCSN` in addition to entryUUID.
The [OpenLDAP replication documentation](https://www.openldap.org/doc/admin26/replication.html)
distinguishes entry revisions from the provider's committed replication state.
An individual entryCSN is not a database transaction or global snapshot token.
Re-read resolved objects and repeat the complete group scan before returning a
candidate; changed identities, revisions or group manifests refuse the whole
attempt. This catches observed churn without claiming to exclude all changes
between reads. Record the observation interval; a failed attempt never refreshes
the previous generation's authority expiry.

Build direct stable-ID edges and a bounded transitive user closure. Reject
cycles, ambiguous identities and exceeded limits for objects, direct edges,
derived memberships, depth and directory operations. Memoized subtrees must
retain their height so reuse through a deeper parent cannot bypass the depth
limit. All refusals return no candidate. Keep this graph separate from local
roles; IAM-110 still owns reviewed mapping and current-authority reconciliation.

The [Microsoft range-retrieval contract](https://learn.microsoft.com/en-us/previous-versions/windows/desktop/ldap/searching-using-range-retrieval)
allows an empty requested attribute alongside the actual returned numbered
range. A decoder must distinguish that placeholder from the terminal range,
require contiguous inclusive indexes and matching value counts, and stop only
on the actual `*` range. Prototype that decoder with synthetic LDAP entries;
never infer a fixed server range size. Repeated/overlapping ranges, missing
continuations, duplicate attributes, mixed nonempty ordinary/ranged values,
oversized indexes and aggregate budgets must fail without partial membership.
Those fixtures qualify parsing only, not an AD server or its consistency model.

This extension reuses the exact already inspected LDAP module graph, signed
OpenLDAP source and private build image; it adds no dependency, model or dataset.
The existing commercial/offline/redistribution and notice obligations below
apply unchanged. No product dependency or formal schema is admitted by this
experiment. Reject recursive DN-string identity, unbounded nested traversal,
partial graph publication, a local role grant derived from a display label and
an assertion that two scans establish a transactional directory snapshot.

Acceptance requires native race tests for the graph and range failure matrix,
then actual disconnected OpenLDAP tests for nested membership, group cycles,
unresolved/out-of-base references, rename/recreation, observed in-flight churn,
bounded failure and explicit successful retry. A completed empty group scan
must remain distinguishable from an interrupted scan. Atomic PostgreSQL
publication, single-run ownership, current tenant/connector revision, audit and
commit-uncertainty qualification remain a subsequent persistence slice.

## Candidate dependencies and licensing

The candidate is [go-ldap v3.4.15](https://github.com/go-ldap/ldap/releases/tag/v3.4.15),
whose [module file](https://github.com/go-ldap/ldap/blob/v3.4.15/v3/go.mod)
requires Go 1.26. The selected module graph was resolved outside the product;
no product dependency, model or dataset is added. Exact module source licenses
were inspected in the checksum-verified downloaded versions below. Re-resolve
the complete product graph and retain its notices when actually adding it.
Windows SSPI and Kerberos modules in the graph do not enable those protocols.

| Module | Selected version | Source license |
| --- | --- | --- |
| go-ldap/ldap/v3 | v3.4.15 | [MIT](https://github.com/go-ldap/ldap/blob/v3.4.15/LICENSE) |
| Azure/go-ntlmssp | v0.1.1 | [MIT](https://github.com/Azure/go-ntlmssp/blob/v0.1.1/LICENSE) |
| alexbrainman/sspi | 7d374ff0d59e | [BSD-3-Clause](https://github.com/alexbrainman/sspi/blob/7d374ff0d59e/LICENSE) |
| go-asn1-ber/asn1-ber | v1.5.8 | [MIT](https://github.com/go-asn1-ber/asn1-ber/blob/v1.5.8/LICENSE) |
| google/uuid | v1.6.0 | [BSD-3-Clause](https://github.com/google/uuid/blob/v1.6.0/LICENSE) |
| hashicorp/go-uuid | v1.0.3 | [MPL-2.0](https://github.com/hashicorp/go-uuid/blob/v1.0.3/LICENSE) |
| jcmturner/aescts/v2 | v2.0.0 | [Apache-2.0](https://github.com/jcmturner/aescts/blob/v2.0.0/LICENSE) |
| jcmturner/dnsutils/v2 | v2.0.0 | [Apache-2.0](https://github.com/jcmturner/dnsutils/blob/v2.0.0/LICENSE) |
| jcmturner/gofork | v1.7.6 | [BSD-3-Clause](https://github.com/jcmturner/gofork/blob/v1.7.6/LICENSE) |
| jcmturner/goidentity/v6 | v6.0.1 | [Apache-2.0](https://github.com/jcmturner/goidentity/blob/v6.0.1/LICENSE) |
| jcmturner/gokrb5/v8 | v8.4.4 | [Apache-2.0](https://github.com/jcmturner/gokrb5/blob/v8.4.4/LICENSE) |
| jcmturner/rpc/v2 | v2.0.3 | [Apache-2.0](https://github.com/jcmturner/rpc/blob/v2.0.3/LICENSE) |
| davecgh/go-spew | v1.1.1 | [ISC](https://github.com/davecgh/go-spew/blob/v1.1.1/LICENSE) |
| pmezard/go-difflib | v1.0.0 | [BSD-3-Clause](https://github.com/pmezard/go-difflib/blob/v1.0.0/LICENSE) |
| stretchr/testify | v1.8.1 | [MIT](https://github.com/stretchr/testify/blob/v1.8.1/LICENSE) |
| golang.org/x/crypto | v0.54.0 | [BSD-3-Clause](https://github.com/golang/crypto/blob/v0.54.0/LICENSE) |
| golang.org/x/net | v0.57.0 | [BSD-3-Clause](https://github.com/golang/net/blob/v0.57.0/LICENSE) |
| golang.org/x/sys | v0.47.0 | [BSD-3-Clause](https://github.com/golang/sys/blob/v0.47.0/LICENSE) |
| golang.org/x/term | v0.45.0 | [BSD-3-Clause](https://github.com/golang/term/blob/v0.45.0/LICENSE) |
| golang.org/x/text | v0.40.0 | [BSD-3-Clause](https://github.com/golang/text/blob/v0.40.0/LICENSE) |
| gopkg.in/yaml.v3 | v3.0.1 | [MIT and Apache-2.0 by file](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE); retain NOTICE |

These licenses permit commercial and offline use and redistribution with their
conditions. Preserve copyright, license and applicable notices. MPL-covered
source and modifications retain their file-level source obligations; do not
relicense them as ownsolid files. The actual LDAP package import closure resolves
21 modules and 27 license/notice files; a root-only module query omitted the
three `golang.org/x` rows added above. Source inspection is not a build or product
dependency admission.

## Private protocol fixture

Use [OpenLDAP LTS 2.6.16](https://www.openldap.org/software/download/) rather than
an older distribution package or a community image with additional entrypoint
behavior. The official source archive has SHA-256
`8d8452bc3f56aa7a392c62dd377733f83d1314eed6c52fe44028439a3bb4df18`.
Offline `gpgv` verified its detached signature against the separately downloaded
[official release key](https://www.openldap.org/software/download/OpenLDAP/gpg-pubkey.txt),
fingerprint `3CE269B5398BC8B785645E987F67D5FD1CE1CBCE`, before source extraction.
Retain the signed archive and verification output with the external fixture.

OpenLDAP and its bundled LMDB use the
[OpenLDAP Public License 2.8](https://www.openldap.org/software/release/license.html),
which permits commercial/offline use and redistribution with preserved copyright,
notices and the verbatim license. Other embedded source notices remain applicable;
retain the full signed source, its root COPYRIGHT and component notices. This is
a private test server, not an admitted product component or distribution image.

The cached arm64 build base is
`golang@sha256:cfa27959837144bf4649802ba38e59daffd0dd2a7994aa83daaf113285b0f551`
(Debian 13). Its 203 installed package/common-license and Go license files are
retained by path and hash outside Git. This includes GCC GPL-3.0 with its runtime
exception, GNU make GPL-3.0, glibc LGPL-2.1 and Go BSD-3-Clause notices. These
unmodified private build tools are not ownsolid dependencies. The base has
OpenSSL `3.5.7-1~deb13u3` runtime libraries but no development headers. Before
building, obtain only the matching development package through Debian's signed
package indexes, retain the exact package hash and copyright, and refuse an
unplanned runtime upgrade. OpenSSL 3 uses
[Apache-2.0](https://openssl-library.org/source/license/); preserve its bundled
third-party notices as well.

Follow the upstream [build instructions](https://www.openldap.org/doc/admin26/install.html)
and [TLS configuration](https://www.openldap.org/doc/admin26/tls.html): use the
bundled MDB backend and native OpenSSL LDAPS with synthetic entries and a private
CA. Disable unused backends, overlays and SASL rather than claiming their support.
Acquire packages before provisioning; build and run without network access,
host-published ports or access to real directory data. Resource limits belong to
the external fixture configuration. The workstation's permanent nonblocking build
lock covers compilation and protocol tests. The actual protocol qualification
below is separate from source/signature inspection and product admission.

## Parser and operation boundaries

The selected [BER decoder](https://github.com/go-asn1-ber/asn1-ber/blob/v1.5.8/ber.go)
has package-global limits: approximately 2 GiB per packet and nesting depth
1,000. Those defaults are too broad for a login reader. Prototype a per-connection
LDAP frame guard that rejects indefinite lengths, malformed lengths, oversized
frames and excessive nested BER before releasing a frame to the library. A
deadline or post-decode entry count does not bound parser memory or depth. Do
not mutate global library limits concurrently or add a plaintext parsing proxy.

The [connection owner](https://github.com/go-ldap/ldap/blob/v3.4.15/v3/conn.go)
provides per-connection timeouts; the bind API is not a request-context API.
Use the caller's dial/handshake deadline, an owned connection and cancellation
that closes that connection and joins its workers. Prove that cancellation
terminates an actual blocked bind/search without leaking a goroutine. Never
share a connection between user authentication and synchronization or modify
the library's package-global default timeout at runtime.

## Protocol prototype qualification

The standalone adapter passed six native Go test roots with race detection.
The frame guard applies before the SDK BER decoder, including its separately
decoded paging-control value. It rejects indefinite/nonminimal lengths,
oversized declarations, excessive depth/nodes, unsupported response operations
and unrequested control types. A near-2-GiB declaration was refused before a
payload allocation; that test observed less than 128 KiB additional allocation.
The fixture policy is 64 KiB per frame, 1 MiB per connection, depth 16 and
1,024 nodes per frame. This is not a full-bound peak-RSS qualification.

Verified-TLS hostile peers exercised wrong CA/name, malformed packets and a
changed stable identity between lookup and user bind. The user connection
re-reads its own entryUUID after a successful bind and requires the original
identifier. Twelve real blocked bind/search attempts were canceled; each owned
operation returned, the peer observed the socket close and the SDK workers
drained within the bounded check. No global SDK timeout/parser limit was changed.

Four actual OpenLDAP 2.6.16 roots then passed in a disposable Linux arm64
container with `--network none` and no published ports:

- A read-only search bind and a separate user bind authenticated the synthetic
  account; wrong/empty passwords, an escaped filter-injection value, duplicate
  login names and missing users produced no identity.
- The real directory refused the search identity's attempted write with LDAP
  insufficient-access-rights. Fixture mutations used a separate verified TLS
  management connection.
- Renaming an entry preserved entryUUID; deleting/recreating the same name
  yielded a different identifier. No local binding or session was created.
- Five groups were read across two-entry pages. Page-count, entry-count and
  byte-budget failures returned no partial scan result; an explicit fresh scan
  succeeded. The adapter does not publish group roles or resolve references yet.

The exact development package is `libssl-dev_3.5.7-1~deb13u3_arm64.deb`, SHA-256
`ee3ce1214925d3905a8ad54ea1250b016438322f73a9719474fcd0ece6243a8e`.
Debian's signed indexes, package metadata and source hashes were retained before
the disconnected build. Its installed copyright matches the already retained
OpenSSL package notice; the development and runtime versions match exactly.

The durable external evidence set is `iam103-protocol-20261113.oogmig8q`, run
`qualification-5c3lyuq_`. It preserves exact prototype source/module sums,
signature verification, package/image identities, configuration policy, raw
race/live logs and cleanup. The source-manifest SHA-256 is
`ea709f639d725f506d07a613cddbf9c39910b3ecf4915ac819ef8bb852adda17`.
Four live-only roots skipped in the native invocation ran explicitly in the
private provider. The initial missing-`soelim` documentation build and the
management-fixture response-profile failure remain in separate failed runs.
The corrected build selects the upstream include/library/client/server targets;
it does not claim an upstream documentation build or upstream test-suite run.
All owned containers were removed and no directory server remains running.

This qualifies the initial LDAPS protocol approach. Product dependencies,
configuration, formal SQL, routes, local-account binding, sessions, synchronization
jobs and UI are unchanged. AD ranged/nested membership, stable-reference
reconciliation, complete-generation persistence and full-bound memory checks
remain required before composition. This is not Windows AD, rendered-browser
or Linux amd64 installation acceptance. IAM-103 remains open. Publication changes
research only; layout, roadmap and whitespace checks do not replace the future
feature's full repository and installed-flow gates.

## Rejected alternatives and acceptance

Reject plaintext simple bind, certificate bypass, empty-password authentication,
automatic endpoint/referral discovery, automatic DN/email rebinding and copying
raw directory diagnostics into Problems or audit events. Reject unbounded
all-results buffering, timestamp-only deletion detection and incremental-only
synchronization before a complete reconciliation path exists. AD DirSync and
LDAP Content Sync can become later optimizations; they do not remove the need
for a safe initial/full scan and explicit cookie reset handling.

Before product integration, qualify the guarded reader against actual private
OpenLDAP and hostile BER fixtures outside the product code. Test valid TLS and
wrong CA/name, disallowed destinations, empty/wrong passwords, ambiguous users,
escaped filter injection, renamed/recreated identities, blocked I/O cancellation,
oversized/deep/malformed packets and partial results. Validate multi-page and
ranged group fixtures, duplicate/repeated cookies, nested cycles, unresolved
members, interruption and a successful explicit retry. Measure peak memory at
the configured bounds; a parser helper test alone does not establish that.

The finished item additionally requires real PostgreSQL persistence, current
actor and foreign-tenant connector/binding/job refusals on every exposed route,
cross-audience and revoked-session tests, audit-append rollback and real commit
uncertainty behavior. A disconnected rendered UI must configure, test and bind
the connector, sign in, display the last complete synchronization and its retry
outcome, and enforce disable/revocation. Formal schema/configuration, public
contracts and Web remain with their shared owners. IAM-103 stays open until
that complete path passes the repository gates.
