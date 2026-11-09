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
On Linux amd64, all 19 tests also passed with CPython 3.12.3 and 30
hash-checked wheels selected from the existing Linux training lock.
Installation used the local wheelhouse with index access disabled, and
both installation and tests ran in a network namespace. The real tokenizer
tests used the five assets from the retained Linux snapshot. The packaged
release and dataset preview UI still require an end-to-end run.

### Release and control handoff

Ship the installed catalogue, runtime manifest and applicable training lock
with the tokenizer packages in the offline preview environment. Resolve
the model path and catalogue digest from trusted configuration. Authorize
the dataset and artifact in the requester's scope, stream only the sampled
rows, and bound concurrency and time before invoking the runtime. Map the
runtime refusal codes to actionable errors without exposing host paths or
sample content in logs.

### Control-plane bridge

Reuse the model registry's [scoped ready and range checks](../../services/control/internal/modules/models/transfer.go)
to stage the five tokenizer assets for the requested ready model ID.
Verify the full lengths and SHA-256 against the compiled catalogue before
the Python preview independently verifies the assets and installed catalogue.
The request selects a model ID in the authenticated workspace. Filesystem
paths and catalogue identity remain under operator control.

Read the first bounded page through the authorized draft reader and reject
invalid samples. Use the existing dataset subprocess limits and process
custody. Start the pinned interpreter in [isolated mode](https://docs.python.org/3.12/using/cmdline.html#cmdoption-I)
with [direct process execution](https://pkg.go.dev/os/exec). Keep the
transport adapter with its control owner so data and training worker source
identities remain stable. The control image currently ships the dataset
processor and standard Python. The handler requires explicit injection of
a hash-checked runtime and private scratch space. This adds no dependency,
model, dataset or licence.

The isolated Linux prototype produced the expected counts with the existing
dataset CPU and memory limits. Peak RSS was 179,768 KiB. In the loopback-only
HTTPS and PostgreSQL fixture, Admin and Portal used the real tokenizer from
the retained Qwen snapshot. The test seeded scoped ready-model metadata
and installed only the five read-only files. This exercises the existing
registry range reader and exact-byte verification. Model import and production installation have separate evidence. A foreign
draft and model, missing and same-size altered assets, an invalid token
budget and an oversized batch were refused. The exact-fit and over-budget reads bound the compiled
catalogue and canonical row digest. Existing draft and publication
requests used the same dispatcher and subprocess runner.

To repeat the fixture, configure the real loopback database and
`PROJECT_ONE_DATA_PYTHON` as in the
[development environment](../development/environment.md). Set
`OWNSOLID_TEST_PREVIEW_PYTHON` to the isolated, hash-checked preview
interpreter and `OWNSOLID_TEST_QWEN_TOKENIZER_DIRECTORY` to the retained
approved assets. With external networking disabled, run:

```sh
go -C services/control test -tags dataset_processor_integration -count=1   ./internal/modules/datasets -run '^TestActualDatasetTLSContractsLargeUnicodeRowsCSRFExportAndReadiness$'
```

The fixture reserves a 30-second child wall budget and a 45-second HTTP
deadline for the cold model-store read and tokenizer load. The preview
uses separate resource settings from inspection and publication. Before enablement, DEV must publish
the optional route and its error contract. The composition and release
owners must supply typed configuration, the protected runtime and a
sufficient request and file-size budget. S-503 owns the user entry point.

### Dataset UI handoff

The S-503 dataset preview entry point must show the original counts, the
token budget and the section where an overlength sample first exceeds it.
Render the three sections as escaped text. Explain that training rejects
overlength rows and preserve loading, permission and retry states.

### Authorization after tokenization

A preview reads protected draft text, then spends time running the
tokenizer outside PostgreSQL. The session, workspace grant, draft access,
or ready model may change during that work. Apply
[OWASP's request authorization guidance](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html#validate-the-permissions-on-every-request)
at the response boundary using the existing ordered authorization
transaction and the ready-model reference check. Retain the prepared
sample digests. Refuse the complete result if the final authority check
fails. Holding a database transaction across Python would block unrelated
scope writes. Relying on the initial authorization would allow a revoked
session to receive private sample text.

The existing child wall, memory and single-slot limits remain in force.
Use the pinned Qwen tokenizer and verify a training-reserved token maps to
the dataset validation error. In the fixture, hold the real interpreter
after it has written a valid response and before it exits. A concurrent
request must receive the rate-limit error. Log out the original actor,
release the interpreter, and require a refusal with zero sample data.
A later authorized request must still succeed. The fixture uses only the
standard library, the existing hash-locked runtime and approved
Apache-2.0 Qwen files. It adds no shipped dependency or model.

The Linux loopback-only PostgreSQL and HTTPS run passed with all five
digest-verified Qwen assets and the 30 hash-locked preview wheels. The
real interpreter produced a prepared response before the original
session was revoked. The concurrent request returned `RATE_LIMITED`,
the revoked request returned no preview, and the existing authorized
Portal session succeeded after release. The HTTP route also refused the
training-reserved token and recovered after restoring the correct model
file.

The integration fixture now loads the exact installed tokenizer locally
before making the first HTTPS preview request. This separates cold package
and asset I/O on the shared test host from the fixed 30-second fixture
client deadline. The route and the timed child still run with their
existing deadlines and resource limits.
