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
