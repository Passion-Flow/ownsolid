# Capability status

The single status table for ownsolid. Columns follow AGENTS.md section 4. Planned work
and its progress live in the [roadmap](roadmap/index.md); this page lists what is usable
today and what is known to fail.

| Capability | Implemented | Run end to end | Customer verified | Known failures |
| --- | --- | --- | --- | --- |
| Repository layout checks | Yes | Yes | n/a | None |
| Cell and site scope on tables, API paths and shared keys | Yes, PostgreSQL only; identity fixed to `default` until ARC-202 | See the single-node install row | n/a | MySQL and Dameng chains lack the columns (CMP-201, CMP-202) |
| Agent session protocol v1: gRPC stream on HTTP/2 for heartbeats and claims, HTTPS long-poll fallback (ADR 0007) | Yes; stream gateway runs in the control process, task wake is per process until ARC-011 | Yes: a real agent enrolled into the offline single-node install, ran tasks over the stream and reconnected after a control restart, Docker-in-Docker on `ownsolid-cpu`. The fallback ran against the real handler and PostgreSQL on the Mac | n/a | None |
| Single-node install (`ownsolid-install`, control image, Compose) | Partly: no web console | API only: installed, signed in, restarted and reinstalled with no network route, Docker-in-Docker on `ownsolid-cpu` | No | No console or portal: both origins serve a placeholder page. Not complete until UX-001 (DEL-001) |
| Zero-egress end-to-end harness: every outbound packet, DNS included, recorded and refused (ADR 0008) | Yes | Yes: the single-node install run recorded no outbound attempt besides its own probes, from the host and from the control container, Docker-in-Docker on `ownsolid-cpu` | n/a | None |

Backend code from the previous `project-one` repository now lives in this layout. The
control plane runs one complete platform with one PostgreSQL lineage and a
`topology.size` setting instead of application profiles (ADR 0003). Capabilities
are listed above once they are usable from the new interface.

## Tests

`make test` passes on macOS. The factory tests need a non-root user and a real mounted
filesystem, so they skip there; `make test-offline HOST=ownsolid-cpu` runs them on Linux.
`tests/e2e/run-single-node.sh` is the end-to-end install, see
[environment](../development/environment.md). Database integration tests carry build tags
and are not part of these runs.

Database integration tests on the Mac (PostgreSQL 18.6, `go test -p 1`, with and without
`-tags repository_integration`) have known failures: 132 tests, mostly because the pinned
data-processing Python and component assets are not installed there. The ARC-004 change
was checked against that baseline and adds no failure. ARC-005 was checked the same way
on the packages it touches (nodes, identity HTTP, machine composition, agent tests).

## Not supported

| Capability | Why |
| --- | --- |
| Sizes other than `single-node` | Refused with `CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED` until their roadmap items land |
| Backup and restore of the platform | Removed with the offline operator (ADR 0005); REL-002 |
| Upgrades and signed bundles | DEL-104, DEL-101 |
