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
DEL-101 remains open until the production chart, plug-in, font and model runtime
inputs are qualified.

The delivered archive includes the installer. For a site without a trusted
installed copy, authenticate the archive digest through the release channel
before extracting that binary. The signed release root is provisioned
independently. Executing an installer merely because it appears inside a tar
file would let a replacement archive select its own verification program.

## DEL-101 model runtime boundary

Keep the existing no-egress admission rule for runtime tests. The first CPU run
generated tokens and still attempted to look up its own container hostname
through the site resolver. Pin the standalone test hostname to loopback and
repeat the same packet assertions with the resolver present. Discarding DNS
packets or accepting generation alone would miss similar problems. The local
hostname applies to one CPU container. Agent-managed GPU and multi-node
launches require their own address and resolver checks.

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
to provide the OCI inputs and on the RKE2 registry path agreed in ADR 0051 and
ARC-116. A second Compose registry would leave the cell's worker path
unverified. DEL-109 explicitly requires an issuer format freeze and an
issuer-signed test artifact. The local issuer checkout at `1812e94` contains
foundation and working rules, with no implementation for that artifact.
DEL-110 needs the actual release inventory
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
DEL-114 needs ARC-110 listeners and IAM-114 shared-hostname opt-in from
ADR 0027, plus Compose health checks and trusted proxy enforcement. Current
certificate generation can encode an IP name. Enabling IP-only access or
HTTP through installer settings alone would bypass the missing listener or
cookie policy. DEL-115 depends on that matrix and on session and SSO callback
ownership. Keep these lines open.

## Roadmap evidence boundary

The isolated installation substantiates the signed Linux amd64 Compose path.
The production gate still requires the exact release inputs for DEL-101 and
DEL-110. Physical NVIDIA driver checks and agent-managed GPU serving need an
authorized GPU host. Keep this scope separate from the CPU model result.

The repository-wide suite also needs a passing run. A full run reached the Web
unit stage after Go, Rust and both browser suites passed, then failed on the
initial language attribute in the Web internationalization provider. The
complete Web unit stage passed when repeated independently. A standalone
pass cannot replace the full gate. The next full run passed both browser
suites and stopped in the agent training revocation test with
`TRAINING_OUTPUT_CHANGED`. The same test passed five focused repetitions
with a canonical temporary path. Both failures are outside the delivery
paths owned by this lane. The roadmap index, product status and
capability declaration are coordinated outside this lane. Ticking a line on
its own would leave the repository's progress checks out of sync.

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

## DEL-112 root policy

Use a versioned public root file with distinct thresholds for its current and
successor policy. The rotation signatures cover the exact successor bytes in
a root-specific signature domain. A root can delegate release signing without
giving the release signer authority to replace the root. A shared unversioned
key list cannot express expiry or detect skipped versions, so retain it only
as a compatibility input and use the versioned file for rotation. The scope is
local trust rotation for the signed delivery path.

## DEL-104 and DEL-105 recovery scope

Exercise a stopped writer and verified recovery point in the isolated Docker
test, with a controlled failure at the candidate's start command. The
same-schema rollback must restore the previous signed image while keeping
PostgreSQL and its schema in place. Reject a forced migration downgrade or
restoring data as a side effect of the application rollback. An advanced schema
still requires separate data recovery. The ordinary restore command runs the
active release's migrations. An in-place downgrade of both data and
application remains outside this qualification.
