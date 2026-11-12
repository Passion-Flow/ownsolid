# Bounded HTTP request classes (OBS-106)

The process recorder uses a closed set of route families. The monitoring
contract permits health as a family. Its earlier classifier recognized
only /healthz and /readyz. The actual
[health owner](../../services/control/internal/platform/health/handler.go) and
[Admin dispatcher](../../services/control/internal/modules/identity/http/handler.go)
serve /health/live and /health/ready. Before this correction, those paths
entered other and were missing from the health-family view.

[Prometheus instrumentation guidance](https://prometheus.io/docs/practices/instrumentation/#do-not-overuse-labels)
bounds label cardinality. The
[OpenTelemetry HTTP route convention](https://opentelemetry.io/docs/specs/semconv/registry/attributes/http/)
also separates bounded route information from raw URLs. Keep the recorder's
existing family contract rather than introduce raw paths, query values,
tenant IDs or a new routing/trace dependency.

Recognize the two actual exact paths in the existing health family, preserving
the two legacy classifications. Reject prefix matching because arbitrary
suffixes are not registered health routes. All unknown paths still collapse
to other, and HTTP failures retain their outcome classification. This changes
no readiness, authentication or serving policy.

The same dispatcher recognizes the current /agent/v1/ and /gateway/v1/
machine families plus the exact
[agent gRPC SessionPath](../../api/agent/v1/session.go).
The earlier recorder recognized only their legacy API-prefixed families.
Classify the canonical machine roots and the exact gRPC method after the
existing cell-relative normalization, preserving legacy classes. Do not infer
a family from a nearly matching version or arbitrary gRPC method name.
Metrics describe HTTP transport outcomes; this adds no RPC status decoding
or agent/gateway authorization. Unit refusal traffic must verify that scoped
node/instance IDs and query values never become labels.

The existing dependencies are Go under
[BSD-3-Clause](https://go.dev/LICENSE),
[httpsnoop at b0adf18e9f12 under MIT](https://github.com/felixge/httpsnoop/blob/b0adf18e9f12/LICENSE.txt),
and [google/uuid 1.6.0 under BSD-3-Clause](https://github.com/google/uuid/blob/v1.6.0/LICENSE).
They allow commercial use, redistribution with their notices and offline use.
No dependency, model or external data set is added.

Verify the actual health owner through the recorder over real private TLS
using HTTP/1.1 and HTTP/2. Liveness succeeds; readiness without a repository
correctly refuses. Legacy and near-match paths remain refused by the health
owner, while only the exact declared routes get the health family. Assert
counts, outcomes, a closed family set and drained inflight state. Run the
existing recorder tests for transport transparency and redaction. This
qualifies process instrumentation, not installed readiness or a dashboard.

## Verified route classification

On Linux amd64, 4f94a898 passed the complete monitoringmetrics package with
the race detector in a loopback-only namespace. The run held both shared
locks after an I/O pressure preflight. Real private TLS HTTP/1.1 and HTTP/2
each exercised the actual health owner: liveness returned 200, readiness
without a repository returned 503, and legacy and near-match paths returned
404. The recorder produced only the expected health/other families and
transport outcomes, with no leaked inflight request.

The machine refusal controls covered the exact gRPC method, canonical and
cell-scoped node/gateway roots, legacy roots, and near-match versions or
method names. Snapshots preserved refusals and contained no generated
node/instance ID, site/cell identity or query value. Existing transport,
SSE, optional-interface, redaction and overflow tests passed in the same
package run. This does not qualify agent or gateway authentication, RPC
semantic outcomes, installed readiness or the complete OBS-106 dashboard.
The in-process TLS listeners closed and no external service was started.

The complete owned-module race suite passed again in the combined Linux
acceptance at 876f9c5b, together with make check, make build and the three
native history profiles. The route correction adds no label values beyond
the existing closed families.
