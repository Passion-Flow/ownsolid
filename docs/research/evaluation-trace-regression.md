# EVL-104 production trace regression cases

MLflow lets an evaluator select individual traces, attach reviewed
expectations and add the selected inputs to an evaluation dataset.
OpenTelemetry keeps prompts, instructions and responses out of default
GenAI telemetry. It describes separately authorized content storage
with references on spans for production systems that need to retain
content. [MLflow evaluation datasets](https://mlflow.org/docs/latest/genai/datasets/),
[OpenTelemetry GenAI content capture](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-spans.md#capturing-instructions-inputs-and-outputs).

The proposed flow uses a tenant-scoped, explicitly authorized capture
and an existing dataset draft and publication. The trace reference
identifies the source, and a reviewed expectation supplies the
regression target. The observed response can explain why the trace was
selected. A reviewer must approve any use of that response as an
expectation. Preserve the source model revision, generation profile
and approved content digest alongside the case. A dataset revision
freezes the approved input and expectation so that changes to the
source or its retention do not silently change future scores.
Use the existing offline evaluation path to replay published cases.

Keep raw message bodies out of ordinary request logs, metrics, audit
events and CSV exports. The current gateway
[usage export](../../services/control/internal/modules/distribution/usage_export.go)
contains request identity, state, latency and token counts. It does not
contain the input or output necessary for a regression case. Treat
request IDs as correlation keys only. A trace lacking approved content
may be inspected for operational measurements. It cannot yield a replay
case until a reviewer supplies or authorizes the required input.
Reusing an existing authorized dataset draft keeps one publication path
and avoids a second data store or an automated copy of production
requests into evaluation data.

MLflow and the OpenTelemetry GenAI conventions are Apache-2.0
references. Their terms permit commercial and offline use and
redistribution subject to the license conditions. This design adds no
dependency, model or external dataset. Customer capture rights and
retention policy apply to any imported content. Tests use synthetic
requests and synthetic expectations. [MLflow license](https://github.com/mlflow/mlflow/blob/master/LICENSE.txt),
[OpenTelemetry license](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/LICENSE).

## Acceptance boundary

An authorized evaluator selects a scoped trace and reviews its sanitized
input and an explicit expectation before creating a draft case. The same
trace and review revision can be retried without duplicating the case.
Publication binds case content to an immutable dataset revision and the
offline scorer produces the same score from the same retained outputs.
Test missing or expired captures, cross-tenant and cross-workspace
references, missing review, repeated requests, redaction, failed
publication and source retention expiry. Refused attempts must leave
the existing case and dataset unchanged.

The product flow requires the OBS-103 trace source, a scoped
content-capture authorization and the authorized EVL-101 dataset
workflow. The existing receipt and offline replay are available
separately. The trace-to-draft product flow remains open.

## Retained native trace qualification

The [native preparation replay](https://github.com/Passion-Flow/ownsolid/commit/0a722cff)
used generated CC0 holdout inputs, an approved Apache-2.0 Qwen model
revision and the existing offline worker. The retained result records
per-case input digests, expected labels, output digests, execution
profile and the original request digest. Qualify this trace shape as a
bounded offline handoff. Keep the three original review expectations.
Never derive expectations from the observed model outputs.

Use the existing Go model-lifecycle result decoder to independently
verify the pinned request and result, original holdout and tokenizer
template. Reuse the datasets record encoding and publish API. Carry the
exact result digest in the dataset origin and derive stable source IDs
from that digest and the original case identity. The publication manifest
then binds the derived bytes under the ordinary dataset authorization.

Run the proof outside product code in a disconnected repository fixture.
Recreate only the archived synthetic tenant and workspace identities
in a disposable schema. Issue actual Portal sessions, recheck current
scope and require an owner for the fixture review operation before
returning converted rows. Use the normal dataset service for the draft
and publication. Assert that a viewer and an owner of a different
workspace receive no rows, a missing review or modified capture fails,
and an exact replay keeps the same dataset and version. Read back the
immutable rows and audit attribution with a real processor and
PostgreSQL. The fixture uses the existing Go, PostgreSQL and CPython
dependencies, the pinned tokenizer metadata, and no production trace
content or new dependency.

### Disconnected result

On Linux amd64 at `1af64277`, a build-only overlay used actual
PostgreSQL 18.6, pgvector 0.8.7 and the pinned CPython 3.12.15 data
processor. The issued Portal owner session passed the current source
workspace check. The same check refused a viewer and an owner of a
sibling workspace. Missing review, altered review bytes and an altered
report yielded no cases or database writes. The real publication service
also refused the viewer and left the dataset version count and audit
trail intact.

The owner converted and published all three reviewed cases. Exact creation
and publication retries returned the same dataset and job, with one
immutable version and one scoped publication audit. The authorized
published source read returned all three cases with the same expectations
and provenance IDs. An independent disconnected Python calculation
matched the Go conversion's corpus digest. A second disposable database
produced the same corpus and manifest digests.

| Retained or derived input | SHA-256 |
| --- | --- |
| Original report | `5b244e7c87060e0afab4c4dcd57387f46964d4e37506ad96827df82a0b5ecf47` |
| Original request | `6d3cf80e42a8066b196f39d07fb9976f7971b0d4ebbfff734b1630d6f783155e` |
| Reviewed holdout | `0fd36fecf2b79bfd5e23a09cf0c66d395304824d5ae18ca4be8d01a23f9b09af` |
| Derived corpus | `7ab44ad04a76a76cd18d5c2fcd123b7b165e50da4e92f28b19ded914c24b5be4` |
| First publication manifest | `f33528ef595cf6075265a44b9551be158070ce4cb744ab67c1bbc6f980607491` |
| Qualification overlay source | `33c2f6c55efb24797fed6b9415ba6e7ff3cdf1debfa07cbeae82f8dc41aa96b6` |

The overlay test `TestRetainedCPUTraceScopedRegressionPublication` and
its runner use the original private archive inputs and the existing
repository fixture. The prototype exercised retained native execution,
synthetic actor provisioning and the existing dataset publication path.
Production capture acquisition, expiry and the review interface remain
open. The synthetic review fixture supplies the expectations for this run.
