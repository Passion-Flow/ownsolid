# Agent operation cost coverage

Research for ARC-108. ADR 0020 declares a ceiling per v1 call, while each
workload adds distinct exchanges below that call. Counting a route or measuring
one direct service call does not cover its authenticated HTTP path or prove
that its work stays independent of fleet history.

## Primary sources and existing evidence

[PostgreSQL auto_explain](https://www.postgresql.org/docs/18/auto-explain.html)
can record executed JSON plans, nested statements and buffer/row observations.
Record all plans, turn timing off, and suppress bind parameter values. A real
superuser fixture is required; this setting does not belong on a production
serving role. Keep all setup, seeding and independent readback outside the
recorded exchange. Existing agentcost tests already prove the parser and
small/large comparison against real PostgreSQL.

[PostgreSQL planner settings](https://www.postgresql.org/docs/18/runtime-config-query.html)
explain that disabling sequential scans discourages them without eliminating
them. Retain the existing missing-index check, seed relevant owner tables and
refresh statistics. Report buffer counts without making cold catalog cache
state a flaky assertion. Keep the exact statement ceiling, equal statement
counts and existing scanned-row growth bound.

[Kubernetes ClusterLoader2](https://github.com/kubernetes/perf-tests/tree/master/clusterloader2)
uses explicit workload definitions and separately selected measurements in its
scalability framework. Apply that separation here: label the actual protocol
exchange and measure its database work; use the simulator for concurrent fleet
capacity and recovery. Seeded peer rows are not simulated connected nodes and
supply no 10,000-node capacity claim. ClusterLoader2 is a reference, not a new
dependency.

The current component heartbeat test uses HTTPS, mTLS, 50/3,000 seeded peers,
shared-row contention, stored-state readback and stale credential coverage.
The inference heartbeat test currently uses a direct service call and one node.
Training's separate measurements retain known budget overruns in
[training Agent cost](training-agent-cost.md). Preparation, registry and other
component/inference operations remain required coverage; completing heartbeat
coverage alone cannot tick ARC-108. The cost target must run the node
observation roots as well as the unchanged training root, serially against
its real database. An isolated manual measurement is not continuous coverage.

## Approach and alternatives

Reuse one node observation fixture for real enrollment, client certificates,
scoped HTTPS, peer task/audit history and shared identity-row contention.
Parameterize only the execution profile and owner observation rows. Keep
component-specific stored-state and credential replacement assertions. Measure
first and updated inference reports and invalid credentials over the same
public machine boundary. Read the committed heartbeat timestamp and exact
owner observation independently after recording; denied requests must preserve
both. Include relevant observation rows for every seeded peer.

The shared recorder must suppress parameter values on every configured
connection before any enrollment or authentication material is used. Read the
actual PostgreSQL setting in the fixture. Do not rely on each caller remembering
an optional setting. This changes test instrumentation, not serving logging.

Reject copied enrollment/lock fixtures, mocked SQL plan counts and HTTP handler
success stubs. A direct service measurement remains useful for diagnosis but
cannot replace the public-path measurement. Do not increase v1 budgets to make
a red case pass: identify the added work, fix owned code with authority fences
intact, or retain a precise unresolved owner boundary. No wire message, agent
version, cursor or lease changes; old agents and mixed-version fleets retain
the existing v1 contract.

## Licenses and offline execution

No dependency, model or external dataset is added. Use the pinned repository
versions and cached binaries. Existing [Go](https://go.dev/LICENSE) is BSD-3-Clause,
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) is MIT and
[PostgreSQL](https://www.postgresql.org/about/licence/) uses its permissive
PostgreSQL license. These permit commercial use, redistribution with their
required notices and offline use. Existing synthetic UUIDs use
[google/uuid](https://github.com/google/uuid/blob/v1.6.0/LICENSE), BSD-3-Clause;
HTTP schema checks reuse
[jsonschema](https://github.com/santhosh-tekuri/jsonschema/blob/v6.0.3/LICENSE),
Apache-2.0. The existing machine handler uses
[gRPC-Go](https://github.com/grpc/grpc-go/blob/v1.85.0/LICENSE), Apache-2.0.
Those terms also permit commercial redistribution with notices and offline use.
The pinned pgx v5.11.0 and schema-library license texts were checked in the
admitted module cache. No Kubernetes code is copied.
All identities, reports and peer rows are synthetic fixture data. Runtime
instrumentation must not fetch an extension, dependency or dataset.

## Failure coverage and completion

A missing/empty recording fails. Missing indexes, more SQL on the large fixture,
exceeded declared cost, excessive scanned-row growth or waiting on shared
identity rows fail unchanged. Wrong credentials must return the existing
unauthorized error without altering either node or workload observation.
Independent readback must match the successful committed response. Fixture
transactions, TLS servers and client resources must be joined and disposed.

Run both observation roots against real PostgreSQL 18.6/pgvector 0.8.7 without
external networking, record statements/rows/blocks and exact source, then run
affected race tests and the required repository gates. The entire item remains
open until every named operation has an actual exchange and all declared costs
pass. Installed lifecycle and simulator load results stay separate.
