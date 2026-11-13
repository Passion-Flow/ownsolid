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

## Qualified observation coverage

The complete source a91a817b23157fd610f714533b04cf6e7d8ec012,
with tree 5ddb7c0683319219ff5f482630ca6ee3fd938b1c, passed the two
observation roots on PostgreSQL 18.6/pgvector 0.8.7. The Linux arm64
Docker fixture ran on Darwin arm64 with network none, two CPUs, one GiB
of memory and no published port. Each history had exactly 50 or 3,000
peer nodes, their owner observations, four tasks and four audit records
per peer, plus the one enrolled node under measurement. No database case
was skipped. These are seeded database histories, not connected simulators.

| Exchange | SQL, 50 / 3,000 peers | Scanned rows | Shared buffers, 50 / 3,000 peers |
| --- | --- | --- | --- |
| First component report | 11 / 11 | 18 / 18 | 231 / 241 |
| Component update | 9 / 9 | 15 / 15 | 46 / 59 |
| Refused component credential | 2 / 2 | 1 / 1 | 3 / 5 |
| First inference report | 9 / 9 | 14 / 14 | 132 / 140 |
| Inference update | 7 / 7 | 11 / 11 | 31 / 37 |
| Refused inference credential | 2 / 2 | 1 / 1 | 3 / 5 |

Every exchange retained the existing 11-statement heartbeat ceiling,
statement equality, scanned-row growth and missing-index checks. The
independent shared-row holder was verified to hold each intended row.
Readback matched the committed timestamp and full inference observation;
credential refusal preserved both node and workload state. The component
credential-replacement and disabled passive-reporting cases also passed.
The actual PostgreSQL parameter setting was zero before enrollment.

The affected nodes, agentcost and httpfixture packages passed complete
Darwin race tests and vet. Full `make check`, `make build` and `make test`
passed on a private APFS temporary volume with offline resolution. Console
passed 1,075 cases and Portal 389, with 37 existing live-fixture skips and
no added retry. The retained log SHA-256 values are:

| Evidence | SHA-256 |
| --- | --- |
| Actual observation costs | 63c787f580bc77ca5c52bedb009f892ce1cc7422bcf08d498a4cb1a5e3580f20 |
| Affected race | 17d8c03ed2e69578508c172b14d233e978ade3a134cf2a969236a92268ce7370 |
| Full check | 0eafcf7ce9ed43e6aff67c661a3ebe86f21b9088ca67b3ddf37d0ce2dde5f5c0 |
| Full build | 97052419668903eeceb8a886450bf30874b89757b280ff193ff716b868366d60 |
| Full test | ab35aa38354b5a6fb3adb3767a14ddd7c2f9acd51d02c1ef382a8c401d043996 |

The first combined wrapper correctly failed its protected-input guard:
its new directory was 0755 instead of 0700. The corrected wrapper uses
private directories and retains the original guard. The failed log remains
separate; the passed race/vet and identical compiled binary were reused,
then the actual database replay passed on the corrected fixture. No product
source or cost assertion changed to repair that harness error.

The later rebase changes only Web inputs and documents. Non-Web executable
and test inputs remain byte-identical to a91a817b. The complete Web tree is
byte-identical to the owner's full-gate input
60b85e037aa24fc0d30ae0195e6dffe8f59a6946. Its complete check/build/test
passed, including 1,112 Console and 410 Portal cases, 38 existing fixture
skips and zero retries; see the
[owner's complete Web acceptance](gateway-model-description.md#complete-web-gate-and-final-screen-review).
Reuse those qualified component inputs and check the final integrated tree;
do not present the older browser count as a run of the newer Web tree.

ARC-108 remains open: other component/inference exchanges, preparation and
registry operations still need measured coverage. Training's recorded
budget overruns remain unchanged. Native amd64, installed operation and
concurrent simulator capacity acceptance are separate from these results.

The final integrated tree 0df420bf0a654a9cb92d9439097319970da98dda
passed `make check` and `make build` after that source comparison. Its
complete test evidence is the two matched inputs above; no new execution
of `make test` on the newer Web tree is asserted. Final check/build log
SHA-256 values are respectively
c27586b5b20596d712641bb24d5608d085612f641f4f136652a9a4738730b93c
and 3595d621654b9622c347b324cad66c757a8fc16076946248c6f523bf3f9c159d.

## Retained deployment coverage in the shared heartbeat root

The current shared inference root measures first/update hardware reports and
credential refusal, but not a heartbeat that reaches a retained deployment.
The separate Deployments-owner test and two-fleet diagnostic already established
the real public path for a current and a stale desired revision. Incorporate
those exchanges into the existing Nodes-owned root instead of creating another
enrollment/HTTPS recorder or editing the domain's test.

Create the target through the real deployment service after publishing synthetic
approved model metadata and a valid GPU observation. Keep its actual queued task
and held reservation. No synthetic successful task result or generated-text
verification is needed for a loading report. Seed an exact retained deployment
and held GPU lease for each existing peer, with unique synthetic GPU identities,
then analyze those tables outside recording. These are query-cost histories,
not connected machines or hardware acceptance.

Close the original TLS test listener and construct a fresh production dispatcher
against the same pool, retaining the issued node certificate and existing trust.
[httptest.Server.Close](https://pkg.go.dev/net/http/httptest#Server.Close) joins
outstanding requests before the next listener is used. Measure the first current
deployment report with fresh node/pacing state under the same independent shared-row
holder. Advance desired state through the real stop action, then measure the old
revision's delayed failed report. Independently compare the complete deployment
and GPU reservation rows before/after that stale report; it must not change them.
Both successful reports must retain their actual heartbeat timestamp, full
observation and node scope.

The existing [auto_explain](https://www.postgresql.org/docs/18/auto-explain.html)
recorder and exact 11-statement declaration remain unchanged. All five exchanges
must be present on both 50/3,000-peer histories, with equal statements, bounded
scanned-row growth and usable indexes. Keep the three original cases and their
stored-state/credential assertions. A cold/stale failure remains a failure;
neither a new budget nor an owner implementation copy is permitted. No new
dependency, wire field, protocol version, model or external dataset is added;
the existing licence/offline analysis above applies.

This source preparation requires its own allocated PostgreSQL replay and affected
race/full gates. The shared native window currently admits no new workload or
background queue. Its original package roots and strict assertions remain the
future entry; preparing this extension neither qualifies ARC-108 nor resolves
the other operation families and retained cost overruns.

The source preparation has been formatted and passed layout, roadmap consistency
and diff checks. No compiler, native SQL, service or simulated fleet has started
for this extension. The future focused entry uses the existing protected test
fixture and pinned offline cache, with no fixture skip accepted:

```sh
GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOMEMLIMIT=512MiB \
  go -C services/control test -mod=readonly -p 1 -count=1 -timeout=10m -v \
  ./internal/modules/nodes -run '^(TestComponentHeartbeatCostAtFleetSizes|TestInferenceHeartbeatStatementCost)$'
```

Use one explicitly allocated pinned PostgreSQL 18.6/pgvector 0.8.7 fixture with
network none, two CPUs and at most 2 GiB, and budget 15 minutes for serial build
and the focused replay. The workstation gate and both Linux heavy locks, their
existing memory/disk/PSI and foreign-job checks apply before starting. Full
check/build/test and affected race acceptance require their own allocation.
