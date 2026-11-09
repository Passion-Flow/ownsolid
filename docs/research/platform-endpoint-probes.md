# Offline platform endpoint probes (OBS-112)

## Platform readiness

Use the [Prometheus multi-target exporter pattern](https://prometheus.io/docs/guides/multi-target-exporter/).
A local Blackbox Exporter performs a GET against each operator-configured
HTTPS readiness URL. vmagent runs the scrapes, rewrites the target URL
into the probe parameter and writes the resulting Prometheus metrics to
VictoriaMetrics. Keeping these services separate measures the network,
TLS and response from the intended observation point. A direct scrape of
an internal process counter would not test that path. A bespoke ownsolid
HTTP poller would duplicate upstream timeout, TLS, metrics and scheduling.

Use the [pinned probe contract](https://raw.githubusercontent.com/prometheus/blackbox_exporter/v0.29.1/CONFIGURATION.md)
with a site CA, TLS 1.2 minimum, a four-second bound, an exact HTTP 200
expectation, disabled redirects, and no HTTP proxy. The platform profile
does not send authentication. Probe only operator-configured readiness
URLs. The example uses one site, static inventory and a 30-second scrape
interval. Give every readiness endpoint a stable site/endpoint identity.
Use stable endpoint names in samples rather than raw URLs and retain
only the probe success, duration and status code from the exporter's
response. The scraper's own health metrics remain separate. Runtime
storage remains in VictoriaMetrics. No target, URL parameter, or metric
label comes from tenant input.
[vmagent documentation](https://docs.victoriametrics.com/victoriametrics/vmagent/).

Keep the exporter listener accessible only to the local trusted vmagent.
Because /probe accepts a destination, enforce an operating-system or
network allowlist for exporter egress covering only the intended private
targets. A CA authenticates a destination but does not authorize all
network calls. Do not expose the listener through Control, Portal or a
customer ingress. Keep the readiness endpoints themselves on the private
operator network. Production configuration requires the release owner's
offline packaging and site firewall policy.

The [Blackbox Exporter 0.29.1 release](https://github.com/prometheus/blackbox_exporter/releases/tag/v0.29.1)
and [license](https://raw.githubusercontent.com/prometheus/blackbox_exporter/v0.29.1/LICENSE)
are pinned. The published Linux amd64 archive SHA-256 is
b82b81839e6317e84fdd367f46d7ddd3b773fa09cbe2e3d5633a59171d741e76.
The [VictoriaMetrics 1.155.0 release license](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/v1.155.0/LICENSE)
covers the existing vmagent and storage binaries. Both projects use
Apache-2.0, which permits commercial use, offline use and redistribution
subject to license and notice obligations. There is no SDK, model or
external dataset.

Qualify the real exporter, vmagent and VictoriaMetrics together on a
loopback-only Linux namespace. Observe a successful real private platform
health endpoint, then exercise 503, redirect refusal without a redirected
request, a rejected certificate, and recovery in the remote-written
metrics. Check that exporter failure is distinguishable from an HTTP
probe returning failure. Verify that labels contain no destination URLs
and record the actual stored series count per configured endpoint.

## Verified Linux path

On Linux amd64 with only loopback, Blackbox Exporter 0.29.1 probed the
real VictoriaLogs 1.53.0 health endpoint through a controlled HTTPS
reverse proxy. vmagent 1.155.0 scraped the probe and wrote to
VictoriaMetrics 1.155.0. The test used the example configurations with
local destinations and an accelerated two-second scrape interval.

The store reported HTTP 200 and successful probes, then a forced 503
while the exporter scrape itself stayed up. A 307 produced a failed
probe and no request to the redirect target. An untrusted certificate
produced no HTTP request. After the controlled fault was removed, the
store again reported 200 and successful probes. Only stable site,
endpoint, instance and job identifiers accompanied the metric name.

The store contained ten series per endpoint, consisting of three probe
series and seven vmagent scrape series. At 30 seconds per scrape that
is up to 28,800 samples per endpoint per day before retention and
compression. This measures stored series count, rather than disk use
or query latency across a production fleet. The test verified the
telemetry transport and a platform health endpoint. Installer-managed
placement, site egress policy, ownsolid entrance deployment and
model-route qualification remain open.

## Model-route probes

An authenticated model probe requires the gateway owner's stable route
inventory, one least-privilege synthetic caller identity per scope, and
an approved inference budget and output validation contract. The
worker owner must specify rotation, timeout and lifecycle handling.
Those inputs are required to verify real model-route probes offline.
The platform profile does not use or grant a model caller identity.
