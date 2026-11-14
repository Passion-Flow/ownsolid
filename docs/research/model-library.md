# The model library

Research behind DEL-117 (the installer's model store) and MOD-119 (the Model library page), screens
S-201 and S-202 of the [build plan](../product/plan/README.md).

## What exists

The control plane has one complete path: a scoped, resumable import of the approved model
(`api/agent/v1/model-catalog.json`), verified byte by byte, published into an immutable store and
retired behind a reference check. Its contract is `api/openapi/v1/models`. The catalogue holds one
entry. MOD-113 to MOD-116 replace it with an index of both registries; this note covers only what
works today, and a page built on it must say so.

An installed product could not use any of this: `ownsolid-install` set no `models.artifact_root`, so
the store did not exist and an import could not start.

## How others do it

- **Model registries.** [MLflow](https://mlflow.org/docs/latest/ml/model-registry) gives a registered
  model a name, versions, lineage to the run that produced it, and aliases.
  [Kubeflow's registry](https://www.kubeflow.org/docs/components/model-registry/overview) separates a
  registered model, its versions and the artifact each points at, and stores metadata only. Both make
  the version page the real thing, with its origin on it. S-202 follows them: name, licence as stated,
  the files with their hashes, and where the model came from.
- **Resumable upload.** [tus 1.0](https://tus.io/protocols/resumable-upload) finds where to continue by
  asking the server for its offset, and refuses a write at any other offset with 409 and no change
  ([specification](https://www.github.com/tus/tus-resumable-upload-protocol/blob/main/protocol.md)).
  Our chunks work the same way: `uploaded_bytes` is the offset, `MODEL_CHUNK_OFFSET_MISMATCH` the
  refusal. The server stays the authority, and a client never remembers an offset across a failure.
- **Hashing in the browser.** [`SubtleCrypto.digest`](https://developer.mozilla.org/docs/Web/API/SubtleCrypto/digest)
  exists only in secure contexts. The installer always serves HTTPS, but the control plane also has an
  explicit plain-HTTP transport, and a page must not fail there.

## Licences

- `@noble/hashes` 1.8.0: MIT, no dependencies, audited, runs offline. It is loaded only where
  `crypto.subtle` is missing, so an HTTPS page never downloads it.
- The test model, Qwen3-0.6B, is Apache-2.0. It is fetched for the run and never enters the repository.

## The store in the installer (DEL-117)

- **Where.** `data/models` under `install_root`, mode 0700, owned by the runtime user, bound into the
  control container at `/var/lib/ownsolid/models`. The store opens only for its owner and refuses group
  or other access, so nothing else may share the directory.
- **Only serving gets it.** The control plane refuses a store while no administrator exists
  (`BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES`), and the operator tasks that create the first one read
  `operator.yaml`. So `models` is written to `control.yaml` only, and it joins the keys `control_settings`
  may not set.
- **Backup holds `objects/`, not `imports/`.** Startup verifies every ready model against the store, so
  a restored database without its files would leave serving down. Published objects never change, which
  makes copying them from a running installation safe, and copying after the dump means every model the
  database names is already there. An unfinished upload has meaning only to the client sending it.
- **Rejected:** the `export-model-files` operator command. It verifies well, but needs the store and the
  backup directory inside a one-off container and a second format beside the manifest, while the manifest
  already hashes, sizes and restores owned files.
- **Restore** puts back what is missing or damaged, before the database changes, and leaves what matches,
  so a restore on the same host does not rewrite the store. It runs on a running installation as well as
  an empty one, because the store can be lost alone.

## The page (MOD-119)

- **Route.** `/library`, the navigation's existing *Model library*. *Model services* stays for S-204.
- **Scope.** Models belong to a workspace, so the page follows the workspace switcher.
- **Your models** is the list, with a name search and a kind filter from the address, applied in the
  browser: the server returns a workspace's whole directory, which is bounded by its import limit.
- **Imports** lists what is arriving and what failed. A failed import blocks the next one
  (`MODEL_IMPORT_BUSY`), so its page offers both ways out: verify again, or discard.
- **Catalogue** shows what this release can import, and says plainly that searching both registries
  arrives with MOD-113 to MOD-115.
- **Import a bundle** is a flow of one page, as it has fewer than four steps, and states the space it
  takes before the commit. A person who has no bundle is told how to make one.
- **Upload.** One chunk at a time, in order: the server accepts only the next offset. A failure that can
  be retried is retried a bounded number of times, re-reading the offset each time. A lost answer is safe,
  as sending a stored chunk again with the same hash is accepted.
- **Resume.** Nothing can reopen a file for the browser, so a person chooses it again. The size must match,
  and the last stored chunk is sent again first: a different file then fails with `MODEL_CHUNK_CONFLICT`
  before a byte of it is stored. The upload lives outside the page, so moving around the console does not
  stop it; closing the tab warns first.
- **Deploy** on a model's page is not ready until SRV-111, and says so.
- **Cost (MOD-120).** The form shows the approved files' size before a bundle is chosen, and an
  estimate of the selected bundle plus those files afterward. Both copies need room during import.
  The estimate does not stand in for a free-space check. The [worker research](model-import-cleanup.md)
  separates publication from cleanup, following the [S3 multipart lifecycle](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html).
- **Publication and cleanup.** The import read exposes `state`, `model_id`, `uploaded_bytes` and
  `error_code`, with no field for post-publication cleanup. Once the API returns `ready` and a model
  ID, show the publication and make Open the model the primary action. The import record alone
  supplies its status. Read the catalogue only when resuming an upload or offering a new
  verification attempt needs its limits. If that read fails, keep the known state visible and
  explain which control is unavailable. The upload step shows activity when this tab is
  sending, and preserves the server's byte count when the upload is incomplete. A persistent note explains
  background cleanup and automatic retry. The worker can retain staging after a failed attempt.
  The UI keeps the publication timeline at three steps and shows no cleanup progress, completion,
  released bytes or manual retry. Upload counters and timestamps are historical. A verification
  failure still uses the existing Verify again and Discard actions. The operator diagnoses a
  background failure from `model_import_cleanup_failed` and the import ID in the service logs.
  The console and bundled help use the same distinction. No new dependency is needed.

## Making an import bundle (MOD-101, MOD-119)

The [GNU tar manual](https://www.gnu.org/software/tar/manual/tar.html#ustar) specifies
`--format=ustar` for POSIX USTAR. [Python's tarfile documentation](https://docs.python.org/3/library/tarfile.html)
provides the explicit `USTAR_FORMAT` writer. The [Go tar reader](https://go.dev/src/archive/tar/reader.go)
also checks header encoding when deciding which format it read. The
[model verifier](../../api/agent/modelartifact/archive.go) requires Go to classify each
header as USTAR and accepts only the regular files in the approved manifest.

On the Mac, all nine files and 1,519,207,673 payload bytes in the existing Qwen3-0.6B
archive matched the pinned sizes and SHA-256 values. With networking denied, the old
`COPYFILE_DISABLE=1 tar --format=ustar` command using macOS bsdtar 3.5.3 produced
headers Go classified as unknown and the model verifier returned
`MODEL_ARCHIVE_INVALID`. Python 3.12 with `tarfile.USTAR_FORMAT` and GNU tar 1.34
and 1.35 in local Linux containers produced nine USTAR entries and passed the shared
verifier. GNU tar 1.34 produced identical archives when invoked as `tar` and as
`gtar`. All three commands copied from Chromium also passed when executed with
networking denied. An unlisted file beside the model was excluded by the explicit file lists.
A canonical USTAR that explicitly included that file was refused with
`MODEL_ARCHIVE_INVALID` before the file entered the stage.

Generate the commands from the current catalogue's file list and quote each filename
for a POSIX shell. Show separate copy controls for Python 3, GNU `gtar` on macOS,
and GNU `tar` where that name resolves to GNU tar. The bundled help points to the
revision-specific commands, so it stays accurate when the approved list changes.
Archiving the directory or using a wildcard can add directories, download caches or
other unlisted entries. BSD tar and permissive server parsing would leave a mismatch
with the verified format. The current single-entry catalogue remains the authority.

GNU tar is [GPL-3.0-or-later](https://www.gnu.org/software/tar/). CPython's standard library
uses the [PSF License Version 2](https://docs.python.org/3/license.html). Both allow
commercial and offline use and redistribution under their respective terms. These
commands use tools already installed on the preparation machine. ownsolid does not
bundle either tool for this feature and adds no runtime dependency or data set.
The approved Qwen fixture retains its Apache-2.0 licence.

## Model detail lineage (S-202, MOD-103, DAT-106)

[MLflow Model Registry](https://mlflow.org/docs/latest/model-registry) links a model version
to the run or artifact that produced it. [SageMaker model lineage](https://docs.aws.amazon.com/sagemaker/latest/dg/model-dashboard-lineage.html)
traces the model through its training job to its input data. These are design references.
No dependency, model, dataset, or redistributed asset is added. The browser fixtures use
the pinned model metadata with synthetic derivative and dataset identifiers and digests.
See the [licence inventory](training-dataset-lineage.md#failure-coverage)
for the retained-source test stack.

Use the [Admin detail response](training-dataset-lineage.md#dev-contract-handoff) through the
existing model query. The generated response type is the authority for both inputs. Keep the
existing origin link to the base model or the import record on the Versions and lineage tab.
Give the training dataset and preparation holdout separate groups, with each original dataset
ID, version ID and manifest SHA-256. Resolving current dataset details would hide retired inputs
or substitute newer versions. A graph or a second lineage endpoint would require relationships
and authority outside this slice. Keep the existing Deploy action and share the tab through its
URL.

An older or imported model can omit lineage while its origin is available. Explain that state
without filling in values. The detail page retains its loading, missing, denied and retry
behavior. The server refuses an inconsistent retained graph with
`MODEL_DERIVED_CATALOG_INVALID`. The browser displays the refusal and keeps permission
enforcement on the Admin endpoint.

Browser coverage verifies both languages, exact values in the two groups, links to the base model
and import, the missing-field state, and the existing denial and retry path. The installed
acceptance reads a derived model from the owning test environment using the normal Admin route.
Compare both triples with the retained fixture values and check the rendered page over the same
installation. The opt-in browser case takes `OWNSOLID_LIVE_MODEL_LINEAGE_FILE`, a local JSON
fixture with `model_id` and the two expected `dataset_lineage` references from the publication
fixture, plus the existing live Console settings.

### Verified Admin read

Chromium and `TestActualAdminModelDetailDatasetLineageHTTPS` shared one model owner's
loopback HTTPS dispatcher and PostgreSQL publication fixture. The browser matched both
dataset triples after the training version was retired, followed the base model link, and
saw the localized absence state for that base model. An unsigned read returned 401.
After a browser transport interruption, retry read the same model through the actual
Admin route. The browser made only same-origin requests. The owner test also verified
that Portal and list responses omit lineage, that Portal cannot use the Admin detail route,
and that an inconsistent retained graph returns `MODEL_DERIVED_CATALOG_INVALID`.
This acceptance uses synthetic publication metadata under the normal database constraints.

### Remaining roadmap evidence

The [retained CPU acceptance](training-dataset-lineage.md#retained-cpu-acceptance)
also verifies the actual derived files, their persisted copy, and the authorized
transfer ranges. It used synthetic publication approval. The installed product
flows below still need evidence from publication through the corresponding views.

- **MOD-103** requires an end-to-end model version flow. Show multiple retained versions
  of the same model and trace each version's weights, adapters, tokenizer and template
  to its immutable artifacts, producing training run and version-bound evaluation report.
  Verify those links and permissions in the installed UI with the network disconnected.
- **DAT-106** requires an end-to-end dataset-to-run-to-model flow. Publish successive
  dataset versions, complete training, preparation and product approval, and compare
  the frozen version and manifest on the dataset, training run, preparation and published
  model views. After
  retiring the original source, retry the existing run with that exact input and verify
  a new run cannot select the retired version. Exercise the installed flow offline,
  including cross-workspace denial. The model detail read proves the final projection
  from retained metadata.

## Failure modes and how the tests cover them

| Failure | Coverage |
| --- | --- |
| A model bundle uses the wrong tar implementation or contains an extra file | Real pinned files: compare the archive headers and run the shared model verifier with the network blocked. Browser: copy the exact commands in both languages |
| A chunk's answer is lost | unit: the same chunk is sent again and accepted |
| A published import still has upload records | Browser flow: the model opens after reload, with a background cleanup explanation and no fourth timeline step |
| The catalogue read is unavailable | Browser flow: a published record opens without a catalogue request. A paused upload keeps its progress, explains why it cannot resume, and recovers after a retry |
| A verification attempt fails before publication | Browser flow: verify or discard with the existing controls, without a published notice |
| The connection drops in mid-upload | browser flow: *Try again* continues from the server's offset |
| A different file is chosen to resume | unit and browser flow: refused, nothing stored |
| The tab closed | browser flow: resuming from the Imports tab |
| No store on the installation | browser flow: the form says why and cannot start |
| Verification fails | browser flow: the failure explained, verify again, discard |
| Retiring a model in use | browser flow: `MODEL_VERSION_REFERENCED` explained, the model stays |
| `crypto.subtle` missing | unit: the fallback gives the same digests |
| A restored database without its model files | installer tests; live run of backup and restore |
| The control process stops in mid-extraction | live: the import fails with `MODEL_STAGE_INVALID`, and *Verify again* unpacks into a fresh stage and publishes |

## What done means

- A fresh `ownsolid-install apply` and an upgrade in place both give the control plane a working store.
- A person imports the real model from the console with the network disconnected from the registries:
  uploads, sees verification, opens the model, retires it. The same run interrupts and resumes an upload.
- `backup` and `restore` bring a published model back on an installation that lost it.

## Cancelled import steps (S-201)

The live first-chunk discard showed a Discarded import with a Failed
upload step. The stored import state distinguishes cancellation from a
verification failure. [GitHub check conclusions](https://docs.github.com/en/rest/checks/runs)
likewise distinguish cancellation from failure. Keep completed steps
and mark the step that was stopped with a neutral icon and a translated
Cancelled label. This follows [WCAG use of color](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color)
and the existing step timeline. Treating a stopped step as pending
would suggest that the import can still run. The live early-discard
and Chinese completed-upload cases cover the two positions. No new
dependency is needed.
