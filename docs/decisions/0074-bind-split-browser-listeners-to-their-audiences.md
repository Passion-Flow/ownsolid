# 0074: Bind split browser listeners to their audiences

Date: 2026-11-13

Status: accepted; complete four-entrance installed qualification remains open.

## Context

ARC-110 extends ADR 0027's shared browser socket to independent network binds.
The current process already prebinds a browser socket and an optional machine
socket, with one connection budget and one business graph. Merely opening a
second browser socket with the same handler would also expose the Admin entrance
through the Portal socket whenever a caller supplied a valid Admin Host and SNI.

## Decision

Add optional server.portal_listen_address. Omission preserves shared serving.
Selection makes server.listen_address Admin-only and the extra socket Portal-only.
Validate the existing transport policy and require the socket's audience before
the unchanged identity dispatcher. Do not rewrite Host, TLS or public origins.
Keep one connection budget, prebind every socket and join every bounded shutdown.

The browser certificate and transport mode remain shared configuration; direct
HTTPS still verifies both declared identities. The existing machine socket is
independent. The separately configured Rust model gateway is not moved into this
process. No agent wire, stored scope or session identity changes.

Extend protected environment selection and exact version-1 serving intent with
the optional public bind field. An old expectation asserts omission and refuses
split serving. Older binaries reject the new field; select split configuration
only after upgrading the binary. Returning to shared serving requires an explicit
valid configuration snapshot with the field omitted, not an empty override.

## Consequences

Operators can firewall the browser entrances separately without another process,
worker graph or installation owner. Ports still do not isolate cookies; the
existing audience, origin and CSRF checks remain necessary. Advertised origins
stay independent from local binds. Exact collisions fail validation, and actual
prebinding detects aliases and releases previously owned sockets on failure.

Delivery projection and the complete installed four-entrance path remain separate
acceptance gates. See the [research and failure coverage](../research/entrance-listeners.md).
