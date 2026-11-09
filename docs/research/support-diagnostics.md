# Redacted support diagnostics

## REL-105

[Replicated Troubleshoot](https://troubleshoot.sh/docs/support-bundle/collecting/)
separates collection from redaction and supports local collection.
Its [log collector](https://troubleshoot.sh/docs/collect/logs/) caps the
lookback and scopes it to selected workloads. Its
[redactors](https://troubleshoot.sh/docs/redact/redactors/) can transform
raw collector output. [Docker Compose ps](https://docs.docker.com/reference/cli/docker/compose/ps/)
exposes service state independently of the application endpoint.

Extend the existing local support bundle with a projection of the typed
installation configuration and recent installer operation events. The
configuration projection contains modes, configured timeouts, resource limits,
and retention periods. Omit identifiers, addresses, filesystem paths, credentials,
and arbitrary control settings. Read only a bounded tail of the local operator
log. Retain timestamps and the allowlisted operation and outcome. Omit details,
which may include paths and errors from subprocesses. Missing logs on an
installation that has never run a backup or restore are normal. A file that
cannot be read safely leaves the rest of the report available and adds a fixed
collection issue.

Keep diagnostics collection available when the Docker endpoint or console is
down. Reject copying the installer YAML, container logs, or arbitrary operator
log fields and trying to clean the result with a regex. Container output and
dynamic operation details can contain customer data with no reliable marker.
The supported report shows recent backup and restore events and deployment
state. Full container output and Kubernetes node logs are separate collections.

This uses the Go standard library under its
[BSD-3-Clause licence](https://go.dev/LICENSE), which permits commercial use,
redistribution and offline use with its notice. The existing Compose host
requirement is unchanged. No model, dataset or additional runtime is required.

Verify collection against a real isolated Compose installation with control
stopped. The archive must show the stopped service, installed schema, selected
configuration and real installer operations. Verify that arbitrary config
values and log details are absent, truncated or malformed records are dropped,
a symlink or special file cannot be read, and a failed Docker collector
still yields a private local report.

## Pending restore state

A process killed during logical restore cannot append its normal operation
event. The support bundle should still report which recovery phase is pending
when both Control and Docker are unavailable. GitLab's
[Self-Managed diagnostics](https://docs.gitlab.com/administration/troubleshooting/diagnostics_tools/)
also separates diagnostic collection from application availability and
sanitizes deployment configuration. Reuse the installer's existing protected
restore reader and include only its validated phase in the JSON snapshot.
The phase describes the installer record, independent of whether PostgreSQL
currently agrees with it. The recovery command still checks actual database
placement before taking action.

An absent record omits the field. A malformed, unsafe, or foreign record
omits the phase and adds a fixed collection issue. Do not copy the record
or report its path, site digest, cluster identifier, OIDs, or random suffix.
No dependencies, models, or datasets are introduced. Test all four phases,
absence, wrong site, symlinks and invalid payloads. Run the collector on
disconnected Linux with Docker unavailable and verify that it still emits a
private archive and the validated phase with zero egress.

### Disconnected diagnostic check

On 2026-11-09, the Linux amd64 `ownsolid-install support-bundle` binary ran
as a non-root user against a synthetic protected `loading` record on
`ownsolid-cpu`. Docker's Unix socket was deliberately unavailable. The
mode 0600 archive included `pending_restore_phase: loading` and the fixed
`DOCKER_UNAVAILABLE` issue. It excluded the installation root, site digest,
cluster identifier, database OIDs and candidate suffix. The network
namespace had only recorder routes and the capture contained only its IPv4,
IPv6 and DNS probes. The check used no containers.

## Managed cluster and Helm diagnostics

The existing [cell-clusters API](../../api/openapi/v1/cell-clusters/openapi.json)
lists the selected site and cell's managed cluster using an Admin session.
Its state and counts reflect the Control plane's records. A GET does not
schedule fresh observations. The [Helm chart](../../deploy/helm/ownsolid/README.md)
creates a Deployment and ClusterIP Service named from the selected release.
Kubernetes 1.36 exposes a named
[Deployment](https://v1-36.docs.kubernetes.io/docs/reference/kubernetes-api/apps/deployment-v1/)
and [Service](https://v1-36.docs.kubernetes.io/docs/reference/kubernetes-api/core/service-v1/)
through stable REST endpoints. Deployment generation and observed generation
distinguish a pending rollout from the current controller view. Service spec
only establishes HTTPS routing configuration. It does not establish ingress
or endpoint health. Replicated Troubleshoot's scoped collectors are a
comparison. This slice collects a smaller projection without invoking a
general-purpose diagnostic collector.

Keep both sources independent under one explicitly bounded collection budget.
Use the existing support archive publisher, with a separate protected cluster
input so Compose installation, recovery binding and diagnostics remain
compatible. Accept an existing Admin session value and a short-lived Kubernetes
service account token as separate protected file references. Kubernetes
[RBAC](https://v1-36.docs.kubernetes.io/docs/reference/access-authn-authz/rbac/)
can limit the token to GET on the two exact names. Both origins verify TLS
against their explicitly selected site CAs. The client never follows redirects,
uses ambient proxies, discovers other scopes or executes kubeconfig plugins.

Project only recorded cluster and server state, member counts, fixed rollout
states and replica counts, and whether the named Service matches the chart's
HTTPS routing. Omit identifiers, URLs, UIDs, labels, selectors, raw errors,
credentials, workload specifications and arbitrary status messages. Verify
returned scopes and chart ownership labels before projection. Bound each
response and retain successful results when a sibling read fails. Use fixed
issues for denied access, missing objects, invalid responses, timeouts and
unavailable services. A cluster or installation readiness claim requires
separate live verification.

Alternatives included querying Helm release Secrets and invoking kubectl
with an ambient kubeconfig. The former requires reading broader sensitive
data and reports Helm history rather than current readiness. The latter
can invoke credential plugins and inherit a context or proxy. The selected
named REST reads require no new runtime binaries, dependency, model or
dataset. Go's standard library and the existing JSON and protected-file
readers keep their current licence terms. Kubernetes and Helm are comparison
and protocol targets, both [Apache 2.0 Kubernetes](https://github.com/kubernetes/kubernetes/blob/v1.36.0/LICENSE)
and [Apache 2.0 Helm](https://github.com/helm/helm/blob/v4.3.1/LICENSE).
These licences permit commercial use, offline use and redistribution under
their notice terms.

Exercise real HTTPS transport with local API responses in a disconnected
Linux amd64 namespace. Check exactly the three named GET paths, per-source
auth, permission denial, expired or untrusted credentials, timeout, offline
Control, wrong object identity, oversized bodies, redirect refusal, partial
success, and sentinel redaction in output. Re-run the existing Compose
diagnostic suite. A fixture is protocol evidence. A managed RKE2 cluster and
deployed Helm release still require their own installed qualification.

### Disconnected managed API check

On 2026-11-09, the native Linux amd64 installer ran as an unprivileged
user in a disconnected namespace on `ownsolid-cpu`. Two loopback HTTPS
fixtures served the existing Admin and Kubernetes API response shapes.
The CLI sent only the expected named GETs and kept each credential with
its source. Thirteen cases covered successful and partial reads, access
denial, timeout, a stopped endpoint, rejected TLS, unsafe credential
permissions, wrong scope and resource identity, oversized responses,
redirect refusal, and pending or failed rollout. The mode 0600 archives
contained the projected states and fixed issues. Sentinel identifiers,
endpoints, credentials, and response details were absent. The existing
Compose diagnostic tests passed in the same namespace.

The recorder captured only its IPv4, IPv6 and DNS probes. The check used
no containers or host ports. Live RBAC, RKE2, the installed chart and
cluster workload logs remain outside this protocol fixture.
