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

### Signed preview input fixture

Reuse the existing release factory's explicit test profile and a
temporary Ed25519 signer. Keep the full and projected locks, runtime
manifest, actual wheels, tokenizer assets and candidate notices in the
same signed manifest. Use pip's [binary-only hash checking](https://pip.pypa.io/en/stable/topics/secure-installs/)
again after independently pinned archive preparation. Bind the base image
reference to the locally observed Linux amd64 identity. Preserve the
Debian [package copyright files](https://www.debian.org/doc/debian-policy/ch-docs.html#copyright-information)
and the CPython license from the cached base as candidate evidence. The
base notice bytes from a local image and the wheel notice archives have
separate redistribution review. No new dependency, model, dataset,
image pin or release trust is introduced by the fixture.

Test different failure boundaries. A different public trust file must
reject the original signature. A valid fixture signature cannot substitute
for a required base notice or a matching wheel notice. Refuse such a
fixture before staging an interpreter. Verify the existing release
manifest and asset hashes before creating a private hash-installed
runtime. Test the optional preview adapter with the resulting installed
interpreter, the protected runtime code and its private scratch root.
Test missing runtime configuration separately. Keep this test outside the
production install path. The Control composition owner still needs typed,
operator-provisioned injection under the published route.

The recovered signing harness remains a prototype. The original base-image
read recorded 106 Debian and CPython notice paths from the digest-pinned
Python image. Its temporary notice archive and final signing transcript
are unavailable. Re-read those bytes before fixture manufacture and
complete the signature, notice, installation and bridge sequence in the
scheduled Linux window. The factory refused the original 97-character
MarkupSafe wheel filename under its 96-character path-component rule.
The prototype now carries short asset paths and an independently signed
filename map for private pip staging. That continuation still requires
acceptance. A test scope marker for first-party inputs grants no
redistribution approval.

The retained input readback on Linux amd64 verified all 30 selected wheel
hashes, the exact pip 26.2.1 installer, 69 embedded notices and both
Tokenizers notice archives again. The full lock SHA-256 remained
`250fee15d9e7eb9ec71793c743b02f62870094bb494a57a0c463c5ce29399946`
and the projected lock remained
`7873b0c15e07995060826373087b050437d10cee5af935e6c717b9e10622c19d`.
The source checkout was `fa82b4dd`. This read-only check ran with no
external route and an empty network system-call trace. A separate source
check confirmed cleanup and a clean checkout after the reader's memory
limit caused the final Git status command to fail. The transcript SHA-256
is `ab33f97012df8161bb57f81841544bc87939b9df4d92065dbe12f4e095e7fa70`.
The separate cleanup check has SHA-256
`ed0d6467676bbb382845086dbcc432a4d763267b2c0db43d68247d10aee1c89f`.
The recovered source archive has SHA-256
`9db9acb248a54d20295fa4d62e1edd9c638e3b27a8b3819ef6d833d3c6968985`.

### Installed preview composition handoff

The shared handler builds `Datasets.Service` in
`services/control/internal/modules/identity/http/handler.go`. Production
composition has no caller of `NewTokenizerPreviewProcessor`. The existing
constructor takes the same `Datasets.Service.Processor`, the handler's
`Models.Service` and `TokenizerPreviewConfiguration`, then supplies
`Datasets.TokenizerPreview`. The processor identity must match the service
that handles the request.

The configuration owner must provide a protected absolute
`PythonExecutable`, `TrainingRoot`, `ScratchRoot` and validated
`Resources`. The executable must be a regular file with protected parent
permissions. A venv's default interpreter symlink fails this existing
admission rule. `TrainingRoot` contains the signed runtime sources,
manifest and full applicable lock. `ScratchRoot` belongs to the service
identity, has mode 0700 and is separate from durable dataset and model
storage. Keep these inputs under installation authority. The existing
missing-runtime response is `DATASET_STORAGE_UNAVAILABLE`.

Complete the approved notice assets and image manufacture before enabling
the typed startup configuration. Acceptance must use the authenticated
Admin and Portal routes, the installed interpreter and the exact source
and lock binding. It must cover runtime absence, invalid protected files,
changed tokenizer bytes, authority loss and retry, with recorded egress.

## Exact Linux CPU training admission

The [official CPU index](https://download.pytorch.org/whl/cpu/torch/)
publishes a distinct `2.14.1+cpu` distribution for CPython 3.12 and
manylinux 2.28 amd64. The existing Linux lock selects the ordinary
[PyPI 2.14.1 release](https://pypi.org/pypi/torch/2.14.1/json).
Python packaging treats `+cpu` as a
[local version identifier](https://packaging.python.org/en/latest/specifications/version-specifiers/#local-version-identifiers).
A dependency specifier can admit a local build while the worker's exact
installed-version check still rejects it. The current worker requires
Torch `2.14.1`, Transformers `5.19.0`, PEFT `0.21.2`, Accelerate `1.15.0`
and Python `3.12.15`. The server interpreter is `3.12.3`. Installing the
CPU wheel or relaxing a requirement specifier cannot establish that runtime
identity. Keep the current production checks unchanged.

The dependency audit evaluated the four project roots and their complete
`Requires-Dist` markers for Linux amd64 CPython 3.12.15, with no optional
training extras. For every pin it selected a compatible binary wheel whose
advertised SHA-256 is allowed by the existing lock, and checked its
[core metadata digest](https://packaging.python.org/en/latest/specifications/simple-repository-api/#serve-distribution-metadata-in-the-simple-repository-api)
against the package index. The ordinary Linux graph reaches all 56 locked
distributions. Substituting the official CPU metadata reaches 37, with the
same 36 non-Torch versions. The CPU graph omits 19 CUDA-specific packages,
including Triton and the NVIDIA libraries. This is a metadata compatibility
result, not a new admitted lock or installed-environment result.

```text
accelerate annotated-doc anyio click filelock fsspec h11 hf-xet httpcore2
httpx2 huggingface-hub idna jinja2 markdown-it-py markupsafe mdurl mpmath
networkx numpy packaging peft psutil pygments pyyaml regex rich safetensors
setuptools shellingham sympy tokenizers torch tqdm transformers truststore
typer typing-extensions
```

| Selected Torch input | SHA-256 | Verification scope |
| --- | --- | --- |
| Ordinary PyPI `2.14.1` wheel | `23011fe29a99b591eabb3a31a25080c62e2c2f0690a3c798744e5489dae50451` | Advertised 554,618,695-byte archive pin in the current lock; archive not fetched in this audit |
| Ordinary PyPI core metadata | `f1e9245037c999a2ff46ba37e329cff31dcb3678d400f656bdc61fa906e6e3e6` | Downloaded bytes match the index metadata digest |
| Official CPU `2.14.1+cpu` wheel | `5a6363570c753812540a05eb82380e329469cbe668643e88111414c12627711f` | Complete 196,253,677-byte archive matches the official CPU index pin |
| Official CPU core metadata | `a3fd232f82dcc389c6dab2665dfc17649c03ffb72cb641e96a76a0690177b918` | Downloaded metadata matches the full wheel's original `METADATA` |
| Dependency metadata report | `bf6f13282733976703893fd18ecde60e1b2a181fe00eb233d7efd521b36be631` | Both graphs and each selected file's URL, size, metadata and archive digests |
| CPU wheel notice inventory | `6112088ab9b31bbdca839f912c70a7b2349fc3ad17f9177cd2de541fd49392b2` | Original `METADATA`, `WHEEL` and all 86 declared license files, with archive `RECORD` hashes and sizes checked |

Both wheel filenames end in `cp312-cp312-manylinux_2_28_x86_64.whl`;
their version fields differ as shown. The CPU index's alternate official
origin supplied the full archive after its linked mirror timed out.
The CPU archive audit ran with the network disconnected and retained the
original bytes. The metadata graph replay also ran disconnected and
reproduced the same report digest. Altering the staged CPU metadata caused
digest rejection before dependency traversal.

The CPU wheel declares `Apache-2.0 AND Apache-2.0 WITH LLVM-exception AND
BSD-2-Clause AND BSD-3-Clause AND BSL-1.0 AND MIT`. It carries all 86 declared
original license files, totalling 307,343 bytes; preserve them with the
binary, including upstream's bundled-component collection. These declared
terms permit commercial use, offline use and redistribution under their
notice, attribution, disclaimer and applicable Apache conditions. A complete
binary and base-image notice review is still a release input. The 30-package
preview inventory above covers their existing declarations. The six other
non-Torch distributions declare Apache (Accelerate and PEFT), BSD
(mpmath and SymPy), or BSD-3-Clause (NetworkX and psutil). This audit checked
their core metadata, not their complete binary notice payloads. It adds no
model or dataset and installs no dependency.

### Remaining admission and recovery gates

No CPU runtime profile is created from the metadata graph. Before a Linux
CPU worker can run under its own admitted identity, release inputs must
supply all of the following:

- A reviewed CPU runtime manifest and complete 37-distribution wheel lock,
  binding the local Torch version and exact binary digests independently of
  the existing CUDA and macOS profiles. The CPU Torch hash is absent from
  both current locks. Preserve full original notices for every selected
  binary, including the Tokenizers notice candidates above.
- The exact CPython 3.12.15 Linux interpreter and selected base image,
  with their identity, redistribution notices and approved offline media.
- A fresh disconnected installation from only those inputs using the exact
  pinned pip, `--no-index --require-hashes`, then `pip check`, actual imports
  and the worker's strict runtime-identity checks. Bind that result to the
  installed runtime and delivery identity.
- A real interrupted two-step CPU run: retain the step-one checkpoint and
  its complete seal, start a new worker with the same sealed training
  horizon, verify `resume_from_step == 1`, and compare its final model,
  optimizer, scheduler, RNG and trainer state with an uninterrupted baseline.
  Reject changed optimizer bytes and a changed sealed horizon before training.

The exact CPU archive is now available for that review. A metadata closure,
complete archive or retained uninterrupted CPU baseline does not establish
those installation and resumed-computation gates. CUDA image reconstruction,
Agent admission and real NVIDIA recovery remain separate. Keep TRN-108 and
TRN-104 unticked until their full product acceptance is satisfied. While the
CPU profile is unavailable, continue accepted experiment and model-handoff
verification with the retained CPU artifacts under their stated scope.
