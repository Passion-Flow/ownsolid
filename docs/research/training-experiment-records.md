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

## Production execution evidence

An [OCI image manifest](https://specs.opencontainers.org/image-spec/manifest/)
references a separate image configuration descriptor. The
[OCI image ID](https://github.com/opencontainers/image-spec/blob/v1.1.1/config.md#imageid)
identifies the configuration bytes. Training freezes both the digest-pinned
distribution reference and the local image ID in its command. The Agent
[inspects the image by ID](../../services/agent/internal/training/docker.go)
and [checks the owned container](../../services/agent/internal/training/container.go).
The protected worker result repeats the execution identity. The authenticated
positive stop binds that local image ID to the command, task, node, attempt,
container and accepted result digest. Report the distribution reference as
the frozen request and the local ID as the image bound to that stop. Control
has no independently retained registry manifest or installation attestation.

Before promoting a stopped attempt to success, bind the scoped accepted
upload and its exact original result bytes to the command, artifact inventory
and attempt outcome. Verify the same chain when reading an accepted result.
[PostgreSQL JSONB](https://www.postgresql.org/docs/18/datatype-json.html)
normalizes representation, so compare decoded typed outcomes across SQL and
the protected result. Hash only the original result bytes. Keep the existing
physical release path for revoked, cancelled and disabled work.

The [Linux training lock](../../runtimes/training/requirements/linux-amd64-cuda.lock)
is hash-pinned in the runtime source. Pip hash checking can establish what the
release builder installed. DEL must publish a verified mapping from the
selected distribution manifest and local image ID to that exact lock digest.
Control must retain the mapping with the attempt or a durable release
reference. The worker package versions or a hash of the current checkout
cannot establish the production lock identity.

The Agent's [preflight](../../services/agent/internal/training/manager.go)
probes the requested NVIDIA UUID and applies capability, driver, memory and
MIG constraints. [NVIDIA's management interface](https://docs.nvidia.com/deploy/nvidia-smi/index.html)
describes the UUID and observation fields. ARC must retain the bounded
observation in the verified attempt journal and authenticate it with the
same node, task, command, GPU UUID and attempt. The control-plane
`HardwareWithin` reads current fleet observations for admission, and the
stop only repeats the requested UUID. The experiment leaves the lock and
hardware fields explicitly unavailable until their owners supply these
bindings.

Exercise the existing claim, result upload, stop and scoped read against
disposable PostgreSQL with the retained CPU result and adapter. Refuse an
unbound image ID, foreign attempt, invented lock or hardware fields and a
CPU profile before creating an upload. Refuse inconsistent retained result
copies or a foreign stop image before successful completion. Restore the
exact result and retry. Read back both distinct image identifiers with the
unavailable fields empty. The node, production envelope and stop are fixture
inputs. This verifies the acceptance rules independently of NVIDIA. The
change adds no dependencies or new redistributable material.

`TestActualTrainingExecutionEvidenceAdmission` is included in the offline
replay above. On Linux with only loopback, it used the retained result and
adapter in the real PostgreSQL upload and stop path. It refused a registry
digest substituted for the local image ID, a foreign attempt, the native
CPU profile and fabricated lock and device fields before writing an upload.
After accepting the exact adapter, it refused the wrong stop image, a
changed attempt result and changed protected upload bytes. The exact retry
completed once, the scoped read distinguished the two image identifiers,
and a changed retained result was refused on read. No lock or hardware
observation was returned from the fixture's current node telemetry.

The replay also verified readback when the JSONB projection was larger
than the exact accepted result budget. The test fixed the original byte
budget to the size of the valid report and confirmed that PostgreSQL's
normalized projection exceeded that limit. The accepted record remained
readable because the original bytes and the typed outcomes still matched.

## Accepted experiment after a private store handoff

The existing file snapshot owns accepted immutable bytes and the store
namespace. Reuse that snapshot for a stopped accepted CPU fixture and
keep its authenticated database references. Import into a fresh private
directory, open the recovered store and read the experiment through
`ReadAcceptedExperiment`. The command, result, stop, file inventory
and accepted adapter identity must match the original source.

Verify readiness against the imported files before the read. A
same-length change to the recovered tensor file must fail with
`TRAINING_STORAGE_DATA_INVALID` and return an empty experiment.
Restoring the exact original bytes must make the read retryable.
An unknown workspace must still receive no experiment. This checks the
training file-owner handoff under the same scoped database. Database
exchange and account-authority invalidation belong to the coordinated
restore acceptance.

The fixture reuses the actual retained CPU adapter and the existing
PostgreSQL upload, stop and read path. Node facts and the production
result/stop envelope retain their declared synthetic scope. The test
adds no dependency, model or data. The existing Qwen Apache-2.0,
generated CC0 fixture and pinned runtime license conditions apply.

The disconnected Linux amd64 replay passed with PostgreSQL 18.6 and
pgvector 0.8.7. The recovered experiment and preparation identity matched
the original accepted 24-step CPU artifacts. A changed tensor returned
`TRAINING_STORAGE_DATA_INVALID` and no record; replacing the exact bytes
restored the read. An unknown workspace returned no experiment. The six
retained-input replay tests passed together, including the existing
registered-model refusal and accepted-result JSONB budget regression.
No new worker computation or physical production stop ran. One earlier
replay reported `SCHEMA_UNAVAILABLE` while initializing a neighboring
fixture; the unchanged full repeat passed. That setup failure is retained
in the private transcript, without attributing an unobserved database cause.

## Local-cell experiment and preparation source

The experiment export already uses the shared locked training job read.
The borrowed `AcceptedAdapterWithin` path independently repeats its SQL.
Reuse the common read so both authenticated experiment exports and model
preparation sources enforce the same server-owned PostgreSQL site/cell
predicate. The internal caller still owns actor and Workspace authority;
this method grants no new authorization or commit capability.

A separate adapter-only predicate could diverge from operator/worker reads.
Copying a foreign job to this cell or accepting its command's tenant/workspace
would substitute client-visible identity for installed server scope. Preserve
the original row and return the existing not-found error with no source.
Exact scope repair in the disposable fixture must restore the same accepted
experiment and preparation identity. This reuses the [local-cell owner fence](training-job-lifecycle.md#local-cell-owner-fence-before-scheduler-admission)
and [PostgreSQL transaction semantics](https://www.postgresql.org/docs/18/transaction-iso.html).
It adds no dependency, model, dataset or license term beyond those above.

Extend the real retained CPU upload/stop/read replay: change only the owned
job's site, then its cell. Require an empty experiment, empty authenticated
adapter snapshot and empty borrowed preparation source, all with
`TRAINING_NOT_FOUND`. Refused reads must append no export audit. Restore the
exact coordinates and require the original experiment to read again. Keep
node, production command and physical-stop envelopes explicitly synthetic;
this does not establish an installed Agent, UI, NVIDIA or cross-cell restore.

## Accepted-result children remain in the owner's cell

A local job is insufficient when a retained artifact, upload, attempt or
node task carries foreign site/cell coordinates. Their existing read joins
bind tenant, Workspace and exact identities, but do not yet reconcile the
cell columns introduced by migration 30. Enforce the installed local cell
on the artifact and require all four joined owners to have that same tuple
in the existing PostgreSQL metadata read. Keep the caller's locked local job,
current authority and final export recheck. No client coordinate, new ledger,
migration or production trigger change is needed.

A job-only check would leave foreign children readable. Reassigning their
coordinates would alter retained evidence. A second preflight query would
add another snapshot instead of making the protected record read coherent.
Use predicates in the same joined SELECT and retain its row locks. Existing
PostgreSQL snapshot/locking semantics and Go/pgx/PostgreSQL licenses apply;
no new dependency, model or dataset is introduced.

Using the actual retained CPU adapter, change only each owned fixture child's
site or cell while leaving the job local. The authenticated experiment read
must expose no record and report its existing invalid-data error; borrowed
and authenticated preparation snapshots must expose no source under their
existing unavailable-result contract. No refused export may append audit.
Restore the exact coordinates and require the original experiment to read
again. Also change the artifact cell between initial metadata/file
verification and the final authority read; the call must return no already
computed experiment and append no export audit. If immutable-row triggers prevent injecting the fault, disable only
the owned artifact table's immutable-row trigger around the fixture update
and re-enable it in the same transaction. Production triggers remain unchanged. These
are component acceptance rules, separate from installed Agent/UI/NVIDIA
qualification and the original CPU computation evidence.

The disconnected PostgreSQL 18.6 replay at `8495818a` passed the actual retained
CPU evidence group with both job coordinates, all eight child-coordinate
faults and the artifact change before the final read. Refused reads returned
empty records/sources and appended no export audit; exact coordinate repair
restored the original experiment. The eight neighboring groups passed in that
run. Its separate new cell-owner group initially failed in a test-only audit
UUID comparison and passed after correction at `be30d3f4`; the raw logs and
repository-gate boundary are in the [native owner acceptance](training-job-lifecycle.md#native-acceptance-boundary-for-the-local-cell-candidates).
No Agent, UI, physical GPU or new worker computation is implied.
