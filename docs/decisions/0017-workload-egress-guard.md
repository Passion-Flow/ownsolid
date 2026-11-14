# 0017: Workload egress guard in the node's own firewall table

- Status: accepted
- Date: 2026-11-05

## Context

DEL-003 asks for real egress denial for workload containers on nodes. Training and model
preparation containers already run with `--network none`. The inference container
cannot: the gateway and the agent's readiness probe reach the model only through a port
that Docker publishes on `127.0.0.1` (the gateway accepts only a loopback backend). Until
now the container sat on an ordinary Docker bridge, so it could open connections to the
site network and the internet, and the only defence was offline flags such as
`HF_HUB_OFFLINE` inside the image. The 2026-11-04 real-GPU run found this gap.

Measured on Docker 29.1.3 (`ownsolid-cpu`, 2026-11-05):

- An `--internal` network blocks egress and external DNS, but Docker publishes no port on
  it, so the loopback contract breaks.
- Attaching both an internal and a normal network does not help: the normal network still
  provides the default route, whatever the gateway priority.
- Docker's embedded resolver forwards to a loopback resolver on the host, such as
  systemd-resolved at `127.0.0.53`, from the host's network namespace. A firewall rule
  on the bridge never sees those queries, so a container could resolve, and leak, any
  name through the host even with routed traffic blocked.

## Decision

**The agent owns one nftables table, `inet ownsolid_workload_egress`.** For every owned
inference bridge (`br-` and the first twelve characters of the network ID) it holds:

- `forward`: anything arriving from the bridge is rejected with "administratively
  prohibited", so senders fail at once; anything routed into the bridge is dropped,
  because the published port is bound to loopback and nothing legitimate is forwarded.
- `input`: replies to connections the host opened are accepted, which keeps the
  published port working; every connection the container opens to the host is rejected.

Both chains hook at priority -10, ahead of Docker's filter chains at 0. A packet dropped
by any base chain is dropped, so the guard holds under the iptables-nft, iptables-legacy
and nftables firewall backends alike. The agent talks to the kernel over netlink
(`github.com/google/nftables`), so hosts need no `nft` or `iptables` binary.

**The container's DNS upstream is `203.0.113.53`.** An explicit upstream makes the
embedded resolver dial from the container's network namespace, where the guard refuses
it. The address is in TEST-NET-3 (RFC 5737) and never belongs to a real resolver.
Ownership checks treat any other DNS setting as a foreign container.

**The guard is not optional.** `EnsureNetwork` installs it before handing out the network,
and `VerifyNetworkEndpoint`, which every start of an owned container passes through,
re-installs it if a reboot or firewall reload removed it and rebuilds the table if anyone
changed it. Every change replaces the whole table in one netlink batch, so the kernel
applies it atomically. When the rules cannot be installed or read back, the deployment
fails with `INFERENCE_EGRESS_GUARD_UNAVAILABLE` and the model does not start. Removing the
network removes its rules.

## Consequences

- The inference agent needs root with `CAP_NET_ADMIN`, the `AF_NETLINK` address family and
  the host's network namespace. It already needs root for the Docker socket.
- Inference containers created before this change carry no DNS setting; the ownership
  check refuses them, and they must be removed and deployed again.
- `make e2e-binaries` builds the agent's egress guard test, and
  `tests/e2e/run-single-node.sh` runs it on the isolated Docker host: a probe on an
  unguarded bridge reaches the egress recorder, the same probe on the guarded network,
  plus a host service and a DNS lookup, leaves nothing in the recorder log.
- The guard covers Docker nodes. Workloads on Kubernetes cells (ARC-104) need the same
  denial through network policy, which is roadmap item NET-106.
