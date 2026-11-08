# ARC-108 and ARC-109 training Agent cost

The [agent cost rule](../decisions/0020-agent-call-cost-rule.md) uses
[PostgreSQL auto_explain](https://www.postgresql.org/docs/18/auto-explain.html)
to count executed plans, including nested statements, while an independent
transaction holds the shared fleet rows. The existing real HTTPS test exercises
50 and 3,000 seeded peer nodes with their task and audit history. The baseline
at f3b2490 ran 142 statements for a three-item training event batch, 72 for
checkpoint upload creation and 99 for a checkpoint chunk on both fleets.

The event batch separately selected the immutable attempt audit principal three
times. Upload creation and chunk acceptance selected it once in their write
transactions. The existing exact attempt and GPU fence can project that same
principal from the matched row. The row's PostgreSQL and MySQL immutability
triggers preserve requester and audience. Keep the actor's current authorization
and its revocation behavior separate from audit attribution.

A successful chunk ran a third authorization pass solely to clear its ticket.
The acceptance transaction already clears that exact ticket. Skip the redundant
cleanup only after confirmed acceptance commit. Keep the existing bounded
authorization and exact-ticket cleanup after failures or unknown outcomes.
The body still streams outside locks and final accounting still reauthorizes.
[Read Committed](https://www.postgresql.org/docs/18/transaction-iso.html)
allows later statements to observe intervening commits, so caching authorization
across the file operations would lose revocation checks. Reusing node fences
would require the node owner's shared API and separate verification.

Measure the same exchanges and require one authority pass for events and
two each for upload creation and successful chunk accounting. Count the human,
scoped node credential, exact lease and attempt checks in each pass. The separate
attempt actor SELECTs and the successful chunk cleanup should disappear. Check
unchanged protocol budgets, exact audit scope, concurrent replay, malformed
attempt and body denial, revocation while a stream is in flight, and failure
rollback. Successful publication, other workload owners and fleet-wide ARC
acceptance are separate coverage. Existing Go, pgx and PostgreSQL components
and their commercial, redistribution and offline terms are recorded in
[the scheduling training dependency research](scheduling-training-reservations.md).
This change adds no dependency, model or dataset.

## Measured results

On disconnected Linux amd64 with PostgreSQL 18.6 and pgvector 0.8.7, the
existing real HTTPS fixture reported these counts. Each pair is the small
and large fleet respectively. Blocks include cache hits and physical reads.

| Exchange | SQL statements before | SQL statements after | Rows before | Rows after | Blocks before | Blocks after |
| --- | --- | --- | --- | --- | --- | --- |
| Three training events | 142 / 142 | 139 / 139 | 80 / 80 | 77 / 77 | 512 / 492 | 506 / 486 |
| Begin checkpoint upload | 72 / 72 | 71 / 71 | 66 / 66 | 65 / 65 | 246 / 250 | 241 / 247 |
| Accept checkpoint chunk | 99 / 99 | 80 / 80 | 101 / 101 | 73 / 73 | 333 / 348 | 266 / 273 |

Run the targeted assertions with the existing PostgreSQL superuser test DSN
and pinned CPython configuration using
`go -C services/control test -p 1 ./internal/modules/training -run '^TestAgentCallCostDoesNotGrowWithTheFleet$/^training_authority$' -count=1 -v`.
Run the parent test without a subtest filter to check every declared budget.
Events still exceed 75 statements and the chunk still exceeds 76. The
full test also reports the same baseline overruns for certificate, enrollment,
facts result, training claim and training stop. Successful publication and
artifact-specific paths still need measurements. Node, component, inference,
model preparation and registry owners retain their ARC verification.
