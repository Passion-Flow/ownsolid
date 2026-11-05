# Capability status

The single status table for ownsolid. Columns follow AGENTS.md section 4. Planned work
and its progress live in the [roadmap](roadmap/index.md); this page lists what is usable
today and what is known to fail.

| Capability | Implemented | Run end to end | Customer verified | Known failures |
| --- | --- | --- | --- | --- |
| Repository layout checks | Yes | Yes | n/a | None |
| Cell and site scope on tables, API paths and shared keys | Yes, PostgreSQL only; identity fixed to `default` until ARC-202 | See the single-node install row | n/a | MySQL and Dameng chains lack the columns (CMP-201, CMP-202) |
| Agent session protocol v1: gRPC stream on HTTP/2 for heartbeats and claims, HTTPS long-poll fallback (ADR 0007) | Yes; stream gateway runs in the control process, task wake is per process until ARC-011 | Yes: a real agent enrolled into the offline single-node install, ran tasks over the stream and reconnected after a control restart, Docker-in-Docker on `ownsolid-cpu`. The fallback ran against the real handler and PostgreSQL on the Mac | n/a | None |
| Single-node install (`ownsolid-install`, control image, Compose) | Yes, with the web console and portal from `web/` | Installed, signed in, restarted and reinstalled with no network route, Docker-in-Docker on `ownsolid-cpu`; the image's console and portal load with every asset | No | None |
| Web console and portal: sign-in, sign-out, password reset, registration by invitation, single sign-on, session, failure states, frame with navigation, workspace switcher, command menu, notifications and user menu, email delivery status with resend of failed emails, light and dark themes and density that follow the account across browsers, bundled fonts, English and Simplified Chinese; pages loaded with their route under a bundle budget the build enforces; WCAG 2.2 AA checked by axe-core on every page state in both themes; every control reachable by keyboard with visible focus, focus moved to each new page's heading, and each page named in the browser tab | Yes; other pages land with their roadmap items | Real Chromium signed in and out of both entrances of an installation on `ownsolid-cpu`, through its TLS, cookies, CSRF checks and Content-Security-Policy | No | None |
| Telemetry stores (VictoriaMetrics, VictoriaLogs; ADR 0009, 0012): both installed, control pushes process metrics, component logs live in the log store, no telemetry table in PostgreSQL | Yes | Offline single-node install on `ownsolid-cpu`: stores installed, control's series read back from the metrics store, the log store reached over the internal network, platform keeps serving with both stopped, `apply` repairs them. Settled component tasks through the queue to the real log store and back on real PostgreSQL, including replacement and removal. A real agent reading the systemd journal into the store has not run: the installer does not deliver the node exporter archive until FLT-109 | n/a | None |
| Backup and restore of a single-node installation (`ownsolid-install backup`, `restore`; ADR 0014): verified backup while serving, restore with checksum comparison, swap and rollback, revocation of restored authority, rebuild of a lost installation | Yes | Offline single-node install on `ownsolid-cpu`: backup while serving, restore over a running installation that undid a later session and kept node tasks, a damaged backup refused with serving untouched, and a lost `install_root` rebuilt from the backup with the original administrator credential. Telemetry and model stores are not backed up (OBS-113, REL-103) | n/a | None |
| Zero-egress end-to-end harness: every outbound packet, DNS included, recorded and refused (ADR 0008) | Yes | Yes: the single-node install run recorded no outbound attempt besides its own probes, from the host and from the control container, Docker-in-Docker on `ownsolid-cpu` | n/a | None |
| Workload egress denial on nodes: model containers cannot open any connection, routed, to a host service or through DNS, while the loopback port still serves (ADR 0017) | Yes, Docker nodes; Kubernetes cells in NET-106 | Probe containers on `ownsolid-cpu` (Docker 29.1.3, iptables-nft): on an unguarded bridge they reach a host service, on the guarded inference network connections are refused at once and the lookup fails, and the result is read through the published loopback port. Behind the egress recorder in Docker-in-Docker, the unguarded probe's packet is recorded and the guarded network leaves no trace. Removal, rebuild after deletion, a foreign rule and a dormant table were repaired by the agent. Not yet run with a real vLLM container on a GPU node | n/a | None |
| Task delivery semantics: per-node sequences, acknowledgement before execution, release of undelivered leases without spending an attempt, cursor-based resync limited per node and per fleet (ADR 0011) | Yes, PostgreSQL only | Yes: a real agent in the offline single-node install acknowledged every task, kept its cursor, and resynced after its cursor file was deleted, Docker-in-Docker on `ownsolid-cpu`. Release, redelivery, delivery bound, resync rate limits, the race between an acknowledgement and a redelivery, and the upgrade of existing tasks ran against PostgreSQL 18.6 on the Mac, including training and model preparation leases and their stop-authority triggers | n/a | MySQL and Dameng chains lack the columns (CMP-201, CMP-202). Model preparation ran with synthetic command envelopes; the scenarios that claim through the full preparation service need a retained CPU training output and have not run |
| Agent pacing: heartbeat interval, jitter and reconnect backoff changed at runtime, full-jitter backoff, one retry layer (ADR 0013) | Yes, PostgreSQL only; API without a console page (FLT-113) | Yes: in the offline single-node install on `ownsolid-cpu` an administrator slowed a running agent through the API, the agent adopted the pacing from its next heartbeat reply and kept running tasks, then the default was restored. Pacing bounds, the online-window refusal, version conflicts under concurrent writers, permission denial, audit and convergence across control processes ran against PostgreSQL 18.6 on the Mac | n/a | MySQL and Dameng serve the default pacing and refuse a change (CMP-201, CMP-202). Retry hints are covered by the agent call admission row |
| Agent call admission: every agent call classified and admitted in memory before database work, per-node limits for each call, a per-address limit, priority seats (quarantine above heartbeat above telemetry above bulk) separate from the console's request slots, retry hints honored by agents (ADR 0018) | Yes; limits apply per control process; settings through `control_settings` without a console page (FLT-113) | Yes: in the offline single-node install on `ownsolid-cpu` a heartbeat limit of two a minute was applied with `apply`, control refused every other heartbeat with `RATE_LIMITED` and its retry hint, the agent's refusals were at least the hint apart, a task still ran, and the default limits refused nothing after the next `apply`; the admission counters reached the metrics store. Priority dispatch, caps, queue timeouts, cancellation and seat accounting under concurrency ran as unit tests with the race detector; the stream, HTTPS and proxy address paths against PostgreSQL 18.6 on the Mac | n/a | Not load tested at fleet scale (ARC-015) |
| Agent call cost rule: every call declares its server cost, a test measures every call on PostgreSQL on a small and a large fleet and fails on growth, a missing index or an exclusive lock on a row the fleet shares; agent calls lock only their own node and enrollment reads a counter (ADR 0020) | Yes, PostgreSQL only; node and training paths measured, the other operations in ARC-108 | Yes: 16 exchanges of all ten calls over real HTTPS on 50 and 3,000 nodes; with either fix undone the test fails. Load at fleet scale is ARC-015 | No | None |

