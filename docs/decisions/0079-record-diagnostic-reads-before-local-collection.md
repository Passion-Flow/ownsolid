# Record diagnostic reads before local collection

Status: Accepted

## Context

The installer collects diagnostics while Control and Docker may be unavailable.
Both local and managed collectors already project a bounded report and keep
credentials and raw source values outside the archive. They did not account for
the diagnostic access before reading its sources. Existing backup/restore log
appends warn after an operation and cannot enforce a pre-read requirement.

## Decision

Both real support consumers use the existing protected operations.log. Admit
and synchronize an attempt before invoking any collector. Bind the attempt to
the kernel real/effective UID, fixed collector kind and input digest. Append a
terminal outcome with the exact archive digest and length before returning
success. Use the installation operator directory for local collection and the
protected cluster-input directory for standalone managed collection.

Create the log exclusively; reopen an existing owned regular entry without
creation, then recheck its pinned identity. Keep the descriptor pinned, serialize
bounded appends, synchronize the file and
directory, and refuse an unsafe or replaced file or an incomplete trailing
record. Never truncate or replace the operator log. Preserve the ordinary
backup/restore records and archive redaction. If admission fails, collect
nothing. If terminal recording fails, withdraw only this attempt's archive;
return an actionable cleanup error when withdrawal cannot be confirmed.
Do not retry an uncertain terminal append. A process interruption leaves its
original attempt for inspection. An explicit retry is a separate attempt.

These are local operator records. They neither authorize the remote read nor
identify a browser actor. Existing Admin/Kubernetes source authorization still
applies. There is no implemented server consumer for these retained support
attempts. Do not fabricate SQL audit events or claim automatic reconciliation.
The future real consumer must authorize the installation and preserve attempt
identity, exact digests and idempotency under an unknown commit.

## Consequences

Recording failures can prevent diagnostics even while other sources are down.
Operators must preserve and repair the protected log rather than discard an
incomplete attempt. The existing control-down collector remains usable when
its local durable operator log is available. No additional ledger, dependency,
model or dataset is introduced. AUD-108 remains open until the actual server
consumer and end-to-end reconciliation are implemented and verified.

See [diagnostic research](../research/support-diagnostics.md#durable-local-operator-access-before-diagnostic-collection).
