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
