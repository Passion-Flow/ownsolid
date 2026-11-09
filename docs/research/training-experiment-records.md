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

### Agent observation and lease binding

The training manager probes the requested GPU with the existing
root-protected `nvidia-smi` path before launching Docker. The parser
requires the exact NVIDIA UUID, driver, memory and compute capability.
It preserves NVIDIA's reported `N/A` for unsupported MIG observations
and refuses missing or malformed required fields. NVIDIA documents
stable UUID selection and unsupported fields in the
[management interface](https://docs.nvidia.com/deploy/nvidia-smi/index.html#query-options).
This is a preflight observation. The container's requested UUID
and the final stop receipt do not prove that the observed properties
remained constant during execution.

The private training journal already retains the node, task, delivered
lease and validated command digest. The digest covers the attempt ID,
attempt number and requested GPU. Exact journal replay uses all those
bindings. The protected preflight currently returns an admission result,
so its observation is unavailable after that call. The history stores
the accepted journal and receipt. Neither carries the observed fields.

The v1 result and event decoders admit exact worker fields. The exact
stop receipt is also used to reconcile physical ownership after a lease
is revoked. Placing observations in these payloads would change
compatibility and could give a stop request authority it does not have.
A future owner handoff needs a versioned, capability-selected Agent
exchange with the authenticated node, current training lease, exact
command and attempt. Persist the observation before acknowledging it
and link its digest to the accepted result. Old Agents and old records
retain their original behavior. Current heartbeat telemetry, a UUID
alone and unsupported values cannot populate historical hardware.
DEL owns the image-to-lock mapping.

The local checks use the existing journal and NVIDIA parser with
synthetic inputs. Verify that an old lease, foreign node or task, and a
different attempt ID or number cannot replace a stopped journal, while
the exact delivery replays. The available CPU server has no NVIDIA
device to observe. This evidence qualifies the binding controls and
does not populate the historical experiment record. It uses the
existing dependencies above and introduces no redistributable input.

On disconnected Linux arm64, the private journal regression rejected
all five mismatches, preserved the stopped journal, and replayed the
exact delivery. The bounded NVIDIA CSV parser regression also passed
its missing, malformed and busy-device controls. Run them with:

```sh
go -C services/agent test ./internal/training -run '^TestActualPrivateJournalAndSyntheticOwnedContainerStopReplay$' -count=1
go -C services/agent test ./internal/inference -run '^TestGPUProbeNeverTreatsInvalidFieldsOrOtherProcessesAsFree$' -count=1
```
