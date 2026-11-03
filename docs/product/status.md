# Capability status

The single status table for ownsolid. Columns follow AGENTS.md section 4.

| Capability | Implemented | Run end to end | Customer verified | Known failures |
| --- | --- | --- | --- | --- |
| Repository layout checks | Yes | Yes | n/a | None |

Backend code from the previous `project-one` repository now lives in this layout with
behavior unchanged. Capabilities are listed above once they are usable from the new
interface.

## Known failing tests on macOS

| Test | Cause |
| --- | --- |
| `TestInferenceTaskPolicyExistingDeploymentMapping` | Reads a Compose file that is not migrated yet |
| `TestMySQLKnowledgeWorkflowPortalCandidateSource` | Reads a milestone document left in `project-one` |
| `taskdeliveries` contract test | The OpenAPI file disagrees with central license error codes |
| Artifact license reconcile case in `licensing/http` | Inherited, not yet analyzed |
| `license_target_test` | Needs a pinned Python |
| Identity hash admission timer test | Timing sensitive under load |
| `deploy/offline` and `tools/release/offline` engine packages | Need a Linux Docker host |
