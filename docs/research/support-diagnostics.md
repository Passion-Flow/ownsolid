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
