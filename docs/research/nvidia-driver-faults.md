# NVIDIA driver faults

GPU-102 needs a stable interpretation of driver events so an operator can
distinguish an application failure from evidence that a GPU needs service.

[NVIDIA's Xid catalogue](https://docs.nvidia.com/deploy/xid-errors/analyzing-xid-catalog.html)
defines the event numbers and their generation-specific recovery guidance.
[GPU Node Triage](https://docs.nvidia.com/deploy/gpu-debug-guidelines/gpu-node-triage.html)
describes combinations such as Xid 48 with 63 or 64, and distinguishes
contained and uncontained ECC incidents. The
[row remapping guide](https://docs.nvidia.com/deploy/a100-gpu-mem-error-mgmt/595/user-visible-statistics.html)
explains that recording a remap can succeed. Treating every ECC-related event as
a failed remap would misclassify healthy devices. The
[DCGM health guide](https://docs.nvidia.com/datacenter/dcgm/latest/learn/modules/health-monitoring.html)
defines passive findings distinct from raw counters, including driver recovery,
fabric and thermal conditions.

## Collector design

Interpret selected Xids in the first Linux NVIDIA path. Advisory severity
describes urgency for the operator, never authorizes isolation or a reset.
Give unknown or context-dependent codes investigation guidance. Include typed
advice for selected named DCGM incidents as a separate source. A last-value
Xid gauge can survive the incident and cannot establish occurrence or ordering.
A cumulative ECC count can also predate the current workload or boot.

Use the [NVIDIA kernel message format](https://docs.nvidia.com/deploy/xid-errors/working-with-xid-errors.html)
and local systemd journal JSON records. Require both kernel transport and
kernel syslog facility. The [kernel ABI](https://kernel.org/doc/html/v6.12/admin-guide/abi-testing.html)
disallows userspace messages with LOG_KERN, even when userspace writes to
/dev/kmsg. Use [journal metadata](https://www.freedesktop.org/software/systemd/man/258/systemd.journal-fields.html)
for the current boot, event time and opaque replay cursor. Parse a bounded
batch with an explicit expected boot ID. Preserve the observed PCI address
and do not infer a function or GPU UUID. The consumer must resolve it to a
unique device using fresh same-boot inventory. A cached Xid, an unknown
code, a workload-origin log or a PCI address without that join cannot
authorize a device action.

Keeping raw message payloads would leak workload details and make the
operator API depend on vendor text. Kernel events and DCGM health IDs serve
different purposes, so do not turn both into automatic independent
incidents. Cursor persistence, device identity reconciliation and policy
belong to the authenticated runtime and control plane.

## Licences and verification

The implementation uses the Go standard library
([BSD-3-Clause](https://go.dev/LICENSE)). It reads systemd output using the
documented local format. systemd's journal tools are supplied by the node
OS under [LGPL-2.1-or-later](https://github.com/systemd/systemd/blob/main/LICENSES/LGPL-2.1-or-later.txt).
These licences permit commercial and offline use and impose notice and
other obligations if those works are redistributed. No systemd or NVIDIA
code, binary, catalogue text, model or dataset is incorporated by this
slice. The maintained rules use vendor event identifiers and locally
written advice.

Test benign workload, remap bookkeeping, and recovery summary Xids
alongside memory, drop-off, link and thermal cases. Reject malformed
trusted records and duplicate cursors without publishing a partial batch.
Ignore forged facility or transport, records from another boot, and
messages that merely mention an Xid. Test recognized and unknown advisories
separately. Full acceptance requires authenticated event delivery,
deduplication and an operator surface, followed by real NVIDIA events on a
qualified host. The CPU fixture establishes parsing and safety properties.

## Journal message encodings and simulated Xids

[Journal JSON](https://systemd.io/JOURNAL_EXPORT_FORMATS/#journal-json-format)
distinguishes text from byte arrays, repeated values and values omitted by
the output size limit. A valid byte-array message should not strand later
events behind its journal cursor. Decode a single byte array without lossy
UTF-8 conversion, match only the existing ASCII Xid prefix and keep no
payload. Skip missing, size-omitted and multi-value messages because they
cannot identify one unambiguous event. Reject malformed messages and keep
the existing atomic batch limits. Treating all arrays as errors can block
unrelated events. Choosing one repeated value could manufacture evidence.

The [NVIDIA R620 kernel log guidance](https://docs.nvidia.com/deploy/gpu-operational-events/620/analyzing-gpu-operational-events.html#kernel-log)
says that fault-injection events retain the legacy prefix and Xid code and
carry the `[SIMULATED]` marker. Exclude marked messages from fault
observations even when journal provenance is valid. The documentation
does not fix a marker offset. The collector conservatively withholds a
message containing that exact marker and does not infer health from its
absence. Replacing the existing parser with the preview structured
interface would add driver-version and privilege dependencies.

Use the existing Go standard library and systemd format under the licence
terms above. No new dependencies, models, data sets or vendor code are
shipped. Verify that ordinary Xids before and after documented alternate
encodings still arrive, that an Xid in a single byte array preserves its
identity, and that ambiguous arrays, binary data without the exact prefix,
and simulated drop-off and ECC records never produce observations.
Corrupt fields must continue to reject the full batch.

The Linux amd64 loopback-only discovery and inference suites passed. With
the original parser, the alternate encoding stopped the batch, and the
simulation fixture returned both marked events alongside the genuine ones.
The revised parser retains the genuine Xid 79 and 48 observations and the
Xid in a binary message. Invalid byte values and mixed encodings still
fail the batch. The server had no NVIDIA hardware. The driver fixtures
were synthetic, and reporting and quarantine remain separate gates.
