# 0050: Agent certificates

- Status: accepted (ARC-103)
- Date: 2026-11-08

## Context

ADR 0027 names an mTLS client certificate as the agent entrance's authentication. Agents used a
bearer credential alone, over one-way TLS. A rack that powers on could enroll without limit, and
nothing rotated. Research: [agent mTLS identity](../research/agent-mtls-identity.md).

## Decision

**A certificate identifies a node on every connection.** The agent generates an ECDSA P-256 key,
which never leaves the node, and sends a CSR with enrollment. Control signs a client certificate
naming the node in one URI SAN, `urn:ownsolid:node:<site>:<cell>:<node>`, usable only for client
authentication. The join token stays the bootstrap.

**The credential stays, bound to the certificate.** A call needs both, as in RFC 8705. The agent
entrance verifies a certificate only when one is sent, because enrollment has none yet and browsers
share the listener by default; browsers are never prompted, since the request names only the agent
CA. The path decides. On PostgreSQL, serving agents requires the CA, and a proxy that ends TLS in
front of the agent entrance is refused at startup. This is no switch: there is no way to turn the
rule off, only to serve no agents.

**Control pins the key in `managed_nodes`.** A connection's key must be pinned, and a pinned node
needs its certificate: otherwise `AGENT_CERTIFICATE_REQUIRED`, `_EXPIRED` or `_SUPERSEDED`, all 401
and all retried by the agent. Any other 401 still ends it. Pins live in the node view, so repeated
heartbeats stay in memory, and a session stream is rechecked on every exchange.

**Rotation has a bounded overlap and an explicit end.** The agent renews at a random moment 70% to
90% through the lifetime, with a new key saved before it is requested. Both keys work until the
agent confirms from a connection using the new one, which clears the old pin. A lost reply at either
step is retried harmlessly.

**A node recovers from an outage longer than its certificate.** Near expiry the agent stops
presenting a certificate, and the renewal path accepts the credential with a CSR signed by a pinned
key. A leaked credential alone gets nothing. A node from before this release has no pin, takes its
first certificate the same way, and is pinned from then on.

**The agent CA is separate from the serving CA, and only control holds its key.** A site may bring
its own serving certificate, and then has no CA key to reuse. A gateway gets the certificate only.
The file is a bundle, issuer first, so a replacement CA can be trusted before it signs.

**Enrollment is throttled per rack**, before any enrollment work, on the shared rate limit. The
rack is the source network, /24 or /64, configurable, until FLT-101 names racks (ARC-112). A refused
agent waits for the hint; the join token is not spent.

## Consequences

- Every agent call reads the pins, one statement unless a node view holds them, and the declared
  costs rise by one (ADR 0020). Enrollment also pays for the rack limit.
- Agents do not share connections, because a connection is one node's. The simulator gives each
  agent its own, and refuses `agents_per_connection` above one when agents hold certificates.
- A restore already revokes every node credential, so pins from a backup authorize nothing.
- Only PostgreSQL stores pins. MySQL and Dameng keep the credential alone until CMP-201 and CMP-202.
- Replacing the agent CA is a documented manual procedure until ARC-113; the console does not yet
  show certificate expiry (ARC-114).
