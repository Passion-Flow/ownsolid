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

### Retained inputs and the experiment record boundary

The initial inspection of the retained wheel inventory, Linux pip HTTP
cache and earlier fixture directories found the 30 dependency archives
and the catalogued Qwen files. It found no local copy of
`pip-26.2.1-py3-none-any.whl` matching the runtime manifest's 1,816,632
bytes and SHA-256
`71138adf1f4ca900cdb7d289c21b7494329f2332b6d85f0e1c42108c0384ed3e`.
The upstream Tokenizers tag supplies the source license. The selected
native wheel still needs approved binary notice material bound to its
hash, or an approved replacement pin carrying its complete notices.
The public source license and registry metadata can corroborate identity
and terms. The release gate also needs the actual approved files.

For DAT-107, once that media is available, build a Linux amd64 preview
image from the hash-pinned base with installation network-disabled. Bind
the selected OCI manifest, its config digest and the projected lock digest
to the verified build inputs, then run the local Qwen smoke. Qualify the
installed runtime injection separately under the published preview contract.

The [TRN-105 experiment record](training-experiment-records.md) requires
a separate verified mapping for the full training execution image. The
[OCI manifest](https://github.com/opencontainers/image-spec/blob/v1.1.1/manifest.md)
selects the config descriptor and layers. The
[image configuration](https://github.com/opencontainers/image-spec/blob/v1.1.1/config.md)
defines the local image ID from the config bytes. Bind both digests to
the exact full training lock verified during manufacture. The projected
preview lock and the frozen reference in a worker request describe
different inputs. Until the approved full image and its build evidence
exist, the experiment's dependency lock field stays unavailable.

### Pinned installer admission

Stage the [pip 26.2.1 upstream wheel](https://pypi.org/project/pip/26.2.1/#files)
from the exact URL in the runtime manifest. The downloaded file matched
the manifest's 1,816,632 bytes and SHA-256
`71138adf1f4ca900cdb7d289c21b7494329f2332b6d85f0e1c42108c0384ed3e`.
The upstream release metadata supplied the same size and digest.

The Linux amd64 disconnected release checker admitted all 30 projected
dependencies and the exact installer. It returned
`missing wheels: none; missing notices: tokenizers` and removed its
incomplete staging directory. The exact pip artifact clears the installer
input for this fixture. Release admission still requires approved notices
for the selected Tokenizers binary, or an approved replacement pin with
the required notices. The separate full training image and its verified
manifest, config and full-lock mapping remain release inputs.

A separate tokenizer test installed those same 30 pinned wheels with
pip 26.2.1 in a fresh CPython 3.12.3 environment with external networking
disabled. Installation used local binary wheels and required each hash.
`pip check` passed and the installed inventory contained 31 packages,
including pip. The projected lock had SHA-256
`7873b0c15e07995060826373087b050437d10cee5af935e6c717b9e10622c19d`.
All 19 training runtime tests passed with the approved local Qwen
tokenizer assets. This install was a private runtime test. The strict
release checker still requires the binary notice before emitting a
fixture manifest.

### Exact Tokenizers notice inputs

The retained wheel is
`tokenizers-0.23.3-cp310-abi3-manylinux_2_17_x86_64.manylinux2014_x86_64.whl`.
Its 3,396,193 bytes match the Linux training lock and the
[PyPI release](https://pypi.org/project/tokenizers/0.23.3/#files).
The wheel's `METADATA` has an Apache classifier and no `License`,
`License-Expression`, or `License-File` fields. The archive has no
license file. The `WHEEL` entry identifies Maturin 1.15.0 and the
CPython 3.10 stable ABI used on CPython 3.12.

[PyPI's verifier](https://docs.pypi.org/attestations/consuming-attestations/)
verified the exact archive offline using the staged Sigstore trust data
and provenance from the [integrity endpoint](https://pypi.org/integrity/tokenizers/0.23.3/tokenizers-0.23.3-cp310-abi3-manylinux_2_17_x86_64.manylinux2014_x86_64.whl/provenance).
It rejected a one-byte change and a different expected repository.
The publishing identity names the `release` environment and
[`python-release.yml` at the tag commit](https://github.com/huggingface/tokenizers/blob/301277072263fb3830eb66eec7e47adadf76f1dc/.github/workflows/python-release.yml).
Its source digest matches `301277072263fb3830eb66eec7e47adadf76f1dc`.
The [root license at that commit](https://github.com/huggingface/tokenizers/blob/301277072263fb3830eb66eec7e47adadf76f1dc/LICENSE)
and Python source archive's `tokenizers/LICENSE` are byte-identical.
The source archive's `bindings/python/Cargo.lock` also matches the tag.
The embedded Rust SBOM includes the local core crate 0.23.2 from that
same tree and 126 registry entries. The Python package is 0.23.3.

| Input | SHA-256 |
| --- | --- |
| Selected wheel | `376851d22bcf9d650a5c3090bb83e6cf9e895fbf0595369fa4cd43c1f69b5f87` |
| Native extension | `4b1458a379296762a034d3bc89299c0c35b4eefb0577acca922f481b262c65a1` |
| Published Python source archive | `cded33237c77caeef62944d32aa9a7ef42bdce2b3497e18d137e072a8c4be438` |
| Tagged source license | `c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4` |
| Tagged Cargo lock | `afcedf7f2ebe008b068d7afd25e77f7d9d8e433c24153bc35a1eb5611083ee5b` |
| Embedded CycloneDX SBOM | `6faa23324b2b53b4a8da9b451a675610cc45e1a23e8c5bb0740c080064e1ba0d` |
| PyPI provenance JSON | `9040f103398a9349f6e651afa476d5b4c08a6c2eb2f4cf478f1d8b20d879e689` |
| DEL notice candidate archive | `2eb423cd61dec1cf9a2e0c0349d954d2daa15341e86db6b064e7408069d93204` |
| Candidate inventory JSON | `d74c6d354671f827e7bdb7c2d82b872da1f30809d6c2b13cbcf4d252fbda76bc` |

The private `tokenizers-0.23.3-linux-amd64-notices.tar.gz` candidate
contains the original notice bytes, paths and digests for all 127 SBOM
components. Every registry source archive matched its Cargo checksum.
The `defmt-parser` 1.0.0 archive omitted notices. Its recorded source
commit identifies the [upstream license files](https://github.com/knurling-rs/defmt/tree/4a8cdb44891ed57b8ff5a023b6bec7137c48708f).
Its `src/lib.rs` and original manifest matched that commit byte for byte.
The inventory preserves Oniguruma's COPYING text and distinct original
C header attributions, the vendored esaxx and sais notices, the
`unicode-ident` Unicode-3.0 text, and Oniguruma's
[Unicode 16.0 data attribution](https://www.unicode.org/Public/16.0.0/ucd/ReadMe.txt)
and [Unicode license](https://www.unicode.org/license.txt). The wheel
has a Rust SBOM and no grafted library files. The ELF names glibc,
`libstdc++` and `libgcc_s` among its shared-library requirements.
The selected base image supplies its own system-library notices.

Commercial use and offline distribution are allowed under the applicable
terms. Carry the Apache-2.0 text with redistributed copies and retain
attributions and applicable NOTICE material under
[Apache-2.0 section 4](https://www.apache.org/licenses/LICENSE-2.0#redistribution).
Carry the original copyright, permission and disclaimer texts for
bundled components, including the separate BSD-style Oniguruma and
Unicode terms. The archive supplies the declared alternatives for
review. The SBOM describes Maturin's dependency graph. DEL reviews the
binary notices and base-image notices for the selected build.

The [wheel specification](https://packaging.python.org/en/latest/specifications/binary-distribution-format/#the-dist-info-licenses-directory)
uses `.dist-info/licenses/` for files declared by `License-File`. The
current [release checker](../../tools/release/offline/README.md#tokenizer-wheelhouse-fixture)
requires embedded notices and still refuses this upstream wheel. The
private candidate is ready for DEL's approval as a wheel-digest-bound
notice asset and offline installation/readback of the signed media.
An approved replacement wheel with embedded notices would require its
own provenance and a reviewed lock update. Repacking the existing
wheel changes the pinned digest and breaks the upstream attestation.

#### Rust standard-library notices

The selected native extension's ELF `.comment` names rustc 1.99.0.
Its embedded source paths give the full commit
`b940084d7eb6a299eb4bfeb8e34901bc051e7ac4`.
The matching official Linux amd64 compiler and standard-library archives
both report that commit. The compiler archive carries the generated
`COPYRIGHT-library.html` and the distribution's license texts. The
standard-library archive supplies the root copyright and MIT and Apache
texts, which match the [source commit](https://github.com/rust-lang/rust/tree/b940084d7eb6a299eb4bfeb8e34901bc051e7ac4).

| Rust notice input | SHA-256 |
| --- | --- |
| [Compiler distribution](https://static.rust-lang.org/dist/rustc-1.99.0-x86_64-unknown-linux-gnu.tar.xz) | `77171ba2a0345fdf2abc4fedda55d6de078dae7a68527c28be8c77dcc9604bd5` |
| [Standard-library distribution](https://static.rust-lang.org/dist/rust-std-1.99.0-x86_64-unknown-linux-gnu.tar.xz) | `3e58dff2d0b72196b5ea4e90536e174d400de88564a52694686b81e091169933` |
| Original `COPYRIGHT-library.html` | `5647be074c8edf7339fd863055923a8fc80bc5610a8d4661ec3b767b9d392c27` |
| DEL Rust standard-library notice addendum | `39b279fc31fb3bbf743cf0569d7605dd92125aa0fe305e99b5675aae44a0d787` |
| Addendum inventory JSON | `79e88854fbb39e90e37dc9adad3e0f324a1d43e652550924aaf5e97094a0ca3f` |

The private `tokenizers-0.23.3-linux-amd64-rust-std-notices.tar.gz`
contains the 18 original files and an inventory binding their digests
to the same wheel and ELF. A disconnected read verified all 18
files. This addendum covers the Rust toolchain's standard-library
notice evidence alongside the wheel's dependency notices. DEL applies
the official standard-library inventory and the selected base image's
system-library terms when reviewing the signed release assets.

### Delivery readback of the retained notice candidates

The delivery fixture on `ownsolid-cpu` read the exact retained pip
26.2.1 wheel, Tokenizers wheel and two notice archives without extracting
them. It matched the installer, wheel and ELF pins and compared the
embedded CycloneDX bytes with the retained SBOM. All 127 component names
and versions matched. The fixture verified the size and SHA-256 of 272
distinct dependency archive payloads and all 18 Rust notice payloads.
It checked the shared upstream license wherever the inventory referenced
it and matched the `defmt-parser` source evidence to its recorded commit.
It refused a changed archive and a changed Rust copyright file. The
isolated namespace exposed only loopback and the recorder, which captured
just the IPv4, IPv6 and DNS probes.

The source archive has SHA-256
`3a6d9da4b062531ba71e57424d2a4ec6f067f8b15cdfd1d5a67767b8218212ac`.
The transcript has SHA-256
`5b7dd16d232a686f657b8cffa1742e28183e8a2c948bcff6cca2b6af0021aeb7`.
This is evidence for the retained candidates. Release admission still
requires an approved binary notice asset bound to the exact wheel,
the selected base image's applicable notices, and verification of
the signed offline media. The embedded-notice checker remains in
force. A preview image built from the accepted inputs still needs
installed runtime injection and offline Qwen readback. The full
training image and lock mapping remain separate.
