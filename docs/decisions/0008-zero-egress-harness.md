# 0008: Zero-egress harness and a fixed database address

- Status: accepted
- Date: 2026-11-04

## Context

DEL-002 asks for an end-to-end harness that installs and runs ownsolid with no network
route and records every outbound attempt. The existing run used a Docker-in-Docker host
started with `--network none`. That proves nothing can connect, but an attempt fails
inside the sender with "network unreachable" and leaves no trace, so a product that
quietly tries to reach the internet or a site resolver still passes.

## Decision

**A recording sink is the only route.** The runner keeps `--network none` and adds one
TUN interface, held by `tests/e2e/cmd/egress-recorder`, as the default route for IPv4 and
IPv6. Nothing is behind it. The recorder writes each packet that would leave the host
(protocol, addresses, ports and, for DNS, the queried name) as a JSON line, and answers
with ICMP "administratively prohibited", so senders fail at once as on a site firewall
that rejects traffic. Link-local packets the kernel sends on any new interface are not
attempts and are ignored.

**The run fails on any attempt.** `TestMain` reads the log after every test and fails
the run if anything other than the harness's own probes to TEST-NET-1 (192.0.2.0/24) was
recorded. Two probes, one from the host and one from inside the control container,
prove at the start that the recorder sees and refuses traffic, so a broken recorder
cannot pass silently.

**The database has a fixed address.** The first run of the harness found 374 DNS
queries in three seconds: while PostgreSQL was stopped, Docker's embedded resolver did
not know the name `postgres` and forwarded it, with the host's search domains appended,
to the site resolvers. That leaks internal names and lets a site record such as
`postgres.<site domain>` receive the database login. The installer now gives PostgreSQL
the second address of a typed private subnet (`network.state_subnet`, default
`172.31.254.0/26`) on the internal `state` network and writes that address into the
connection strings. Containers without a fixed address draw from the upper half of the
subnet, so they cannot take it. No service resolves the database by name.

## Consequences

- `make e2e-binaries` builds `egress-recorder`; `tests/e2e/run-single-node.sh` sets up
  the interface and passes the log to the test.
- Any new outbound path, including a DNS lookup of an internal name, fails the
  end-to-end run with its destination and query name.
- Changing `network.state_subnet` on an existing installation needs `docker compose down`
  first, because Compose does not change the subnet of an existing network.
