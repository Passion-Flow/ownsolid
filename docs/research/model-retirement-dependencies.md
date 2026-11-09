# Model retirement dependencies

[MLflow Model Registry](https://mlflow.org/docs/latest/ml/model-registry/workflow/)
treats deleting a version as irreversible and exposes aliases as references to
registered versions. [Kubernetes finalizers](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/)
demonstrate dependency protection for an asset that a workload still uses.
ownsolid keeps the model files for recovery and removes an eligible version
from active inventory and machine admission. A model version in use cannot
be retired by an inventory-only change.

Keep the current shared workspace write fence and verify the model again in
the transaction that creates the deployment. Retirement checks references
under that fence before updating the model row and writing its audit event.
[PostgreSQL row locks](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
make both orderings testable. Checking only at initial placement leaves a
window for retirement to commit before the deployment is recorded. Relying
on the model foreign key would also admit a logically retired row because
its identifier remains valid for recovery.

The existing repository tests cover both orderings using synthetic SQL
writers. Exercise the real deployment creation and model retirement methods
against one PostgreSQL installation. Use an advisory lock in a test-only
trigger to hold the first writer inside its actual transaction, then observe
the second writer waiting in PostgreSQL before releasing the first. A
committed deployment must prevent retirement. A committed retirement must
prevent deployment creation and leave no GPU lease or inference task. A
confirmed deployment removal must allow retirement to be retried. Use
synthetic GPU observations and model metadata. No model weights or new
dependencies are needed. The existing [Go](https://go.dev/LICENSE),
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE), and
[PostgreSQL](https://www.postgresql.org/about/licence/) licenses permit
commercial and offline use and redistribution with their required notices.

This tests the deployment edge of MOD-107. Shared application and key
references and the public dependency explanation remain with their owners.

## Disconnected PostgreSQL acceptance

`TestActualDeploymentCreationAndModelRetirementSerialize` passed both commit
orders against PostgreSQL 18.6 in an isolated Linux arm64 container with no
network. When creation committed first, one deployment, its GPU lease and
its inference task survived, and retirement wrote no audit event. The
synthetic node then reported a confirmed stop and removal, and retirement
succeeded. When retirement committed first, creation returned
`RESOURCE_NOT_FOUND` and created no deployment, lease or task. Retirement
wrote exactly one audit event. PostgreSQL observed both concurrent lock
waits. No real GPU ran.

Run with the [isolated PostgreSQL test fixture](../../services/control/internal/testing/identityfixture/README.md).

```sh
go -C services/control test ./internal/modules/deployments -run '^TestActualDeploymentCreationAndModelRetirementSerialize$' -count=1 -v
```
