# Acknowledged alert escalation candidate (OBS-104)

[PagerDuty escalation](https://support.pagerduty.com/main/docs/escalation-policies)
separates responder acknowledgement from resolution and halts subsequent
escalation for the acknowledged incident. The pinned [Alertmanager v2
API](https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.2/api/v2/openapi.yaml)
exposes alert occurrences and silence and inhibition state. An acknowledgement
is a separate business action. A label silence can also mute a later alert with
identical labels, so creating a silence would not implement per-occurrence
acknowledgement. The webhook GroupKey identifies a group, and its notification
transaction changes when membership or lifecycle changes. Neither is the
identity of one alert occurrence. [Alertmanager webhook
configuration](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config).

## Candidate boundary

Admit one Alertmanager source, one alert per candidate group, and timestamps
with millisecond precision, which the pinned v2 API can represent exactly.
Reuse the relay's protected tenant credentials, receiver and label checks.
Require trusted workspace labels on the group, common labels and alert.
Derive an occurrence identifier from tenant, workspace, fingerprint and UTC
start time. Use only server-resolved tenant, workspace and actor for the
acknowledgement write. The caller selects an opaque occurrence ID. The first
authorized actor and database time win. A replay returns the existing
receipt while the occurrence is active. Unknown, closed and foreign
occurrences share the same rejection.

Use a dedicated observation route and a delayed route from Alertmanager. Set
the gate deadline from the first observation using database time. Before
each claim, read the pinned Alertmanager API and require an exact active,
unsilenced and uninhibited occurrence. A missing or unavailable source
defers delivery. An explicit resolved webhook closes only that occurrence
and can create a tombstone before an out-of-order firing event. A silence
or a missing source record never writes an acknowledgement or a resolution.
The source lookup and outbound delivery are separate operations. A silence
created after a delivery is admitted cannot recall that delivery.

A PostgreSQL conditional update and an attempt insert in one statement
serialize claims and acknowledgement. Each claim records the occurrence,
attempt sequence, lease deadline and database time. A completion from an
expired and superseded attempt cannot overwrite a newer attempt. Use a
lease longer than the bounded delivery and cap attempts. Retry ambiguous
delivery with the same occurrence-derived idempotency key. Persist only
state and delivery outcomes. Raw webhook bodies, log data and metrics stay
outside PostgreSQL. If a worker crashes after the receiver persists the
message, a new claim can obtain the same durable receipt. An acknowledgement
or explicit resolution stops future claims, but cannot recall an already
claimed in-flight request.
[PostgreSQL row locks](https://www.postgresql.org/docs/18/explicit-locking.html)
and [atomic conflict handling](https://www.postgresql.org/docs/18/sql-insert.html).

## Qualification

Use the existing Go standard library under the [Go BSD license](https://go.dev/LICENSE),
[pgx v5.11.0 under MIT](https://raw.githubusercontent.com/jackc/pgx/v5.11.0/LICENSE),
[PostgreSQL under the PostgreSQL License](https://www.postgresql.org/about/licence/),
the existing test image's [pgvector 0.8.7 under the PostgreSQL License](https://raw.githubusercontent.com/pgvector/pgvector/v0.8.7/LICENSE),
and [Alertmanager 0.34.2 under Apache-2.0](https://github.com/prometheus/alertmanager/blob/v0.34.2/LICENSE).
These allow commercial and offline use and redistribution under their
respective notice and license terms. The database image is a preloaded
test fixture. No SDK, model, external dataset or runtime dependency is
introduced.

Qualify the exact start time and fingerprint against real Alertmanager
webhooks and v2 reads. Prove concurrent responders receive one immutable
receipt, unauthorized tenant/workspace scope cannot confirm it, a
persisted escalation with a lost receipt retries under the original key,
a resolved instance stays closed, and a recurrence can escalate
independently. Test silence separately and keep both services confined to
one disposable loopback-only namespace.

The candidate stays behind a build tag with fixture-only DDL. Product
admission requires a coordinated migration, bounded retention, Admin
session recheck and privileged-action audit in the owning transaction,
Console workflow, grouped escalation without storm amplification and a
qualified installer lifecycle. No product route can call the candidate.

## Verified loopback path

Alertmanager 0.34.2 and PostgreSQL 18.6 ran in one disposable Linux amd64
namespace with only loopback. Twelve concurrent responders received one
immutable actor and database timestamp for the exact occurrence. A principal
from another tenant or workspace could not confirm it. The acknowledged
firing occurrence made zero delivery attempts, and the real resolved webhook
closed it. A recurrence kept the fingerprint and received a new occurrence
ID. The receiver synchronized one message before losing the receipt, and
PostgreSQL recorded the failed receipt and the successful retry under the
same key. A real silence suppressed a replay of the captured webhook and
left the occurrence unacknowledged and unresolved. A PostgreSQL fault
fixture also proved that a live lease excludes another claim and an expired,
superseded attempt cannot complete the newer lease.

Alertmanager uses label-based notification deduplication. When the two routes
observe resolution at different times, a same-label recurrence may wait for
the notification repeat interval on the delayed route. A deployment must
qualify recurrence timing, missed resolution reconciliation and group
membership behavior before admitting this gate.

## Group escalation qualification boundary

The single-occurrence candidate must not be generalized by making one delivery
claim per member. Forty members in one incident must still share one escalation
step, with bounded retries under one durable idempotency receipt. The existing rack qualification proves
inhibition and one initial notification, not this grouped acknowledgement and
retry lifecycle.

[PagerDuty's incident acknowledgement semantics](https://docs.pagerduty.com/developer/send-alert-event#event-action-behavior)
keep an acknowledged incident quiet when further triggers join it; a trigger
after resolution creates a new incident. This is a behavior reference only.
No PagerDuty runtime, SDK, service account or cloud connection is introduced.
For the next local prototype, retain acknowledgement while membership changes
within the same open scoped incident. A genuinely new incident must not inherit
an earlier acknowledgement.

### What the native webhook establishes

The pinned [webhook writer](https://github.com/prometheus/alertmanager/blob/v0.34.2/notify/webhook/webhook.go)
provides groupKey and truncatedAlerts. It truncates before constructing template
data. Reject any truncation rather than interpret a subset's status as the full
group's state. The [notification pipeline](https://github.com/prometheus/alertmanager/blob/v0.34.2/notify/notify.go)
applies inhibition and silence before receiver delivery, so even an untruncated
webhook is a notification view, not a complete inventory of group members.

The [v2 API schema](https://github.com/prometheus/alertmanager/blob/v0.34.2/api/v2/openapi.yaml)
provides group labels, route labels, receiver and alerts, but no webhook groupKey
or monotonic group generation. Alert updatedAt is a time, not a sequence. A
prototype must prove an unambiguous lookup under one fixed receiver route;
matching only group labels across several routes is insufficient. An absent,
ambiguous, suppressed or unavailable source must defer a claim. Neither a
silence nor disappearance constitutes an acknowledgement.

The [upstream alert-client contract](https://github.com/prometheus/alertmanager/blob/v0.34.2/docs/alerts_api.md)
requires recurring firing updates. Native alerts can resolve when endsAt expires,
including after a sender stops. A resolved notification reports Alertmanager's
lifecycle; it cannot by itself prove recovery of a node or rack. Qualification
must pause updates through expiry and resume with the same fingerprint and
startsAt, as well as with a genuinely new start. The current occurrence ID and
relay transaction scheme omit refreshed endsAt. Whether they can represent that
expiry/resumption without suppressing a later required notification remains an
open native qualification, not an established product failure or a passing case.
Pipeline missing-signal notification remains the independent OBS-206 boundary.

### Next prototype and rejected shortcuts

Keep source, receiver, tenant, Workspace and trusted grouping labels in every
identity and database constraint. The group key selects a current scoped group;
it cannot be the forever identity of an incident. Persist a separate incident
identity and generation with bounded occurrence membership. Serialize group
creation, acknowledgement and delivery claim in PostgreSQL, reusing the current
lease, stale-completion and unknown-COMMIT controls. Use one idempotency key per
incident escalation step. A changed member list must not mint another escalation
step or reset an existing acknowledgement. Preserve individual occurrences
for diagnostics without fanning out notifications.

Reject one incident forever per groupKey, membership-digest incident IDs, one
escalation per alert and label silences used as acknowledgement. They respectively
conflate recurrence, reset acknowledgement during churn, amplify the storm or
silence future incidents. Do not mount a product group handler until native
lookup, closure and expiry/resumption behavior are proven outside product code.
Only incident, acknowledgement, membership identity and delivery state belong in
SQL. Metric samples, log entries, webhook payloads and label/annotation bodies
remain in their existing external sources.

For a declared layout of 10,000 nodes in 250 racks of 40, a synthetic
40-member-group fixture contains 250 incidents and 10,000 member identities
before delivery attempts. This is a fixture shape, not the existing inhibited
rack policy's actual delivered membership.
This is row-count arithmetic, not measured storage or latency. The prototype
must report PostgreSQL relation and index bytes and request latency for that
metadata shape with concurrent acknowledgement and claims. It must use indexed
scoped lookups, bounded group membership, finite attempts and bounded retention.
Keep those results separate from the native telemetry store's
[17-million-series measurements](telemetry-scale.md#linux-amd64-qualification).

Extend the disposable Alertmanager/PostgreSQL qualification with one 40-member
group, concurrent responders and claimers, membership additions after
acknowledgement, partial resolution, complete resolution and recurrence. Verify
one accepted message after a lost durable receipt and restart. Include identical
rack/group names in another Workspace and tenant, a truncated webhook, ambiguous
source groups, silence/inhibition, source loss, expiry/resumption, delayed old
webhooks, a coordinator crash, stale lease completion and an unknown commit.
Every refused operation must preserve scope and make no unintended delivery.
Use controlled intranet endpoints with no external route. No run is claimed for
these new cases. The current single-occurrence proof retains its original scope.

Reuse the Go, pgx, PostgreSQL, pgvector and Alertmanager versions and licenses
recorded above; their commercial, offline and redistribution terms still apply.
No dependency, model or external dataset is added, and no proprietary vendor
implementation or documentation text is copied.

### Lifecycle observer inputs

The host-only lifecycle observer uses the existing Python standard library under
[PSF License Version 2](https://docs.python.org/3/license.html), permitting
commercial/offline use and redistribution under its notice and license terms.
It adds no Python package or shipped product dependency. Host namespace tools
remain development prerequisites.

The pinned Linux amd64 Alertmanager 0.34.2 archive matches the official
[release checksum](https://github.com/prometheus/alertmanager/releases/download/v0.34.2/sha256sums.txt):
17ee14acd0f6ba6e25a436ff223d9c17ddd5cf484f543ad12f0932eaa89ba186.
Its regular executable member has 50,164,934 bytes and SHA-256
65b77b431acf07fe0df45158a0a6f3060e53e0b592f641d971fb3a745c34f432,
matching the existing qualified native binary. This establishes artifact identity,
not a lifecycle run. The external observer source
aea19520431f474d5c3cfd273eed78be8c0842a2bc1a343e0e2921226a93dc06
has passed syntax inspection only. Its initial scope is expiry and resumption of
one fixed-route synthetic group; grouped acknowledgement, SQL durability, source
lookup ambiguity and idempotent delivery still require the broader profile above.

## Bounded external lifecycle runner

The unexecuted observer now has a separate Linux admission runner at
/root/lane-obs-tools/run-alertmanager-expiry.py, SHA-256
f192668b28b94c1811693b580c7ea4065a7d6d1283af8d77305172533ad2f4ac.
Its observer input remains SHA-256
aea19520431f474d5c3cfd273eed78be8c0842a2bc1a343e0e2921226a93dc06;
the Alertmanager binary remains the independently verified 0.34.2 artifact
recorded above. Both Python sources passed AST parsing only. Neither runner,
namespace nor native observer has executed.

The runner accepts explicit observer/binary paths and SHA-256 values, an output
parent, and the two shared lock paths. It requires existing regular lock files,
acquires its own nonblocking flocks and checks free memory, disk and I/O before
spawning a namespace. The prepared guard uses the shared 2 GiB MemFree, 15 GiB
disk and at-most-five I/O avg10 policy, records df/free/ss, sets GOMAXPROCS=2,
and bounds the default observation to 180 seconds (configurable 30..300).

Its child has isolated network and PID namespaces with kill-child behavior.
The observer checks live netlink interfaces and default routes, rather than
the host-mounted sysfs inventory. Only synthetic Alertmanager and an HTTP
receiver can start. The parent passes its actual lock descriptors into that
owned child and waits for the process group. If cleanup cannot establish an
empty group, it records failure and closes without explicitly unlocking the
shared open description; inherited descriptors retain custody until child exit.
An interrupted observation with no native result is unknown, never proof that
no binary started.

A new private receipt records admission refusal separately from native execution
and retains the observer output and digest. These are source-reviewed teardown
intentions, not qualified teardown or group-lifecycle results. Run only in the
allocated Linux resource window. The real expiry/resumption observation, grouped
acknowledgement, SQL durability, production migration and Console work remain
open; the earlier forty-member inhibition result is unchanged.

## Lifecycle startup fact boundary

The unrun f192 runner infers native_started from the presence of version output.
That only proves the short version command succeeded; the later service Popen can
still fail. Keep the original observer/runner immutable and prepare a new pair
that records process creation, readiness and termination separately. Python's
[Popen pid, poll and wait](https://docs.python.org/3/library/subprocess.html)
provide process facts; the native
[/-/ready endpoint](https://prometheus.io/docs/alerting/latest/management_api/)
provides readiness. Neither fact substitutes for the other.

Set started only after the service Popen returns with a positive child PID,
ready only after its readiness response, and stopped only after its exit has
been observed. A missing observation leaves startup unknown, while a recorded
pre-start failure is false. Require explicit boolean facts and reject impossible
or contradictory completed results. Version output alone must never turn a
pre-start failure into a started service.

Keep the field decoder importable without command execution so pure tests can
cover version-only failure, process-created/readiness-failed, stopped processes,
complete success, missing/wrongly typed fields, invalid PIDs and contradictory
completion. Preserve the native fixture's hashes, two locks, resource floors,
network isolation, deadlines and cleanup custody. This changes only external
prototype reporting, not product grouping or escalation. The existing Python
standard library uses the [PSF license](https://docs.python.org/3/license.html),
allowing commercial/offline use and redistribution with its terms. Alertmanager
remains Apache-2.0; no dependency, model or dataset is added. Native execution
is still required to qualify the lifecycle behavior.

The external runner also needs an explicit optimization-mode refusal: an assert
checking __debug__ is itself removed by python -O. Keep the existing assertion
checks active by exiting explicitly before argument processing under optimization.
A pure CLI regression with -O --help can verify refusal without opening locks or
starting a native service.

The revised external pair is prepared at
/root/lane-obs-tools/observe-alertmanager-expiry-v2.py (SHA-256
b2240790e1c5ca172af7c4f033ca8ae2a4e65041daec3bd719d27f1c5b98d152)
and /root/lane-obs-tools/run-alertmanager-expiry-v2.py (SHA-256
0c8b843cde6b8c39bfd447d9569f011f0462633c84c4c4d9bba67b14fca0fb8c).
The observer now records explicit service creation and PID, HTTP-200 readiness
and observed termination; it also records termination if a created process has
already exited. The runner strictly decodes those facts, requires readiness for
completion, and refuses optimization explicitly before argument processing.
The original aea195/f192 pair remains byte-for-byte unchanged.

The directly runnable input package is
/root/lane-obs-tools/obs104-expiry-v2-package.json, SHA-256
975f70fa1a378ba934be7e0b0124a970cacd8c7f923adc3f842b0c244020423c.
It binds the cached Alertmanager 0.34.2 binary and new source hashes, both existing
lock paths, the original resource floors and child deadline, and a 240-second
caller bound with a ten-second forced-stop grace. It has not been invoked.

The nine pure decoder/optimization CLI tests at
/root/lane-obs-tools/test-alertmanager-expiry-facts.py, SHA-256
89526232c065f0e6df07b8e7ceda89aaa9aaadafdcca6a53fcdba34e27dc6ee4,
passed on macOS arm64 with Python 3.9.6: nine run, zero failures, errors or skips.
The tested runner hash remains 0c8b843cde6b8c39bfd447d9569f011f0462633c84c4c4d9bba67b14fca0fb8c.
This qualifies explicit lifecycle fact decoding and optimization refusal only.
Earlier nonblocking workstation admissions returned 75 before a test process
started; those refusals remain separate from the passing unit result.

The shared short batch retained one parent and its actual lock FD across the
Python tests, the separate controlled Registry client tests and cross-build.
Its single bounded kernel wait acquired the original regular carrier after
569.214 seconds; execution took 3.611 seconds. All owned child groups exited,
the parent closed its FD and exited zero, and the carrier retained its original
identity. The private proof is
/root/lane-obs-tools/obs-short-validation-kk2e5xc6/proof.json, SHA-256
e7311787dc0bc7307b34c21d146440b43e5ec0f13030442b08af2bca7f2b3564.
The Python log and machine-readable result have SHA-256
70f706a53f726aa469db94639670b2ab43e20079c61d8078fd71d96f0450a92e and
2941bb6f500d3402c5bfdb80618d1fc53aa0b93ab49296165653e06742bac1f6.
No Linux lock admission, native Alertmanager version or service invocation,
receiver, database or installed request occurred in this batch. The native
expiry/resumption run, product grouping/escalation and OBS-104 remain open.
