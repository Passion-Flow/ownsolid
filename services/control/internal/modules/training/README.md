# Training orchestration

This owner authorizes jobs from immutable model and dataset versions, dispatches
node tasks and attempts, accepts events/checkpoints/artifacts and reconciles
physical stops. Python performs computation; the Agent owns local containers.
Only the control-plane owner commits training business state.

## Contracts and responsibilities

- [Training wire contract](../../../../../api/training/v1) defines commands,
  execution identities, events, sealed resumes, results, transfers and stop proofs.
- [Training HTTP contract](../../../../../api/openapi/v1/training/openapi.json)
  defines the Admin and Portal operations.
- [Python runtime](../../../../../runtimes/training/README.md) owns bounded offline
  computation and checkpoint serialization.
- [Agent training](../../../../agent/internal/training) owns exact local execution
  and positive stop observations. Its changes belong to the architecture lane.
- [Training schema](../../../migrations/postgresql/000015_training.up.sql) and later
  shared migrations own durable jobs, attempts and artifacts. Runtime grants,
  catalog fingerprints and production migration registration belong to the
  database/release owners.

[creator_repository.go](creator_repository.go) freezes authenticated inputs and
rechecks them after file verification. [authority.go](authority.go),
[dispatch.go](dispatch.go) and [reconcile.go](reconcile.go) retain requester,
Workspace, node, job and physical GPU fences. Dispatch commits the lease, task,
numbered attempt and audit together. An uncertain commit publishes no success;
retry resolves current persisted owner state.

Queued cancellation creates no attempt and clears only the active recovery
pointer, retaining history. Active cancellation and lost contact retain the
physical lease until an exact stop or proven non-delivery is accepted. A
checkpoint recovery binds the original sealed configuration, source/execution
identity and retained bytes. See the [lifecycle](../../../../../docs/research/training-job-lifecycle.md)
and [recovery](../../../../../docs/research/training-checkpoint-recovery.md) research.

PostgreSQL dispatch/reconciliation select local-site/cell jobs, and the shared
locked job read repeats that fence before mutation or accepted-source access.
Foreign retained jobs remain intact. Accepted artifacts also require their
upload, attempt and node task to share the installed PostgreSQL site/cell.
Fair queue admission still requires the
selected-executor ranking API and production scheduler DDL/grants described
in the lifecycle handoff; a standalone preflight is not a reservation.

[accepted_adapter.go](accepted_adapter.go) exposes an internal immutable model
preparation source. `AcceptedAdapterSnapshot` owns current actor/Workspace
checks; `AcceptedAdapterWithin` borrows them from its caller. Neither grants
independent authorization. File hashing and opening remain outside business
locks. The source retains the exact accepted, physically stopped attempt,
command, dataset version, result and adapter identity. Publishing a successor
or retiring the original dataset does not rewrite this provenance.

`ReadAcceptedExperiment` reconciles the protected result, stop and artifact
inventory. It distinguishes the frozen distribution reference from the local
image ID. Lock and hardware observations remain unavailable until verified
attempt bindings exist; current node telemetry cannot replace them. See the
[experiment record](../../../../../docs/research/training-experiment-records.md)
and [dataset lineage](../../../../../docs/research/training-dataset-lineage.md).

Snapshot copying uses [snapshotio](../../platform/snapshotio/README.md).
This owner retains namespace/path/size/hash checks, exclusive output and final
file/directory durability. `OpenStoreForVerification` borrows an existing
read-only ownership descriptor with an exclusive nonblocking lock; it creates
no state and fails closed when the filesystem cannot provide that operation.

## Verification

Run ordinary owner tests from the repository root:

```sh
go -C services/control test ./internal/modules/training
```

For the current PostgreSQL owner tests, configure the protected disposable
repository fixture described in the [development environment](../../../../../docs/development/environment.md#database-integration-tests).
Set `PROJECT_ONE_REPOSITORY_ENGINE`, `PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE`
and `PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA`, then run:

```sh
go -C services/control test -tags repository_integration -p 1 -count=1 \
  -run '^(TestTrainingLocalCellOwnerFence|TestActualPairedTrainingDispatch|TestActualPairedTrainingOperator)$' \
  ./internal/modules/training
```

[testdata/replay_accepted_experiment.sh](testdata/replay_accepted_experiment.sh)
runs the nine accepted-result and owner groups in its own disconnected
PostgreSQL fixture. It includes the local-cell refusal and repair cases and
requires the retained CPU
output, publication and approved model directories through its three documented
environment variables. Synthetic node/production/stop envelopes, actual CPU
computation, installed Agent/control acceptance and physical NVIDIA execution
are separate qualifications. Current outcomes and open boundaries are recorded
in [product status](../../../../../docs/product/status.md).
