# Capability status

The single status table for ownsolid. Columns follow AGENTS.md section 4. Planned work
and its progress live in the [roadmap](roadmap/index.md); this page lists what is usable
today and what is known to fail.

| Capability | Implemented | Run end to end | Customer verified | Known failures |
| --- | --- | --- | --- | --- |
| Repository layout checks | Yes | Yes | n/a | None |

Backend code from the previous `project-one` repository now lives in this layout. The
control plane runs one complete platform with one PostgreSQL lineage and a
`topology.size` setting instead of application profiles (ADR 0003). Capabilities
are listed above once they are usable from the new interface.

## Known failing tests on macOS

| Test | Cause |
| --- | --- |
| `TestInferenceTaskPolicyExistingDeploymentMapping` | Reads a Compose file that is not migrated yet |
| `TestMySQLKnowledgeWorkflowPortalCandidateSource` | Reads a milestone document left in `project-one` |
| `taskdeliveries` contract test | The OpenAPI file disagrees with central license error codes |
| Artifact license reconcile case in `licensing/http` | Inherited, not yet analyzed |
| `license_target_test` | Needs a pinned Python |
| Identity hash admission timer test | Timing sensitive under load |
| `deploy/offline` and `tools/release/offline` packages | Fail on macOS; need Linux, a non-root user and a mounted temporary directory. See the Linux results below |
| Offline install and upgrade through `deploy/offline` | Not run since ADR 0003: no image definitions exist in this repository, so no bundle can be built. Upgrades from M16 `releases` call the removed `upgrade-releases`. Not supported until DEL-001 |

## Linux amd64 results for offline packages

Run on `ownsolid-cpu` as `nobody` with a mounted tmpfs, from test binaries built on the
workstation. Ten of twelve packages pass.

| Package | Result |
| --- | --- |
| `tools/release/offline/internal/factory` | One failure: `TestPreparedSnapshotRejectsPostreadChanges/rewrite_restored_bytes`, not analyzed |
| `deploy/offline/internal/scope` | Incomplete: hit the 20 minute limit after 330 tests. Six failures: five temporary directory cleanup errors, which also fail before ADR 0003, and `TestScopePrivateSnapshotRejectsLeafMutation`, not analyzed, which passes in a Linux arm64 container |
