# TRN-108 offline training runtime inputs

[Transformers offline loading](https://huggingface.co/docs/transformers/installation#offline-mode)
loads models from admitted local files. [pip hash checking](https://pip.pypa.io/en/stable/topics/secure-installs/)
provides the build-time verification for a pinned wheelhouse. Keep dependency
installation in the release build and require the worker's local model
catalogue to match the digest supplied with the request.

The worker currently looks for an inherited contracts directory. The live
approved catalogue moved to api/agent/v1 in this repository. Admit that
layout and retain the inherited path for a release image that still has it.
Use the current file if both exist, and fail if that file is invalid. A
fallback on corruption could mask a bad build. An unrestricted path or
runtime download would weaken the release's trust and offline boundaries.

The source fingerprint includes the installed worker code and its data
processor. Exclude unit tests from the fingerprint so release images can omit
test sources while retaining the same worker identity. Keep the original
fingerprint for a tree that contains only executable sources.

This change uses only the Python standard library under the
[PSF licence](https://docs.python.org/3/license.html). It adds no wheel,
model, dataset or new redistribution terms. It reads catalogue metadata,
not model weights. The existing wheel locks and their redistribution review
remain inputs to the release image.

Verify current and inherited layouts, invalid preferred assets, and agreement
between the current catalogue and the worker's existing digest calculation.
The full item also needs the release owner to build and install the pinned
image from a verified wheelhouse with the network disconnected, preserve
dependency notices, and test the Agent's image and source identity checks.

## Linux amd64 tokenizer-only wheelhouse

The scoped dataset preview uses the existing training tokenizer, Jinja
template and local catalogue. Its separate runtime can select a closed
subset of the [Linux training lock](../../runtimes/training/requirements/linux-amd64-cuda.lock).
The full training image continues to use that lock for CUDA execution.

Select the following normalized distribution names from the lock and
retain each original version and all of its allowed wheel hashes.

```text
annotated-doc anyio click filelock fsspec h11 hf-xet httpcore2 httpx2
huggingface-hub idna jinja2 markdown-it-py markupsafe mdurl numpy packaging
pygments pyyaml regex rich safetensors setuptools shellingham tokenizers
tqdm transformers truststore typer typing-extensions
```

In connected release preparation, stage only binary wheels for those pins.
In the disconnected build, create an isolated interpreter environment and
install from that wheelhouse using `--no-index --no-deps --require-hashes`.
Use the hash-pinned pip installer in the [runtime manifest](../../runtimes/training/runtime.json)
and run [pip check](https://pip.pypa.io/en/stable/cli/pip_check/) against the
result. Preserve the existing dependency notices. The preview itself must
load the installed runtime manifest, full applicable training lock,
compiled catalogue and approved local assets.

Linux amd64 CPython 3.12.3 installed all 30 wheels into a fresh environment
inside a network namespace. Pip 26.2.1 reported no broken requirements.
The installed tokenizer reproduced 22 tokens for the synthetic smoke
sample from the fixed Qwen snapshot. The source lock SHA-256 was
`250fee15d9e7eb9ec71793c743b02f62870094bb494a57a0c463c5ce29399946`.
The derived requirements SHA-256 was
`7873b0c15e07995060826373087b050437d10cee5af935e6c717b9e10622c19d`
with original entry order, escaped line continuations joined and one
terminating newline. The [control adapter handoff](training-tokenizer-preview.md#control-plane-bridge)
covers the remaining release composition. Production image build,
installation and the Agent's full training runtime checks remain open.

## Release input verification

The [wheel format](https://packaging.python.org/en/latest/specifications/binary-distribution-format/)
puts compatibility tags in the filename and distribution identity and notice
paths in the archive. [Core metadata](https://packaging.python.org/en/latest/specifications/core-metadata/)
allows both SPDX expressions and older license fields. The release input
checker should bind the exact runtime and catalogue bytes, project the existing
Linux lock in its original order, and require exactly one compatible binary
wheel for each selected pin and the separately pinned pip wheel. Stage private
copies and inventory their sizes, hashes and embedded notices. Use pip's
binary-only, local, hash-required installation and `pip check` against those
copies. Keep the full training lock and the current catalogue layout for the
existing tokenizer smoke. Check the five runtime assets and Qwen's license
against the approved revision. Model weights are outside this input profile.

The helper uses the Python standard library under the PSF license. The admitted
wheel bytes carry the licenses and notices for the distributions they contain.
Inventory those documents verbatim by digest so a separate redistribution
review can address each exact binary, including bundled native libraries. Qwen's
catalogue records Apache-2.0, which permits commercial use, redistribution and
offline use subject to its notice and attribution conditions. A hash allowed by
the training lock verifies wheel identity. A production release also needs
approved artifact provenance and a notice review for the selected bytes.

Keep the existing isolated fixture as the source of any rerun without approved
release media. Neither a live package index, a platform Python environment nor
a CUDA image is the input. Automated failure checks should cover a changed
source lock, incompatible wheel tags, missing or duplicate pins, changed bytes,
unexpected assets and missing notices. The Linux fixture should demonstrate that
pip can install and check the exact set and run the local Qwen smoke with no
packets beyond the recorder's deliberate probes. Keep this input check outside
the default Control image and leave the optional production interface unconfigured.

### Retained license evidence

The 30 cached wheel archives matched one allowed SHA-256 per distribution.
Their metadata and 69 license or notice files yielded these declarations.
The NumPy and setuptools archives contain additional bundled notices.

| Declared terms | Distributions |
| --- | --- |
| MIT | annotated-doc, anyio, filelock, h11, markdown-it-py, mdurl, pyyaml, rich, setuptools, truststore, typer |
| BSD 3-Clause | click, fsspec, httpcore2, httpx2, idna, jinja2, markupsafe |
| Apache 2.0 | hf-xet, huggingface-hub, safetensors, tokenizers, transformers |
| BSD 3-Clause, 0BSD, MIT, Zlib and CC0 1.0 | numpy |
| Apache 2.0 or BSD 2-Clause | packaging |
| BSD 2-Clause | pygments |
| Apache 2.0 and CNRI Python | regex |
| ISC | shellingham |
| MPL 2.0 and MIT | tqdm |
| PSF 2.0 | typing-extensions |

These terms allow commercial and offline use and permit redistribution
subject to their applicable conditions. Preserve notices, attribution and
license text. Review the source obligations for covered MPL files and the
bundled notices before shipment. The retained
[`tokenizers` 0.23.3 source license](https://github.com/huggingface/tokenizers/blob/v0.23.3/LICENSE)
is Apache 2.0. Its selected Linux wheel
(`376851d22bcf9d650a5c3090bb83e6cf9e895fbf0595369fa4cd43c1f69b5f87`)
has the Apache metadata classifier and no embedded notice file. A release
needs approved notice material tied to that exact native binary or an
approved replacement with complete notices. The separately pinned
[pip 26.2.1](https://pypi.org/project/pip/26.2.1/) is MIT. Its original
1,816,632-byte installer was absent from the retained wheel cache. The
[Qwen snapshot license](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
was retained and matched the catalogue SHA-256.

### Disconnected release-side replay

On Ubuntu 24.04 amd64, CPython 3.12.3 and glibc 2.39, the release checker
recreated the projected lock SHA-256 above. It staged the wheel inventory,
then reported the missing pinned pip wheel and the missing `tokenizers`
notice together. It removed the incomplete staging directory. An additional
eight focused checks exercised the projection, pin and file admission
failures. The installed runtime's protected-file policy was confirmed
during the replay. Git archive extraction can leave 0664 contract files
under a group-write umask. The clean run used 0644 for the catalogue, runtime
manifest and full lock.

The isolated replay used the same 30 wheel hashes and the five retained
Qwen tokenizer assets. The only different input was pip 25.0.1, whose
wheel came from the already cached Control Python base
`sha256:6a0e06f5ba0b373d051aabd3fffb9b873f27f9a2a3a14bcbe37dee6f0e3bbc70`
and matched SHA-256
`c46efd13b6aa8279f33f2864459c8ce587ea6a1a59ee20de055868d8f7688f7f`.
The release helper's installation function created a fresh venv and
installed with `--no-index --no-deps --require-hashes --only-binary=:all:`.
It checked the exact installed set and `pip check` found no broken
requirements. The unmodified repository training suite passed all 19 tests,
including the real Qwen template, token, training parity and refusal cases.
The raw model catalogue SHA-256 was
`1828d79a898ae556e07d7d9fd0f1fa1e1841f06c380562d5ed9626762eb9f822`.

The harness used `unshare --mount --net --propagation private` and routed
IPv4 and IPv6 solely to the repository egress recorder. It replaced DNS
with a test address and captured a deliberate IPv4 TCP, IPv6 TCP and UDP DNS
probe. The capture's SHA-256 was unchanged through admission, installation
and the tokenizer run. This replay supplies no release approval. The
formal wheelhouse, complete native notices and the released image and
control integration remain open.
