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
