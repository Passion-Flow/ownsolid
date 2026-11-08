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