Backend code from the previous `project-one` repository now lives in this layout. The
control plane runs one complete platform with one PostgreSQL lineage and a
`topology.size` setting instead of application profiles (ADR 0003). Capabilities
are listed above once they are usable from the new interface.

## Tests

`make test` passes on macOS and includes the web workspace: Vitest for every package and
Playwright for the console and portal against a scripted control plane. The factory tests need a non-root user and a real mounted
filesystem, so they skip there; `make test-offline HOST=ownsolid-cpu` runs them on Linux.
`tests/e2e/run-single-node.sh` is the end-to-end install, see
[environment](../development/environment.md). Database integration tests carry build tags
and are not part of these runs.

Database integration tests on the Mac (PostgreSQL 18.6, `go test -p 1`, with and without
`-tags repository_integration`) have known failures: 132 tests, mostly because the pinned
data-processing Python and component assets are not installed there. The ARC-004 change
was checked against that baseline and adds no failure. ARC-005 was checked the same way
on the packages it touches (nodes, identity HTTP, machine composition, agent tests).

`deploy/images/control/assets/index.html`, the old placeholder page, is unused and awaits
removal by the owner: no image build copies it.

The inherited browser suite in `services/control/internal/testing/browser` cannot run: its
`tests/acceptance` scripts did not move to this repository (UX-117). It skips unless
`PROJECT_ONE_BROWSER=1`, and it is not part of these runs.

## Not supported

| Capability | Why |
| --- | --- |
| Sizes other than `single-node` | Refused with `CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED` until their roadmap items land |
| Upgrades and signed bundles | DEL-104, DEL-101 |
