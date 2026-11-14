# AUD-111 site-local SIEM delivery

[RFC 5424](https://datatracker.ietf.org/doc/html/rfc5424#section-6) defines
the syslog message format. [RFC 5425](https://datatracker.ietf.org/doc/html/rfc5425#section-4.3)
frames each message as a byte count, a space and the exact message bytes.
[RFC 9662](https://datatracker.ietf.org/doc/html/rfc9662) updates secure
syslog cipher requirements. Rsyslog's
[TCP receiver](https://docs.rsyslog.com/doc/configuration/modules/imtcp.html)
supports octet-counted framing and certificate-based peer authorization.
These are design references. The implementation uses Go's existing standard
library, under the BSD-3-Clause license, with no added dependencies, models
or datasets. Synthetic fixtures run offline and contain no customer records.

The serving role lacks access to the private chain head, and the existing
partitioned audit table has no chain-position index for a continuous seek.
Keep this slice in the operator's existing
[versioned export path](../../tools/audit-verify/README.md). An operator
with export authority supplies a bounded contiguous window with independently
held predecessor and final checkpoints. Validate the entire window with
the existing V1/V2 verifier before a network write. Build a separate
projection with the event UUID, granted tenant UUID, UTC time, action and
outcome. Require bounded action and outcome codes. Consume foreign tenant
positions for continuity while withholding their records. Leave reasons,
subjects, actors, request IDs, target data, scope IDs, metadata and chain
digests in the original evidence. The SIEM payload is a versioned projection.

Use a protected local grant file for one tenant, a literal private or
loopback IP endpoint, an explicit TLS name and a SHA-256 receiver certificate
pin. Use only the configured site CA roots and a client certificate key pair
read from files. TLS 1.3 and ordinary PKI name and chain verification apply
before the extra leaf pin. Literal addresses avoid DNS egress and rebinding.
UDP, plaintext, webhook and tenant-supplied URLs are outside this slice.

Keep a single-writer, node-local cursor in a dedicated protected directory.
Bind it to the granted tenant and recipient identity. Verify the saved
position and hash within each input window or at its predecessor before
resuming. Stop at the first known write failure, persist only the successfully
consumed prefix, and retry the same event within a bounded budget. Use
atomic rename and file and directory sync for each committed prefix. A
crash before the checkpoint can replay a completed local write. Use the
event UUID and tenant UUID for receiver deduplication. Expired ranges,
checkpoint mismatches and corrupt cursors fail closed and require the
operator to restore the missing export or cursor evidence.

[RFC 5425 reliability](https://datatracker.ietf.org/doc/html/rfc5425#section-6.3)
defines no receiver application acknowledgment. Count locally completed
TLS writes and distinguish a refused or blocked delivery from the dropped
counter, which remains zero while this path stops on failures. Receiver
durable ingestion is a separate acceptance property.

In a disconnected Linux network namespace, a TLS 1.3 receiver authenticated
the client and parsed three octet-counted messages from the authorized
tenant. An interleaved foreign record advanced continuity without leaving
the process. The sender retried an interrupted first connection. Wrong CA,
name and pin were refused before a frame write, and the receiver rejected
an untrusted client before reading any bytes. Repeating the same window
sent zero messages. Cancellation and a forced write failure retained their
prefix, and reopening the cursor resumed at the first undelivered event.
The receiver was the loopback fixture. An approved site SIEM remains an
acceptance target.

A trusted control startup still needs explicit installation and tenant grants,
an owned least-privilege export or indexed PostgreSQL reader, a persistent
operator volume and lifecycle registration. Keep AUD-111 open until that
startup and an approved site SIEM acceptance are available.
