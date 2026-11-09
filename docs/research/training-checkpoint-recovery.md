# TRN-104 checkpoint sealing and recovery

The worker must publish a checkpoint only when its complete optimizer-step
state can be recovered after interruption. The controller and agent accept
a schema 1 receipt scoped to the same job, model, data, recipe, execution
identity, and exact training configuration.

## Upstream behavior

[Transformers Trainer](https://huggingface.co/docs/transformers/trainer_recipes)
restores optimizer, scheduler and random state along with weights.
[Transformers PEFT integration](https://huggingface.co/docs/transformers/main/peft)
saves the adapter as its own weights and configuration.
[PyTorch Elastic](https://docs.pytorch.org/docs/main/distributed/elastic/train_script.html)
restarts workers and relies on a checkpoint to recover progress. Its distributed
restart model is separate from the current single-worker runtime.
[Linux fsync](https://man7.org/linux/man-pages/man2/fsync.2.html) requires syncing
the containing directory to persist a directory entry. Syncing a checkpoint
file does not make either the checkpoint directory or its parent entry durable.

## Implementation and alternatives

Keep the existing Trainer and schema 1 receipt. Before reporting a checkpoint,
require exactly the supported flat file layout and nonempty training state.
Check the wire and configured resource ceilings before hashing. Hash files
incrementally. Check the exact trainer step using bounded, strict JSON parsing.
Sync checkpoint files and the directory hierarchy, then publish the receipt
with exclusive linking and sync its directory. Keep the original receipt when
a competing publication already exists.

Selecting the highest checkpoint directory name can select a partially written
checkpoint. A model-only save loses optimizer and RNG state. Changing the
trainer or adding a second resume format would require new product readers.
The recovery path continues to verify the authenticated receipt digest, the
file hashes and exact bindings before Trainer loads serialized state.

The implementation adds no dependency, model or dataset. The filesystem and
hashing code use CPython's standard library under the
[PSF License](https://docs.python.org/3/license.html), which allows commercial
use, offline use and redistribution with its licence and notice. Fixtures are
synthetic files. The existing runtime lock and bundled licences remain in force.

## Verification

Run the CPU unit tests inside a network-disconnected Linux container. Cover
successful sealing and verification, missing or empty optimizer state, invalid
or oversized trainer metadata, extra and nested files, links, size/file count
limits, changed data, incompatible bindings and duplicate receipt publication.
Inject a failed receipt write and verify temporary-file cleanup and retry.

Keep the roadmap item open until a stopped real training attempt resumes the
same job from an accepted checkpoint through the product, verifies the
optimizer step and produces accepted output. A worker receipt demonstrates
local durability on the tested filesystem. Surviving node loss requires the
Agent and control plane to have accepted a copy before the interruption.

## Controller recovery replay

On Linux amd64, PostgreSQL 18.6 in a loopback-only container ran
`TestActualCpuAcceptedCheckpointRecoveryFreezesNewAttemptAndPrivateRanges`.
The test used the retained CPU worker's eight checkpoint files, including
the 4,679,051-byte optimizer state with SHA-256
`9af4c306f041bb3ca4ef1de52eb55c0aafc8117a103080cae58c50dcdd308c9f`.
The original CPU receipt had SHA-256
`77321f4a896732daef90fd156942321727c6ab44526676ad7590e721877c9fb6`.
The fixture retains the worker's file digests and supplies synthetic
production request bindings and an authenticated synthetic physical stop.

A same-length change to the optimizer state blocked readiness and the
recovery request without creating an intent. Restoring the original bytes
allowed the same idempotency key to queue the same job. Attempt 2 bound
the accepted checkpoint at step 1 and served an optimizer byte range that
matched the worker output. The previous lease could not fetch the range
or stop the new attempt. Revoking the current actor blocked the new
lease's fetch.

Run the named test from `services/control` with
`-tags training_artifact_integration` and an isolated PostgreSQL fixture
with the vector extension. Set `PROJECT_ONE_TEST_DSN_FILE` and
`PROJECT_ONE_DATA_PYTHON` for the fixture, and
`PROJECT_ONE_ACTUAL_TRAINING_OUTPUT` and
`PROJECT_ONE_APPROVED_MODEL_DIRECTORY` to the retained worker output and
approved base configuration directories. This replay proves the control
handoff and storage checks. A worker restoring optimizer state and
advancing a real interrupted run remains a separate acceptance gate.

## Controller-to-worker verification

The [pinned Trainer source](https://github.com/huggingface/transformers/blob/v5.19.0/src/transformers/trainer.py)
restores the optimizer and scheduler in its resume path and consumes
trainer progress and RNG state. The ownsolid worker first verifies the
checkpoint seal and the expected execution and data bindings. Run that
verification against a directory assembled only from the accepted
attempt's machine transfer responses. Derive the expected bindings from
the newly claimed command. Keep the same actual CPU-produced adapter,
optimizer, scheduler and RNG bytes as the controller replay, with that
fixture's explicit synthetic production envelope.

Check every delivered range digest and the complete file digest against
the frozen inventory. The worker must accept the sealed step and refuse a
same-size alteration to the delivered optimizer file. This uses the
existing Python verifier and standard libraries. No model, data,
dependency or storage format changes. Directly copying a retained
directory would bypass the controller's authenticated transfer. Reading
expected bindings from the receipt would allow the file to supply its own
authority. A checkpoint at the one-step fixture's final step can exercise
this handoff without loading the model or advancing another optimizer step.

The focused test passed with PostgreSQL 18.6 and CPython 3.12.3 in a
loopback-only Linux namespace. The claimed attempt delivered all nine
files, including the receipt. The worker checked the full inventory and
returned step 1 from both the seal and Trainer metadata. A one-byte
change to the private optimizer copy returned
`TRAINING_CHECKPOINT_CHANGED`. The existing controller test still
checks stale-lease refusal and actor revocation. Continue using the
same test name, build tag and environment documented above. The
NVIDIA acceptance requires the pinned production runtime, an accepted
non-final checkpoint and a resumed attempt that advances and produces
accepted output.

## Non-final CPU replay admission

The retained native trainer follows the declared macOS arm64 profile and
uses the exact CPython 3.12.15 environment and
`native-macos-arm64.lock`. Its executable is Mach-O arm64. The Linux
test server has CPython 3.12.3 without the four training distributions.
The retained Linux tokenizer environment and the CPU serving image
qualify separate profiles. The [official PyTorch CPU index](https://download.pytorch.org/whl/cpu/torch/)
provides Linux amd64 CPython 3.12 Torch 2.14.1+cpu. That distribution's
exact version differs from `2.14.1` in `runtime.json`, and its hash is
absent from the training lock. The pinned PyPI Linux Torch distribution
has a CUDA dependency set. Reusing an approved trainer on this server
requires a complete admitted Linux training runtime for the CPU
verification path with a matching interpreter and dependency lock.

The retained one-step checkpoint is at its configured maximum of one
step. Both retained support-routing checkpoints are at their configured
maximum of 24 steps. Raising `maximum_steps` would change the sealed
configuration and scheduler horizon. Verify that the worker refuses a
request with a different horizon even when the request and its own
configuration digest agree. This verification needs only the standard
library and the existing checkpoint verifier. No dependency, model or
dataset is added. The fixture uses the existing approved Apache-2.0 Qwen
snapshot and generated CC0 training rows. The existing runtime lock
continues to govern the actual trainer.

For the two-step rehearsal, use an admitted offline CPU trainer and
the existing smallest approved dataset and model fixture. Start the
uninterrupted worker with `maximum_steps=2` and `checkpoint_steps=1`.
Keep the step-1 receipt from that run. Start a new attempt of the same
job with that exact seal under `inputs/checkpoint` and retain the same
model, seed, partition bytes, manifest, recipe, interpreter and lock.
The uninterrupted run is also the source of the non-final checkpoint.
This removes any dependence on timing a signal between optimizer
steps. Both workers run sequentially with kernel network denial and
the existing parent RSS, wall and output ceilings.

Require the resumed result to bind the parent receipt and report
`resume_from_step=1` and `global_step=2`. Compare the step-2 metric
events, exported adapter, optimizer, scheduler and RNG state with the
uninterrupted result. Verify both seals before inspecting state. Report
container or archive metadata differences separately from tensor
values. Compare result and event identities to their respective
requests because the attempts differ. Keep the original receipts,
files, events and logs if the experiment fails. The production NVIDIA
run has its own image, accelerator, stop and admission evidence.

The official Linux CPU candidate wheel advertises SHA-256
`5a6363570c753812540a05eb82380e329469cbe668643e88111414c12627711f`.
Its original core metadata matched
`a3fd232f82dcc389c6dab2665dfc17649c03ffb72cb641e96a76a0690177b918`
and states `Version: 2.14.1+cpu`. The worker compares the installed
distribution version exactly. The approved native lock remains
`03d3fab0f311aa4837190ab2e90e30a9c16c51c098fc6e6c41626630c6aa0175`.

On Linux, the disconnected verifier checked the complete retained
24-step CPU checkpoint under its original bindings. The retained seal
SHA-256 is
`1606e49e85ca773891130cf3fbad3237ce603299d7a20e5436fffe8f95470495`.
The original 9,266,571-byte optimizer file had SHA-256
`12aee1107dcc4fb67df33a0cc1b9aa2a29e27a837eebaccb133e9abe63cf34dc`.
A fresh, valid 48-step configuration with the same checkpoint cadence
and its own recomputed digest returned
`TRAINING_CHECKPOINT_INCOMPATIBLE`. The focused regression in
`runtimes/training/tests/test_checkpoints.py` passed with all 20
disconnected training runtime tests. `make check` and `make build`
also passed with external networking disabled.

### Retained CPU baselines

The disconnected Linux verifier also compared the two existing 24-step
support-routing runs. Each reported `resume_from_step=0`. Their original
bindings matched on the model, configuration, seed, partition hashes,
recipe, worker identity and package versions. Their job and publication
identities differed. The original and authorized-export manifest hashes
were `6a69e524b2fb600abfe7dff3287774096525f2a85cb5907d1ad55d6fa68b6125`
and `134bedd3dd44e53bf1ed10f1eae0ac5638ebb3395218cc586604b8eb695a936d`.
The verifier derived each expected seal from its own request, checked
each original checkpoint and result inventory, and refused the valid
48-step extension against both seals.

| Matching step-24 state | SHA-256 |
| --- | --- |
| Adapter tensors | `a46897f648b94c94a3fe55dff81c15a082e1a427126f958e3075a8feee2bf06b` |
| Optimizer state | `12aee1107dcc4fb67df33a0cc1b9aa2a29e27a837eebaccb133e9abe63cf34dc` |
| Scheduler state | `9957c822345dc1c7815e98abd277a4db02b912d5988fa2398f19020ea9bab22c` |
| Random state | `e3a77d4a8b98ce027a4d6a3b9fb5d7c904e27ec1efd5c0468c24fa26bb738316` |
| Trainer metadata | `dff5fb095d4f0609ba291cb0056a8fc763df020c56580227bfc92d16bada73cd` |

The 25 metric events, final loss and the raw exported tensor file
matched. The exported `adapter_config.json` differed only in the order
of `target_modules`. Both declared `q_proj` and `v_proj`, and every
other parsed field matched. The pinned PEFT version
[stores module names as a set](https://github.com/huggingface/peft/blob/v0.21.2/src/peft/tuners/lora/config.py#L1014-L1016)
and [writes sets as lists](https://github.com/huggingface/peft/blob/v0.21.2/src/peft/config.py#L149-L153).
Compare that field as an unordered collection while retaining both
original export digests. The original config hashes were
`8a68945fb5d71cf20d15bdaeeb32e33f99f095dcd8b2c65277c75291e0c0eb2f`
and `4ed67247993152de870045c4551f54de48a5fb0b65d61118bf95acb2c15dfe67`.

The original requests, results, events, seals and exports are retained
with the private comparison. Its SHA-256 is
`354fa59372f9d8834e2f82f2d99e2983257c5a3c32719a61c36e0c633cdb09a9`.
The export comparison SHA-256 is
`fe88b399091ff6e1706931f4e2b79e424b28dcaf85db3e0605343ed3f8dcbe39`.
Use the two-step rehearsal above to establish continuity within one
job and one publication when the admitted runtime is available.
