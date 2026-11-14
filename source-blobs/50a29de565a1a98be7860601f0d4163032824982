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
| gopkg.in/yaml.v3 | v3.0.1 | [MIT and Apache-2.0 by file](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE); retain NOTICE |

These licenses permit commercial and offline use and redistribution with their
conditions. Preserve copyright, license and applicable notices. MPL-covered
source and modifications retain their file-level source obligations; do not
relicense them as ownsolid files. A private OpenLDAP fixture additionally needs
its exact package/image version, OpenLDAP Public License and all bundled OS
dependency notices inspected before provisioning. No test directory is selected
or downloaded by this note.

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
