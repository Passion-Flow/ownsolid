# DAT-107 reproducible tokenizer preview

The [Transformers chat-template guide](https://huggingface.co/docs/transformers/main/chat_templating)
describes model-specific control tokens and warns about adding special tokens
again after rendering. [Transformers padding and truncation](https://huggingface.co/docs/transformers/main/pad_truncation)
makes truncation an explicit choice. Qwen publishes the template in the
[pinned tokenizer configuration](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/tokenizer_config.json).
The [Qwen model card](https://huggingface.co/Qwen/Qwen3-0.6B/tree/c1899de289a04d12100db370d81485cdf75e47ca)
documents disabling thinking during templating. Ownsolid's training worker
adds the assistant generation prefix with thinking disabled, encodes the
completion separately, and appends EOS. It refuses examples over the budget.
Preview should share the same code and show where a cut would start.

## Dependencies and proof

The [training lineage acceptance](training-dataset-lineage.md#retained-cpu-acceptance)
verified all nine files in the approved Qwen3-0.6B snapshot. The preview
needs only the five small files named by the [installed catalogue](../../api/agent/v1/model-catalog.json).
A prototype rechecked their sizes and SHA-256 hashes and loaded the
five-file subset on macOS arm64 with CPython 3.12.14 under kernel network
denial. All 37 wheels were installed with the existing native lock and
hash checking from the offline cache. Transformers 5.19.0, Tokenizers
0.23.3 and Jinja 3.1.6 rendered and tokenized the sample. The cached Linux
inference image has different dependency versions and is unsuitable for
this test. The training attempt retains its exact Python and execution
identity checks.

No new dependency, model, template or dataset is introduced. The existing
approved dependency locks and release redistribution notices still apply.
[Qwen](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE),
[Transformers](https://github.com/huggingface/transformers/blob/v5.19.0/LICENSE),
[Tokenizers](https://github.com/huggingface/tokenizers/blob/v0.23.3/LICENSE),
and [Hugging Face Hub](https://github.com/huggingface/huggingface_hub/blob/v2.2.0/LICENSE)
are Apache-2.0. [Jinja](https://github.com/pallets/jinja/blob/3.1.6/LICENSE.txt)
and [MarkupSafe](https://github.com/pallets/markupsafe/blob/3.0.4/LICENSE.txt)
are BSD-3-Clause. [Python](https://docs.python.org/3/license.html) is
PSF-licensed. These terms allow commercial, redistributed and offline
use subject to the applicable license and notice obligations. Tests use
synthetic text.

## Runtime decision

Use the requested canonical digest to identify the approved installed
catalogue. Copy only the five named, size- and hash-verified files to a
private temporary directory and use the training tokenizer loader with
local-only access and remote code disabled. The same open file supplies
both the bytes copied and the hash. This keeps extra local files from
overriding the template or configuration. Verify the versions of the
tokenization and template packages against the hash-bound training lock
for the current platform. A character count or generic BPE tokenizer
would miss control tokens and Unicode splits. Encoding a concatenated
rendering would erase the training boundary between prefix and completion.
Loading weights would add storage and memory without improving accuracy.

Accept a bounded batch of canonical prompt/completion rows already sampled
by the dataset reader. Return original line and content hash, exact counts
for prefix and completion (including EOS), and the first over-budget token's
zero-based index and section. Return separately rendered prefix, completion
and EOS. Preview is read-only and does not truncate or change training
input. The worker still refuses overlength samples and masks prefix labels.
Existing sealed attempts continue to require their original admitted
worker and image identity.

## Verification and handoff

Check exact parity against independent template/encode calls and the
training worker's IDs, labels, provenance and over-budget refusal.
Test exact fit, overflow in each section, whitespace, Unicode, reserved
tokens, input byte/sample limits, token-budget bounds, an unknown catalogue
digest, missing/altered files, and an extra template file. With the real
pinned assets the same tests must pass with network access denied.

On macOS arm64 the full training test suite passed with the pinned
native wheels, the verified five-file snapshot and kernel network denial.
The real cases matched the previous training algorithm's token IDs,
label masks and source provenance, exercised the three overflow sections
and exact fit, and rejected a package mismatch and altered assets.
Linux unit tests exercise the refusal paths without the tokenizer wheels.
The Linux release and product preview still require an end-to-end run.

### Release and control handoff

Ship the installed catalogue, runtime manifest and applicable training lock
with the tokenizer packages in the offline preview environment. Resolve
the model path and catalogue digest from trusted configuration. Authorize
the dataset and artifact in the requester's scope, stream only the sampled
rows, and bound concurrency and time before invoking the runtime. Map the
runtime refusal codes to actionable errors without exposing host paths or
sample content in logs.

### Dataset UI handoff

The S-503 dataset preview entry point must show the original counts, the
token budget and the section where an overlength sample first exceeds it.
Render the three sections as escaped text. Explain that training rejects
overlength rows and preserve loading, permission and retry states.
