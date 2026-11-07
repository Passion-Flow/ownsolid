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

## Failure modes and how the tests cover them

| Failure | Coverage |
| --- | --- |
| A chunk's answer is lost | unit: the same chunk is sent again and accepted |
| The connection drops in mid-upload | browser flow: *Try again* continues from the server's offset |
| A different file is chosen to resume | unit and browser flow: refused, nothing stored |
| The tab closed | browser flow: resuming from the Imports tab |
| No store on the installation | browser flow: the form says why and cannot start |
| Verification fails | browser flow: the failure explained, verify again, discard |
| Retiring a model in use | browser flow: `MODEL_VERSION_REFERENCED` explained, the model stays |
| `crypto.subtle` missing | unit: the fallback gives the same digests |
| A restored database without its model files | installer tests; live run of backup and restore |

## What done means

- A fresh `ownsolid-install apply` and an upgrade in place both give the control plane a working store.
- A person imports the real model from the console with the network disconnected from the registries:
  uploads, sees verification, opens the model, retires it. The same run interrupts and resumes an upload.
- `backup` and `restore` bring a published model back on an installation that lost it.
