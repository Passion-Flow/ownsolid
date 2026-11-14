# Tenant-scoped metrics federation (OBS-107)

[Prometheus federation](https://prometheus.io/docs/prometheus/latest/federation/)
scrapes selected recent samples and preserves their source labels with
honor_labels. The [VictoriaMetrics 1.155.0 federation handler](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/v1.155.0/app/vmselect/prometheus/prometheus.go)
uses the same label filter path as the store's other Prometheus read
endpoints. VictoriaMetrics documents exact extra_label parameters for
an authorization proxy to inject. Use the store's native exposition
format and an enforced exact tenant and workspace selector.
[VictoriaMetrics query enhancements](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/#prometheus-querying-api-enhancements).

Verify the candidate data-plane primitive using the real private store.
Seed the same named series for two tenants and two workspaces, plus an
unscoped series. A configured tenant/workspace must return only its
own sample, including when the scrape selector is broad or has multiple
alternatives. A selector for a different scope must return an empty
result. Repeat with a caller-supplied match that asks for an unscoped
series. This is store-level evidence and uses no customer destination.

## Native filter prototype

VictoriaMetrics 1.155.0 on loopback-only Linux returned four baseline
series across tenant/workspace combinations and a series without a tenant
label. An upstream request with exact tenant and workspace extra_label
parameters returned only the authorized value for a metric name, a
metric-name regular expression, and a two-alternative match list.
Explicit matches for another tenant, another workspace, and a missing
tenant returned no samples. The fixture used explicit sample timestamps
and a bounded query interval. The store stopped after the test.

## Product boundary

The product boundary requires trusted tenant/workspace labels from
ingestion and a Control-authorized export identity with explicit scope,
revocation and an allowlist of families. The proxy must construct the
upstream request from scratch, restrict it to the federation path and
method, and control deadline, response size and concurrency. Client
paths, extra filters, upstream host, timeout and arbitrary query
parameters must not be forwarded. The installer must restrict direct
store access and admit the customer's private scrape source. Operator
audit and rotation need the same authenticated scope as creation.
Only after those paths exist can the lane test actual customer
Prometheus compatibility and tenant isolation end to end.

Reject a shared store URL, since it gives the scraper access to the
unscoped read and write APIs. Reject treating a caller-supplied match
selector as authorization. The existing VictoriaMetrics release uses
[Apache-2.0](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/v1.155.0/LICENSE),
which permits commercial use, redistribution with notices, and offline
use. The native test requires no new runtime dependency, external
service, model or dataset. Grafana queries and log exports require
their own scoped API and contract tests.
