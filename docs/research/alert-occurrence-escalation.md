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
