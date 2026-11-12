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

## Pending coordinated backup state

The local backup writer may remain stopped after an interrupted fence.
A completed operator event is absent in this case. As with the restore
projection, collect recovery state before trying Docker. Reuse the
protected local coordinated backup intent reader and emit only a fixed
state for the selected site. `recovery_required` means a validated intent
exists. `fence_outcome_unconfirmed` means it still records a fence request
whose outcome was not acknowledged. These states describe the record.
Only the recovery command may compare it with PostgreSQL and resume the
writer.

An absent record omits the field. A malformed, foreign, unsafe or unknown
record yields only a fixed collection issue. No timestamp, site, cluster,
container, credential, operation or object identity enters the report.
Following [GitLab's host diagnostics](https://docs.gitlab.com/administration/troubleshooting/diagnostics_tools/)
and [Replicated's local redaction guidance](https://troubleshoot.sh/docs/redact/redactors/),
project the allowed fields at collection. Reject copying the raw intent
or checking the live fence through Docker. No dependency, model or dataset
is added. The existing BSD-3-Clause Go standard library and local tools
retain their licensing and offline requirements described above.

Test absent, valid, unacknowledged, foreign, unreadable, malformed and
unknown-version records. Run the native CLI on disconnected Linux with
Docker unavailable and verify that a private archive contains only the
allowed state and fixed issue codes.

On disconnected Ubuntu 24.04 amd64, the native CLI ran as a non-root
user against a private fixture with an unavailable Docker socket. The
mode 0600 archives contained the fixed unconfirmed state for a valid
record and the fixed collection issue for an unknown version. The raw
record remained byte-identical and its source identifiers were absent.
The recorder saw only the three IPv4, IPv6 and DNS probes.


## Durable local operator access before diagnostic collection

[OWASP event data sources and exclusions](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
place access capture with the consumer that knows the action and actor and exclude
credentials and payloads. The two real installer consumers are `SupportBundle`
and `ClusterSupportBundle`. Neither a credential file nor an offline log is a
browser identity or a server-authorized audit event. Preserve collection while
Control and Docker are down.

Reuse `operations.log`, with failure-propagating admission before the collector
can run and a terminal record tied to the exact private archive bytes. Record
only an opaque attempt ID, kernel real/effective UID, fixed collector kind,
input digest, UTC time, outcome and archive digest/length. Do not trust SUDO_UID,
USER, arbitrary reasons, remote identities or raw collector errors. A managed
collector uses the protected input file's directory as its operator directory;
a local installation uses its existing operator directory. Both directories
and the log must belong to the executing effective UID and refuse unsafe modes,
symlinks and special files. Every explicit retry is a new attempt.

[Go File.Sync](https://pkg.go.dev/os#File.Sync) flushes file contents; synchronize
the containing directory as well for a newly created log.
[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html) attaches the lock
to the open file description. Keep that descriptor for an attempt, serialize
bounded append/sync operations, and refuse a replaced carrier. A missing legacy newline is separated without
changing its bytes; record its incompleteness and snapshot history only up to
the pre-admission byte boundary. Never adopt an older attempt.
Do not truncate, rotate or replace the existing operation log. Preserve prior
backup/restore events and existing diagnostic redaction projections. A failed
admission must run no collector. Failed archive publication records failure;
a failed terminal record must release neither a success message nor a newly
published archive. A killed collector leaves an admitted, incomplete attempt.

The existing operator append is warning-only after mutation and cannot provide
this admission. A second access ledger, fake SQL append and automatic retry of
an uncertain remote audit commit were rejected. Server reconciliation requires
an actual authorized, installation-bound, idempotent consumer for retained local
attempts. No such support receipt consumer is present in the inspected contracts.
Keep receipts local and unreconciled until that owner contract exists; do not
claim AUD-108 complete or claim that a future database import occurred. Recovery
must preserve attempt IDs, exact input/archive digests and duplicate semantics;
explicit retry collects a new attempt without overwriting an uncertain one.

No dependency, model or dataset is added. The existing Go standard library's
BSD-3-Clause terms permit commercial, redistributed and offline use with notices.
OWASP and man-pages are references and are not embedded product content.

Test both actual collector entry points: durable admission before any protected
read, actual numeric operator identity, concurrent attempts, retry and restart
readback, Control/Docker unavailable, sentinel redaction, unsafe/unwritable or
replaced log, cancellation, failed archive publication and terminal recording.
Run the native CLI and protocol fixtures on disconnected Linux amd64 with the
existing egress recorder; a unit pass alone does not qualify an offline flow.

### Concurrent operator-log creation

The [open contract](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/open.2.html)
defines exclusive creation with O_CREAT and O_EXCL, followed by an existing-file
open when creation reports EEXIST. [Go's rooted open implementation](https://go.dev/src/os/root_unix.go)
also guarantees that create-exclusive will not follow a final symlink.
Use this create-or-open sequence instead of concurrent non-exclusive creation.
Before opening an existing entry, validate its owned regular-file identity and
private mode; recheck its identity under the append lock before any write.
Never recreate a log that disappears during an existing-file open.

A small Mac syscall prototype outside the product reproduced ENOENT with
concurrent non-exclusive creation using both ordinary and no-follow flags.
The create-exclusive/existing-open variant completed 80 attempts without an
error. This selected the approach; the Go concurrent-consumer regression and
native Linux entry-point acceptance still provide the product evidence.
No additional dependency or licence is introduced.
