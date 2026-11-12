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

## Real interrupted Linux CPU worker resume

The [complete CPU prototype inputs](training-runtime-delivery.md#reproducible-cpu-verification-prototype)
now provide an exact Linux amd64 Python 3.12.15 environment with all 37
hash-pinned wheels, original notices and actual imports checked offline.
The independent prototype uses Torch 2.14.1+cpu and an explicitly separate
private runtime manifest. Production CUDA/macOS manifests, worker Python
sources, controller contracts and Agent code remain unchanged. No release
signature or hardware observation is fabricated.

The real worker ran the approved Qwen3-0.6B snapshot and the retained CC0
publication with seed 42, rank 8, alpha 16, dropout 0, 96 tokens, batch 1,
learning rate 0.0005, two optimizer steps and a checkpoint every step.
All attempts retain the same logical tenant/workspace/job, exact data,
configuration, package, runtime and worker identities. A fresh private
attempt directory and a fresh network-none container isolate each worker.
The original 6 GiB memory bound, two CPU threads and shared-host admission
thresholds remain enforced.

| Actual attempt | Observed outcome | Wall seconds | Peak process RSS bytes |
| --- | --- | ---: | ---: |
| Uninterrupted baseline | Trained at step 2; `resume_from_step=0` | 67.89 | 4,993,789,952 |
| Interrupted worker | Published and sealed step 1, then deliberately killed; exit 137 and no OOM | 30.23 | 4,993,232,896 |
| Fresh resumed worker | Trained at step 2; `resume_from_step=1` | 16.97 | 4,993,544,192 |

The parent observed the step-one checkpoint publication event and matched its
original receipt digest before killing only the owned container. That
worker produced no final result. Its receipt SHA-256 is
`49c8ff1ab797aa70681cb8fe0f34c97d906f0c275c554b54ecefa44bc2a4ac1c`.
The new worker received a complete private copy, verified the same seal and
two-step horizon, restored optimizer/scheduler/RNG/trainer state and executed
the remaining step. The original receipt, bindings and two-step horizon
remained unchanged.

An independent reader rebuilt the expected bindings from each actual
request, the approved catalogue and installed runtime, verified the complete
seals before deserialization and compared every tensor/state field. The
interrupted step-one algorithmic state also matches baseline step one.
The final five algorithmic files match baseline step two byte for byte:

| Final checkpoint file | SHA-256 in both baseline and resumed attempt |
| --- | --- |
| `adapter_model.safetensors` | `6e480ed88d3b33ee96bcb25c9813dcffff694f9f5e8b1f08d7db45af97d80937` |
| `optimizer.pt` | `8b679b2cbc5b7bab8dbd344526ae9d65185b9a03a3cd2cd4611dfed0d9ee31e4` |
| `scheduler.pt` | `e2799a306b5c89411f3f874599f534052e29f852c3305f26e4a28a712f0d15c1` |
| `rng_state.pth` | `c1971a7f5a4b2dbf37c7eaedcc9ddd90b7f2853c5557f35719c1a2082903b821` |
| `trainer_state.json` | `7b8783498b1804460f3d82f93d6ffa873920c4bb4b0d4d9332a427e88a6255d6` |

The separately exported adapter tensors also match. The structured and raw
comparison report hashes to
`3ef77a0c7a579bca6dda3fdcc33501da1180d38de4f197313d669ed04d78f05f`.
The original reports, requests, events, checkpoints and observations remain
in the private prototype workspace, alongside the failed launch and all
resource refusals. No raw result or state file was normalized or replaced.

### Explicit differences and refusal checks

The complete checkpoints are not byte-identical. `training_args.bin`
contains each attempt's distinct output directory. A small disconnected
reader checked the original receipt and file digests, then inspected pickle
opcodes without importing Torch or executing pickle. The sole differing
argument string is `output_dir`; every other opcode is identical. The ZIP
serialization identifier changes with that payload. The checkpoint receipt
has exactly one changed inventory entry, the `training_args.bin` digest.
The two files are both 5,265 bytes. Their original hashes are
`dd442afcb1e68ab5d168760bbb417bb0991ecafd3137fbc937a61f48b8d7d64d`
and `3c71338c0c8f180ca3e52ddf870f743d40fbdf45c0b393ccc2c620eda86bdad3`.
The difference report hashes to
`0af71974d76ec408229adba26029de5d9b0300d71645474d636dd86e74afc490`.

The protected `training_loss` values also have distinct coverage. Baseline
step losses are 0.9951430559158325 and 5.267714500427246, with a two-step mean
of 3.1314287781715393. The resumed worker reports only its new step's loss,
5.267714500427246. That step exactly matches baseline step two. The pinned
Transformers 5.19.0 `Trainer` records `_initial_global_step` and divides its
current invocation's accumulated loss by
`global_step - _initial_global_step`. Keep the per-invocation summary distinct
from full-run step history when comparing resumed runs; a seed alone is not
an equivalence claim.

Two additional real worker invocations copied the original sealed step-one
checkpoint. An equal-length optimizer-byte change returned
`TRAINING_CHECKPOINT_CHANGED`; changing only the otherwise valid horizon
from two to four steps returned `TRAINING_CHECKPOINT_INCOMPATIBLE`. Neither
invocation emitted the model-loading phase or performed a training step.
The original checkpoint remained intact and was not resealed for either case.

This verifies actual local CPU computation and restart under the exact
prototype inputs. It does not establish installed Agent/control acceptance,
the user-facing recovery flow, signed-media admission, another filesystem
or survival of node loss before checkpoint transfer. Production NVIDIA
execution remains a separate hardware gate. Keep TRN-104 open for its full
product path and retain this narrower actual-run evidence.

### Reproduction inputs and preserved evidence

The private `cpu-prototype-source-evidence-v2.tar.gz` archive in the retained
CPU audit root has SHA-256
`b83aa744cc40e0121f70ac197f5ab586e652da91479007166c6aced9906e908e`.
A disconnected reader verified every one of its 579 payloads against the
inner size/hash inventory without extracting it. It contains the exact
worker and harness sources, separate runtime manifest, full hash locks,
original notices, package/base identities, CC0 sample publication and
unmodified request/result/receipt/observation metadata. Original algorithmic
binaries remain in the preserved workspace. The 37-wheel/pip directory,
exact base-image archive and nine-file approved model are separate required
binary inputs; none is replaced by this small metadata archive.

Extract into a fresh private root with `umask 077`, preserving source files
0600 and directories 0700. `reproduce.md` records the complete offline
materialization and run commands. `portable_cpu_inputs.py` verifies the
inventory digest, complete wheel/notice payloads and both locks without a
live index. `install_cpu_prototype.sh` creates a fresh environment from only
those inputs. `execute_cpu_prototype.sh` runs the paired workers and both
negative cases; after a resource-admission refusal its explicit starting-case
argument resumes the remaining checks without replacing a successful run.
All heavy stages require both existing shared Linux locks and the unchanged
3 GiB free/12 GiB available memory, 15 GiB disk and at-most-5-percent full
I/O avg10/avg60 thresholds. Every created work container was removed.

### Repository gate admission boundary

The CPU prototype above is complete under its stated inputs. Its candidate
was rebased onto `1aad4eed`, which includes the joint audit schema 58 change.
Fresh repository gates and the retained-input PostgreSQL experiment replay
were then requested with both existing Linux locks. Admission refused them
before execution because free memory remained below the unchanged 3 GiB
threshold. Twelve gate preflights observed between
707,489,792 and 914,714,624 free bytes;
no `make check`, `make build` or module test started in that series.
The complete admission log has SHA-256 `b474335818547187d1f6d739d5aa72f18464fcd2ab0c4b5dae9b7074b9dede9c`.
Earlier repository results do not qualify this rebased candidate.

The candidate remains local and its roadmap items remain unticked. Resume
repository verification only after the original resource thresholds pass:
run `run-cpu-admission-gates.sh` with the private proof root and `all`, then
`run-experiment-handoff.sh` under the same two locks. Preserve original logs
and run only unfinished phases after an admission refusal. The actual CPU
worker sources and their five-case computation evidence remain unchanged;
there is no reason to replace them while completing repository validation.
