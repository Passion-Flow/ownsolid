# Agent mTLS identity, certificate rotation and enrollment throttling

Research for ARC-103 (catalog F575). Decision: [ADR 0050](../decisions/0050-agent-certificates.md).

## What exists

An agent enrolls with a one-time join token and receives a bearer credential that never
expires. Control stores its SHA-256 and every agent call presents it. The connection is
one-way TLS. [ADR 0027](../decisions/0027-entrance-addresses.md) names an mTLS client
certificate as the agent entrance's authentication, and nothing implements it. Enrollment is
limited per source address only, so a rack powering on is either throttled address by address
or not at all. A restore already revokes every node credential the backup held, which covers
F575's restore acceptance.

## How others solve it

| Product | Bootstrap | Identity | Rotation |
| --- | --- | --- | --- |
| [Kubernetes kubelet](https://kubernetes.io/docs/reference/access-authn-authz/kubelet-tls-bootstrapping/) | Limited-use bootstrap token, then a CSR; the key is generated on the node | Client certificate naming the node | [Requested when 30% to 10% of the lifetime remains](https://kubernetes.io/docs/tasks/tls/certificate-rotation/); one year by default; connections reconnect with the new certificate |
| [SPIRE agent](https://spiffe.io/docs/latest/deploying/spire_agent/) | Node attestation plugin | X509-SVID with [exactly one URI SAN](https://raw.githubusercontent.com/spiffe/spiffe/main/standards/X509-SVID.md) | At half the lifetime by default; re-attestation (`rebootstrap_mode`) when trust is lost |
| [RFC 8705](https://www.rfc-editor.org/rfc/rfc8705.txt) | Not applicable | Token bound to a certificate: only the holder of the private key can use a stolen token | Not applicable |

Go's `crypto/tls` gives what this needs: `VerifyClientCertIfGiven` verifies a certificate
only when one is sent, `GetClientCertificate` is called for each handshake (so a rotated
certificate needs no restart), and `VerifyPeerCertificate` is not called on resumed
connections while `VerifyConnection` is. The documentation does not say when expiry is
rechecked, so expiry is checked on every request rather than assumed.

## Licences

No new dependency. Everything uses the Go standard library (BSD-3-Clause) and works offline.

## Chosen approach

**The join token remains the bootstrap, as in Kubernetes; the certificate becomes the
identity.** The agent generates an ECDSA P-256 key, sends a CSR with the enrollment, and
control signs a client certificate with an installation-private agent CA. The key never
leaves the node. The certificate names the node in exactly one URI SAN
(`urn:ownsolid:node:<site>:<cell>:<node>`).

**The bearer credential stays and is bound to the certificate, as in RFC 8705.** A call must
carry both. A stolen token cannot be used without the key, and a stolen key cannot be used
without the token. The credential paths, the admission keys and the heartbeat cache are
unchanged, so the hot path does not grow.

**Control pins the key.** `managed_nodes` holds the digest of the current key and, during a
rotation, of the previous one. A connection's key must be one of them, so rotation is a
revocation point and not only a new certificate. The pin is part of the node view
(ADR 0047), so heartbeats still answer from memory.

**Rotation has a short overlap and an explicit end (F575).** The agent asks for a certificate
for a new key when 30% to 10% of the lifetime remains, chosen at random in that window. Both
keys work until the agent confirms from a connection using the new one, which clears the old
pin. A reply lost at either step is retried and changes nothing.

**A node that outlived its certificate recovers.** An outage longer than the lifetime must not
strand a fleet. With no certificate, the renewal path accepts the credential together with a
CSR signed by the pinned key, so a stolen token alone cannot mint one. A node from before this
release has no pin and takes its first certificate the same way.

**The CA is its own, and only control can sign.** It is separate from the serving CA, because a
site can bring its own serving certificate and then has no CA key at all. Gateways get the
certificate only. Several certificates may be listed for trust, the first being the issuer,
which is what a CA rollover needs.

**The certificate lives 30 days by default.** Long enough to ride out an outage, short enough
that a lost key ages out. Kubernetes uses a year and SPIRE an hour.

**Enrollment is throttled per rack.** The rack is the source network: a /24 for IPv4, a /64 for
IPv6, both configurable, because a leaf-spine fabric gives each rack its own subnet. It uses the
rate limit the per-address rule already uses, before any enrollment work, and the agent waits
for the hint instead of failing. FLT-101 models racks; ARC-112 keys the limit on them then.

### Rejected

- **Certificate only, no bearer.** Rewrites every credential path and the node view for no gain
  over binding the two.
- **A separate enrollment listener.** Doubles what a site has to open. `VerifyClientCertIfGiven`
  on the agent entrance lets enrollment arrive without a certificate; the handler decides.
- **Rotating the bearer with the certificate.** Every concurrent call on the old token would be
  refused as revoked at the moment of the flip, or all the credential checks must accept two.
- **CRLs or OCSP.** Control is already consulted on every call, and an offline site would have to
  host them.
- **Key on the server.** A key that travels is a key to protect in transit and in backups.
- **Conforming SVIDs.** The specification requires both server and client usage when extended key
  usage is set, and a node certificate must not be able to serve.

## Known failure modes and tests

| Failure | Handling | Test |
| --- | --- | --- |
| Reply lost at renewal | Old key is `previous`, still accepted; retry reissues | Service integration test |
| Reply lost at confirmation | Idempotent by key | Service integration test |
| Certificate expired during an outage | Renewal with credential and CSR from the pinned key | Service and agent tests |
| Token leaked, no key | CSR key not pinned: refused | Service integration test |
| Certificate of another node | Refused before any node work | Handler and gateway tests |
| Pinned node without certificate | `AGENT_CERTIFICATE_REQUIRED`; agent renews, does not exit | Handler and agent tests |
| Old key after confirmation | `AGENT_CERTIFICATE_SUPERSEDED`, connection closed, agent redials | Handler, gateway and agent tests |
| Stream outliving its certificate | Checked on every exchange | Gateway test |
| Rack stampede | 429 with a hint; the agent waits within its enrollment budget | Handler and agent tests |
| Clock skew | Agent treats a certificate as expired 5 minutes early; larger skew needs NTP | Agent test |
| TLS terminated by a proxy | Control refuses to start: agents need a socket that sees their certificate | Config test |

## What the finished item must do

1. A new agent enrolls with a CSR and receives a certificate; the key stays on the node.
2. After enrollment, that node's calls and session are refused without its certificate, with
   another node's, or with an expired or superseded one.
3. An agent rotates its certificate without restarting and without losing its session for longer
   than one reconnect, and the old key stops working when it confirms.
4. An agent whose certificate expired recovers by itself when control returns.
5. A node enrolled before this release keeps working and takes a certificate on upgrade.
6. A rack over its limit gets 429 and retries; other racks are unaffected.
7. Gateways verify certificates without holding the CA key.
8. All of it works offline, through the real installer.
