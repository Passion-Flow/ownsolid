# Lane H decisions

## DEL-101 production bundle boundary

Keep DEL-101 open. The inherited shared profile requires the old milestone
inventory, and the release has no Helm chart or release-qualified plug-in to
package. Reuse the existing factory for the independently scoped current
single-node path below.
See [offline delivery research](offline-bundle.md).

Rejected labeling a fixture profile as a release, changing the shared release
contract from this lane, and creating a parallel signing format. Any of those
could create an installable-looking file that violates the existing verifier or
omits a required capability. If the inherited profile can be populated with the
actual current inputs, it can be evaluated without waiting for a replacement.

## DEL-101 additive current release path

Keep the inherited manifest envelope and cryptographic domain. The current
single-node factory path selects a new inventory policy explicitly. This avoids
changing the API module's legacy policy or letting legacy consumers accept new
inventories by accident. A separate archive and signing implementation was
rejected because it would duplicate trust and verification code. A bug in the
new inventory policy could admit an incomplete product, so the profile has a
fixed required set, explicit asset kinds, and checksummed SPDX coverage. Full
DEL-101 remains open until the real chart, plug-in and model inputs are qualified.

## DEL-102 preflight policy

Keep existing configuration files valid. A typed policy enables the full host
checks on apply, and a read-only command lets an operator check before changing
anything. Local host inspection and a private resolver selected by the operator
fit isolated sites. Reject implicit system DNS, online time queries, and remote
Docker for host preflight. Requiring these new policy inputs on every existing
apply would block upgrades or labs that resolve browser origins on clients.
The cost of opt-in is that an installation using an old configuration continues
with its original Docker and Compose checks until an operator enables the policy.

## P1 dependencies after preflight

DEL-103 requires the setup endpoint and its authentication lifecycle. DEL-104
through DEL-106 require an admitted current release and a persisted baseline.
DEL-107 owns chart sources outside this lane, and DEL-108 depends on the bundle
to provide the OCI inputs. DEL-109 explicitly requires an issuer format freeze
and an issuer-signed test artifact. DEL-110 needs the actual release inventory
to establish which notices it must carry. Leave these open and move to DEL-111,
which can collect locally from the already supported single-node installation.

## DEL-111 data collection boundary

Collect a fixed set of host, Docker and application facts on the installation
host. Use a typed allowlist at collection and archive time. Reject collecting
arbitrary log text and configuration followed by regex filtering, because inputs
can carry secrets in unexpected fields or encodings. The tradeoff is that a
bundle gives state and failure location without customer logs. An unavailable
collector records its own fixed status without obscuring the other results.

## Remaining P1 dependencies

DEL-112 needs DEL-101's current installable release format and verification entry
point before rotating its trust root. DEL-113 requires console and API changes.
DEL-114 needs the address matrix to run end to end, including the Compose
healthcheck and listener configuration outside this lane. DEL-115 depends on
that matrix and on session and SSO callback ownership. Keep these lines open.

## Roadmap evidence boundary

Do not tick a line while the repository-wide verification gate fails. The
isolated installation can substantiate this lane's Linux amd64 results while
browser and agent failures remain open in their owning lanes. The coordinator
also owns roadmap index and product status updates. Ticking here in isolation
would make the repository's progress check stale.

## DEL-104 initial planning boundary

Use the installed signed release binding and the database's observed schema for
the plan. Keep PostgreSQL and telemetry images unchanged for an application
release transition. Decline an unbound install, a schema downgrade, or a target
outside its declared predecessor range. Treating a Docker tag or the target's
version string as the source baseline was rejected because either can misstate
the installed data. A wrong predecessor decision could make the database
unreadable, so apply must independently revalidate it before a backup or migration.

## DEL-106 incremental packaging

Reuse complete signed target metadata. Sign the predecessor manifest digest in
the target's prerequisites and omit only byte-identical assets from the patch.
Reconstruct with the retained independently verified baseline. Reject trying
to reconstruct from local Docker tags or a writable staging cache. If the
baseline or any payload is wrong, the assembled release must fail before the
existing upgrade procedure can stop a service.
