# TRN-105 accepted training experiment records

[MLflow tracking](https://mlflow.org/docs/latest/ml/tracking/tracking-api/)
records input datasets, parameters and artifacts against the run that
used them. [PyTorch reproducibility](https://docs.pytorch.org/docs/2.15/notes/randomness.html)
describes why a seed alone cannot reproduce work across releases and
devices. [OCI descriptors](https://specs.opencontainers.org/image-spec/descriptor/)
bind image content by digest, and [pip hash checking](https://pip.pypa.io/en/stable/topics/secure-installs/)
verifies dependency inputs before installation. Preserve these identities
separately.

Reuse the selected SQL training job, exact attempt command, stopped
outcome, accepted report and immutable adapter store. Project the
historical model catalogue, dataset version and partition inventory,
typed configuration including seed, execution recipe and pinned runtime
versions, selected node and accelerator IDs, command, result and
artifact digests. The recipe distinguishes the registry image reference
from the locally verified image ID. Check the publication and its files
outside SQL locks. Recheck current session, workspace and accepted
identity before returning the projection, using the existing adapter
export audit. Keep the current HTTP export schema and adapter bytes
compatible.

The record covers successful accepted attempts. The job's declared GPU
UUID does not establish observed hardware properties. Today no trusted
release-to-attempt binding supplies the dependency lock digest, and
current fleet telemetry cannot reconstruct the attempt's hardware.
DEL must bind the installed image identity to the exact lock, and the
training and Agent owners must retain the admitted or observed hardware
under the attempt identity. Old rows retain the narrower evidence they
actually recorded. S-502 can expose the projection after the public
contract is agreed. Keep the item open for those gates and for production
NVIDIA execution.

A parallel tracking database would duplicate workspace authority and
retention. A worker-only report would leave the accepted SQL and file
identity unchecked. Computing old lock hashes from today's checkout or
looking up the latest node observation would silently attribute mutable
evidence to a historical run.

This work adds no dependency. Existing Go standard library (BSD-3-Clause),
pgx (MIT) and PostgreSQL (PostgreSQL Licence) allow commercial and offline
use and redistribution with their notices. The prior CPU fixture uses
the existing locked Python (PSF), PyTorch (BSD-3-Clause), Transformers,
PEFT, Accelerate and Qwen3-0.6B (Apache-2.0). Keep their existing
redistribution notices. The fixture's rows were generated for the
[CPU qualification](training-dataset-lineage.md#qualified-cpu-result-and-authorized-dataset-publication),
with no external dataset.

Verify the original CPU request and protected result against the exact
published manifest and both partitions, model catalogue and base
configuration, pinned lock, hyperparameters, seed and adapter digests.
A replay of those retained files tests evidence readback independently
of new training. Exercise the authorized service read in disposable
PostgreSQL with real retained adapter files and an explicitly synthetic
production envelope. Missing or changed files, a conflicting result,
revoked authority and an uncertain commit must return no record. CPU
evidence and the synthetic envelope do not qualify NVIDIA, hardware
observations or the production image.

## Offline replay

On Linux amd64 with only loopback, the existing real CPU adapter
verification passed against the original Qwen catalogue and base
configuration. The recorded native run used seed 42, 24 steps, rank 8,
96 tokens and learning rate 0.0005. The digest of its protected
result is `42f0790680294637f8248a37a405cec920ee6d1d6c3b678248946b769781796f`.
Its published dataset manifest and both partitions match the digests
in the original request and result. The native lock hashes to
`03d3fab0f311aa4837190ab2e90e30a9c16c51c098fc6e6c41626630c6aa0175`.
The earlier CPU run established the locked environment and model files.
This replay checks the retained request, result, base configuration,
partition files and adapter without performing training.

The same replay used network-none PostgreSQL with pgvector and checked
the accepted result, stop and partition inventory in the selected
repository. It verified real adapter files staged through the training
store, exact scoped readback, another workspace, missing and corrupted
content, repair, session revocation between file verification and response,
and an uncertain final audit commit. The fixture substitutes a production
command and stop around the retained CPU bytes. The original CPU report
is verified separately.

Provide the existing protected CPU output directory, including its adjacent
`request.json`, as `PROJECT_ONE_ACTUAL_TRAINING_OUTPUT`. Set
`PROJECT_ONE_ACTUAL_DATASET_DIRECTORY` to its exact authorized publication
export, and `PROJECT_ONE_APPROVED_MODEL_DIRECTORY` to the directory with
the verified Qwen base `config.json`. With the cached pgvector image and
Go modules, run from the repository root on Linux:

```sh
bash services/control/internal/modules/training/testdata/replay_accepted_experiment.sh
```
