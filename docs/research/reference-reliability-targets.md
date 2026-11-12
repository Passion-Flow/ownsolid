# Reference reliability targets (REL-102)

## Scope and sources

The target belongs to a named, versioned topology and workload. The current
installer qualification covers a single Linux amd64 Compose host; it does not
establish the multi-replica failure domains in REL-101. Do not derive a highly
available product target from independent replica probabilities or another
vendor's hosted SLA.

The [Google SRE workbook](https://sre.google/workbook/implementing-slos/)
starts with user actions, an explicit measurement window and observations
before selecting a numerical target. Apply that method to the two authenticated
entrances and the operations the reference deployment actually serves. The
existing [service error-budget research](log-alert-rules.md#slo-burn-rate)
requires authoritative eligible and failed request counters from each service
owner. Journal snapshots and a process health response cannot supply them.

[GitLab's restore prerequisites](https://docs.gitlab.com/administration/backup_restore/restore_gitlab/)
include compatible software, configuration and separately held secrets. The
ownsolid recovery clock must therefore cover those inputs and the restored user
path, rather than only the database import. PostgreSQL's
[continuous archiving contract](https://www.postgresql.org/docs/18/continuous-archiving.html)
requires a continuous WAL sequence; logical dumps cannot be replayed as a WAL
base backup. A configured archive timeout alone does not prove durable delivery.

This research adds no runtime dependency, model or dataset and copies no
third-party implementation. Existing Go tooling is
[BSD-3-Clause](https://go.dev/LICENSE), PostgreSQL uses the
[PostgreSQL License](https://www.postgresql.org/about/licence/), and Docker
Compose is [Apache-2.0](https://github.com/docker/compose/blob/v2.40.3/LICENSE).
These permit commercial use, offline operation and redistribution with their
required notices. A release still needs its complete image and asset licence
inventory under DEL-110; these tool licences do not license entire images.

## Measurement contract

Publish the following together before attaching any numerical product target:

- Exact release, schema, reference host/storage/network profile, topology,
  workload and bounded data volume. Separate simulated nodes, physical nodes
  and model serving hardware. Record backup destination and key custody.
- An eligible user action, its server-authoritative outcome and deadline, and
  the same scope for the numerator and denominator. Document retries, policy
  denials, invalid requests, overload and no-traffic behavior. A failing request
  must not disappear from the denominator because its dependency failed.
- The selected measurement window and minimum observation coverage. Use a
  rolling four-week window for initial evaluation, with raw counts and missing
  intervals visible. A short drill reports that drill's coverage; it does not
  substantiate four weeks of availability. Zero or missing observations are
  unknown, not perfect availability. Synthetic checks and real request results
  are reported separately.
- Failure domain and recovery scenario. Single-process restart, host loss,
  storage loss and unavailable external objects are different experiments.
  State any maintenance policy explicitly and retain its observed duration.

Availability is the fraction of eligible actions that finish with the declared
successful result within their deadline. The evidence must include login and
an authenticated persisted read/write journey for both entrances. A readiness
endpoint alone cannot demonstrate that restored keys, sessions or database
permissions work. Instrumentation and the UI remain with their existing owners;
this lane does not introduce another monitoring store or polling service.

RPO is the age of the latest recoverable consistency point at the fault, with
its store coverage stated. Validate the retained archive, its version binding,
digests and separately held key before counting it. The existing scheduled
history's `backup_verified` records a publication event, not current archive
availability. Its `started_at` precedes the dump and may serve only as a
conservative time bound when clock uncertainty is stated. It is not the exact
SQL snapshot or a cross-store checkpoint. A retention failure may coexist with
a verified publication. WAL recovery additionally needs an unbroken retained
chain and a demonstrated recovery endpoint; missing WAL cannot count as a
recoverable point. REL-103 and REL-109 retain their own completion criteria.

RTO starts at the injected loss of the declared user action and ends when that
action works against the verified recovered state. Record detection, recovery
invocation and restore completion separately so operator delay remains visible.
Include local release verification, image loading if needed, secret availability,
logical restore, model/file validation, migrations, database adoption, startup,
login and an authenticated persisted operation. Use a monotonic clock for elapsed
times and UTC plus uncertainty for cross-process recovery-point comparison.
Never restore over customer data for a measurement; use an owned empty site.

## Failures and completion

Prototype measurements with the existing isolated Linux acceptance harness,
real PostgreSQL and database-aware exports. Preserve packet captures with the
recorder controls and prove that installation and recovery have no unintended
egress. Cover a missing/corrupt archive, missing key, incompatible release,
expired archive, missing WAL, interrupted restore and successful retry. Verify
both restored file bytes and SQL state, server permissions and tenant isolation.
An unavailable prerequisite is a failed or incomplete measurement, not a zero
duration. Include cancellation and cleanup in the existing workload lease.

Select initial numerical targets only after these measurements and the service
owners' SLI contracts exist. Publish a reader-facing reference operations page
with the targets, assumptions, measured range and reproduction commands; retain
the exact run scope in product status. REL-102 stays open until that page and
the offline end-to-end evidence exist. Reject invented uptime percentages,
backup intervals presented as guaranteed RPO, `pg_restore` duration presented
as full RTO, and promoting the single-host measurements to an HA guarantee.
