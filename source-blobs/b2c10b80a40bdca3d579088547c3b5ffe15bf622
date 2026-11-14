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
facts result, training claim and training stop. The successful publication
measurements below use a separate targeted subtest. Node, component,
inference, model preparation and registry owners retain their ARC verification.

## Successful output publication

Measure checkpoint finish, adapter upload creation, adapter finish and
artifact-bound stop through the existing HTTPS and PostgreSQL cost recorder.
Use the checked-in Qwen3 config whose bytes match the approved catalogue,
the pinned PEFT metadata template and small synthetic F32 matrices in the
[Safetensors format](https://github.com/safetensors/safetensors#format).
Upload and hash every declared file through the normal training endpoint.
Exercise the existing parser, independent byte verification, publication,
audit, exact replay and restricted stop. The opaque optimizer, scheduler
and RNG fixtures contain test bytes. They have no role in proving resumed
computation or GPU training.

The [Qwen3 config](https://huggingface.co/Qwen/Qwen3-0.6B) comes from an
Apache-2.0 model, which allows commercial use, redistribution with the
licence terms and offline use. The Safetensors reference is Apache-2.0.
Use the existing Go fixture encoder and licences. No third-party runtime,
weight file or additional dataset is needed.

Both fleets reproduced four human authority reads on initial finish and
replay, including a redundant cleanup transaction after confirmed acceptance.
Initial finish also selected the exact attempt principal separately for audit.
Project that principal from the existing attempt fence and retain three
authority passes for admission, input verification and acceptance. Skip the
extra cleanup after a confirmed commit of the exact ticket clear. A failed
or uncertain commit still invokes exact-ticket cleanup. Artifact upload begin
and artifact-bound stop keep their current authority and file verification
passes. A stale scope, revoked actor or stale lease cannot replay success.
The existing protocol budgets remain the comparison point.

The disconnected Linux amd64 fixture used real PostgreSQL 18.6, pgvector
0.8.7, CPython 3.12.15 and the two seeded fleet sizes. Rows were equal
between sizes for each exchange. Blocks are the observed shared reads and
hits and can vary with cache state.

| Exchange | SQL before 50 / 3,000 | SQL after 50 / 3,000 | Rows before / after on each fleet | Blocks before 50 / 3,000 | Blocks after 50 / 3,000 |
| --- | --- | --- | --- | --- | --- |
| Checkpoint finish | 120 / 120 | 101 / 101 | 152 / 124 | 815 / 803 | 727 / 727 |
| Checkpoint finish replay | 86 / 86 | 68 / 68 | 142 / 115 | 331 / 364 | 266 / 291 |
| Artifact upload begin | 91 / 91 | 91 / 91 | 94 / 94 | 297 / 298 | 297 / 298 |
| Artifact finish | 124 / 124 | 105 / 105 | 145 / 117 | 528 / 553 | 462 / 479 |
| Artifact finish replay | 88 / 88 | 70 / 70 | 134 / 107 | 339 / 372 | 274 / 299 |
| Artifact-bound stop | 70 / 70 | 70 / 70 | 60 / 60 | 464 / 480 | 464 / 484 |

The replay cases fit the existing 76-statement upload budget. Both first
acceptances and artifact upload begin remain above that budget. Artifact
stop remains above its 28-statement budget. The publication authority
subtest verifies the scoped node credential, human delegation, exact lease
and attempt reads, and the absence of redundant successful cleanup. The
artifact stop keeps its two independently authorized transactions.

Run just the publication measurement with
`go -C services/control test -p 1 ./internal/modules/training -run '^TestAgentCallCostDoesNotGrowWithTheFleet$/^training_publication$' -count=1 -v`.
The same parent test also runs the earlier Agent paths when unfiltered.
The `repository_integration` test
`TestActualTrainingPublicationCommitAndRevocationFences` uses an exclusive
plaintext loopback PostgreSQL test endpoint to exercise a real lost COMMIT
acknowledgement, explicit replay, failed audit rollback and revocation
after file verification. The competing finish is refused while the exact
ticket is held. An unacknowledged commit exposes no reference, and
reconciliation returns the same reference without another audit.


## Current source review and next local change

The frozen 17e46bdd candidate has no further change to events.go, transfer.go,
delegated_repository.go, upload_chunks.go, upload_finish.go, uploads.go or
stop.go relative to the already measured implementation. Its local-cell job
predicate adds a scope check, not a measured budget repair. The counts above
remain the last measured results; neither compilation nor an unbound test
skip establishes new 50/3,000-fleet costs.

[MLflow's log-batch contract](https://mlflow.org/docs/latest/api_reference/rest-api.html#log-batch)
provides a bounded batching comparison, but permits partial persistence on an
error. ownsolid retains its existing atomic batch contract: every new event,
its audit, job phase and attempt cursor commit together, or none does.
[PostgreSQL row locks](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
serialize the current job/attempt, and a transaction can see its earlier
writes under [Read Committed](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED).
These support one transaction-local event cursor without sharing authority
or progress between requests.

The next local change is to read the exact attempt's event cursor once,
validate each event in the original request order, preserve each immutable
event and individual audit, and persist the final cursor once. Preserve the
preparing-to-running job transition and each returned intermediate JobState.
The current loop rereads the cursor per event and writes the attempt again
for the phase transition. The numbered attempt immutability guard protects
inputs/outcome/stop identity, not a requirement to write progress per event.
Before implementation, also inspect partition and scope guards in the full
registered lineage. This is a plan, not a measured improvement.

Required checks include mixed duplicate/new batches, changed replay bytes,
sequence gaps, step regression, resumed final-checkpoint phases, concurrent
identical replay, a late cursor-write failure, audit failure, and a lost
COMMIT acknowledgement followed by explicit replay. Read the actual event
rows, final cursor, job revision and exact audit attribution independently.
A failed/unknown write returns no accepted items. Replays append no audit.
Keep current human epoch/membership, node credential, task/lease, input and
GPU fences before the callback. No file I/O or cross-transaction authority
cache enters this event-only optimization.

For chunk accounting, any query consolidation must preserve an authoritative
post-lock byte sum. Do not derive it from a correlated aggregate whose
Read Committed statement snapshot may precede a lock wait. Candidate follow-up
changes can project ticket state with the locked upload and return the fresh
sum from the final ticket-clear statement. Existing failure/unknown-commit
cleanup remains necessary; acceptance is acknowledged only after commit.

No dependency, model or dataset is added. Existing Go/pgx/PostgreSQL licensing
and offline terms above remain applicable. MLflow is a documentation
comparison only; its code is neither installed nor redistributed.

## Node-owner shared fence prerequisite

One successful withMachineLeaseActor pass currently follows these calls:

1. loadDelegatedWithin -> Nodes.TrainingTargetWithin ->
   PreparationHistoryWithin;
2. Nodes.CredentialWithin -> PreparationHistoryWithin, then the scoped
   credential-hash read;
3. Nodes.LeasedTrainingWithin -> trainingTaskWithin ->
   TrainingHistoryWithin -> PreparationHistoryWithin, then exact task,
   payload and lease checks.

Each PostgreSQL history read repeats the installation/tenant/node fence.
RecordStop similarly uses a fresh delegated transaction on each side of
artifact I/O and repeats scoped credential and stop-task reads. These
source-level counts are call sites, not a new SQL-cost measurement.

The minimum proposed owner contract is a borrowed callback that authenticates
and locks the scoped node once, then supplies a transaction-bound task reader:

    WithTrainingNodeWithin(ctx, q, tenant, workspace, node, credential,
        func(current Node, tasks TrainingTaskReader) error) error

The reader would expose active leased-task and restricted stop-task operations.
Only nodes may construct it. It must reject pools, unsupported/mismatched
executors, use after the callback/transaction and a foreign node/task scope.
Authenticate the credential before exposing profile/status details. Retain
installation/tenant/node lock ordering, exact command decoding, catalog,
deadline, task attempt and lease-token checks. The training callback still
locks its job and performs exact attempt/GPU/stop-token and current human
checks; restricted stop must retain the ability to free an authenticated
stopped owner after human revocation. Each transaction around file I/O obtains
a new fence.

This is a proposed signature for ARC, not an existing shared API. Training
will not copy nodes SQL, forge an opaque fence or cache a Node DTO as
authorization. The prerequisite is released only by the node owner's typed
implementation and negative/borrowed-executor tests, followed by affected
training revocation/stream/rollback/replay tests and the original real
50/3,000 HTTPS cost profile against unchanged declarations. Target current
node primitives first if the owner can supply an equivalent smaller API.
