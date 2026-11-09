# Offline application catalog

Each application release belongs in `apps/<id>/<version>/`. The
`github.com/Passion-Flow/ownsolid/catalog/apps` Go package reads a release
from a local directory with `apps.Load`. Open an `os.Root` confined to the
immutable release directory.

## Manifest v1

Supply `manifest.json`, `locales/en.json` and `locales/zh-cn.json`. Each is
one strict UTF-8 JSON object of at most 64 KiB. The fields below define the manifest.

| Field | Meaning |
| --- | --- |
| `schema_version` | Format revision. Use `1`. |
| `id` | Stable lowercase identifier, at most 63 characters. Use hyphens between words. |
| `version` | Application release in stable `major.minor.patch` form. Each component is at most nine digits. |
| `platform` | Stable versions in `minimum` (inclusive) and `before` (exclusive). |
| `artifact` | `engine` selects `helm` for `chart.tgz` or `compose` for `compose.yaml`. `sha256` identifies the artifact bytes as `sha256:` followed by 64 lowercase hex digits. |
| `images` | One to 128 distinct OCI image or index SHA-256 digests available to the offline installer. |
| `upgrade_from` | Optional list of up to 32 distinct older versions, each with a `version` and the exact signed `bundle_sha256`. |
| `form` | Up to 32 fields in display order, or an empty array. |

Each field has an `id`, a `type` and a `required` boolean. Types are
`text`, `integer` (signed 64-bit JSON integer), `boolean` and `choice`.
Choice fields supply one to 32 distinct identifiers in `choices`. Optional
values omitted from a submission stay absent, so explicit `false` and `0`
are preserved. Render display text as plain text. Form data excludes credentials.

Both locale files contain a `title`, a `description` and a `fields` object
keyed by the same field identifiers as the form. Each field supplies a `label`
and a `description`. For a choice field, `choices` maps each option identifier
to its label. Use an empty object for `fields` when the form has no fields.

An optional `license_note` gives the publisher's release-specific license and
attribution summary. Supply it in both locales as plain text of up to 2,048
Unicode code points, or omit it in both. The signed bundle authenticates both
summaries. They are available on the verified release's locale messages.

After loading a manifest, call `Manifest.Platform.Supports` with the installed
platform version and `Manifest.DecodeValues` with a configuration submission.
The catalog package reads metadata and validates configuration without starting
workloads. The installer must verify bundle provenance, the artifact and images,
and admit the rendered workload before running it. Prepare all charts and image
archives before delivery. Runtime pulls, builds and chart dependency downloads
are disabled by the deployer.

## Dependency bindings

The optional `dependencies` array declares up to 32 required slots. Each
object has a distinct `id`, a `kind` and a `protocol`. The same name cannot
also be used as a form field.

| Kind | Protocols |
| --- | --- |
| `model` | `chat`, `embeddings`, `rerank` |
| `database` | `postgresql`, `mysql`, `redis` |
| `storage` | `s3` |

The installer resolves the resources through their owners in the authenticated
tenant and workspace. The secret owner issues a reference tied to the resource,
dependency slot and installation. `Manifest.Bind` checks those records and
returns a plan in manifest order. It requires a different secret reference for
each slot. The owner rechecks grants and revocations before the installer mounts
the resolved files read-only under a slot-specific directory. The installer
controls the parent directory and selected containers. Manifest authors supply
neither credentials nor endpoints, and ordinary configuration values cannot
override a binding.

## Signed bundle inventory

An application bundle staged for verification adds `bundle.json`,
`bundle.sig` and `images.tar` alongside the manifest, locale files and
selected engine artifact. The index has `schema_version` set to `1` and
four SHA-256 digest strings.

| Index key | File it covers |
| --- | --- |
| `manifest` | `manifest.json`, which carries the engine artifact digest |
| `en` | `locales/en.json` |
| `zh_cn` | `locales/zh-cn.json` |
| `images` | `images.tar`, the local OCI image archive |

Create a detached binary Ed25519 signature over the bytes of
`apps.BundleSigningDomain` followed by the exact bytes of `bundle.json`.
Preserve those bytes through transfer. Configure the publisher public key
separately from the bundle. `apps.VerifyBundle` accepts that key, an
`os.Root` for a private immutable staging directory, and a combined byte
limit for the engine input and image archive. It checks signature, manifest,
both locales and both payloads before returning release metadata.

For catalog browsing, call `apps.ListReleaseMetadata` with the authorized app
ID, its immutable directory and its trusted publisher key. It returns each
signed manifest, both locales and its exact release pin in descending
version order. A missing or invalid metadata entry fails the read. The list
does not open engine artifacts or image archives. Verify the selected pin
and payloads at installation time.

Use `apps.VerifyInstallBundle` for the Linux amd64 installation path. In addition
to the signature and payload checks it requires a complete OCI image layout in
`images.tar`, with direct amd64 manifest digests exactly matching
`Manifest.Images`. Pack the root index, layout metadata and all referenced
manifest, configuration and layer blobs. Use USTAR headers or PAX headers with
only a size extension. Include regular files and the optional `blobs/` and
`blobs/sha256/` directories. Pack only the runtime closure for that platform.
Layer compression may be gzip or uncompressed. The returned `Archive` carries
the signed digest and byte size, and the manifest and config digests for
each image in OCI index order.

## Installation and version transitions

The installer records `InstallBundle.Pin` with a successful installation.
It contains the manifest version and the SHA-256 of the exact signed
`bundle.json` bytes. For a fresh install, call `apps.VerifyInitialRelease`
with the selected app and pin, a trusted publisher key and the running
platform version. It verifies the selected version directory and checks the
signed app identity and platform range. Reuse the same pin on retries.

Publish a new version when any signed content changes. An edge in the
new manifest's `upgrade_from` names the retained pin of an approved predecessor.

Call `apps.VerifyReleaseTransition` with an immutable root containing one
app's version directories, the trusted publisher key, the running platform
version and the installed pin. It selects the unique direct upgrade from
that pin. Use `UpgradeTo` to require the same choice on a retry. A named
target does not override a fork. For rollback, pass the exact predecessor pin
from installation history in `RollbackTo`. Both endpoints must support the
platform and use the same engine. The selected artifact and signed OCI
closure pass the same verification as a fresh install. An upgrade also
verifies the complete installed bundle for recovery. The byte budget
applies to each verified bundle.

Pass the result's `Pin` as `ImageImportOptions.ExpectedPin` and use its version
directory for the import. This binds image registration to the selected
artifact, even if another signed bundle claims the same version. Keep
staging private and immutable. Installation history, permissions, audit,
workload execution and configuration or data recovery belong to the installer.

## Local image import

`apps.ImportInstallImage` verifies a signed Linux amd64 release and confirms
every declared manifest in a configured local Docker Unix socket. Configure
Docker Engine API v1.49 with its containerd image store and provide the
compressed and expanded byte budgets and an operation timeout. The factory
selects one direct platform manifest per load and streams its tagless OCI
transport from the original archive.

The import checks expanded layers and DiffIDs for the full set before using
Docker. The expansion budget applies across the set and counts a shared
layer once per use. Keep the bundle private and immutable on the configured
factory mount for the entire operation.

The importer reads back every image ID, manifest descriptor and platform.
When an image fails, retry the same signed bundle. Exact images already
registered are reused, and a failed operation returns no successful bundle
result. Use each returned `Archive.Images[i].ManifestDigest` as a digest-only
image reference in the deployment plan. `Archive.Digest` identifies the signed
archive bytes. The caller authorizes and audits the operation. The installer
owns release state, workload admission and startup.

## Rendered Compose images

Render the verified `compose.yaml` in the installer's disconnected sandbox
with access to the signed artifact and approved inputs only. Supply the
source artifact digest and exact `docker compose config --format json`
output as `RenderedCompose`. Include all services that the installer can
start. The first path accepts up to 256 services and 1 MiB of normalized JSON.

`InstallBundle.VerifyComposeImagePlan` checks the source digest and each
service's signed Linux amd64 image digest and explicit `pull_policy: never`.
It refuses builds, provider and init containers, models, watch definitions,
unresolved includes and image mounts. Supply approved non-secret environment
values as explicit strings in a normalized environment object. An empty
string is explicit, and an unset key is omitted. Supply labels inline.
The catalog refuses `env_file`, `label_file` and environment entries inherited
from the process. Keep the resulting release pin, signed
source SHA-256, model SHA-256 and ordered service image bindings in the
trusted admission record for that scoped installation. Pass the retained
model and plan through `ImageImportOptions.RenderedCompose` and
`ImageImportOptions.ExpectedComposePlan`. Import rederives the complete plan
from the staged signed bundle and rejects any mismatch before Docker access.

Before startup, call `ComposeImagePlan.VerifyRendered` with the private
buffer that the installer will pass to its launcher. It applies the same
service input rules to retained plans. Keep the approved project directory,
profiles and interpolation inputs fixed through launch. Disable implicit
project `.env` loading and launch-time environment file overrides.
Deliver credentials through admitted file references. Compose
accepts the buffer as the sole configuration via `-f -` and supports
`up --pull never --no-build`. The installer is responsible for preserving
the admitted model through Compose evaluation and recording the outcome
against the same installation.

## Verification

Run the catalog's local checks from the repository root.

```sh
GOPROXY=off GOSUMDB=off go -C catalog test -race ./...
go -C catalog vet ./...
```


For each Docker integration test, configure the socket of a new disposable
empty engine and a separate private staging mount. The multi-image test
injects a second-load failure and checks that retry loads only the missing
manifest. Dispose of the test engine after each run.

```sh
export OWNSOLID_CATALOG_DOCKER_TEST_SOCKET=/path/to/private/docker.sock
export OWNSOLID_CATALOG_DOCKER_TEST_MOUNT=/path/to/private/staging-mount
go -C catalog test -tags catalog_docker_integration ./apps -run '^TestImportInstallImageIntoEmptyDockerStore$'
# Point the same variables at a fresh engine and mount for the set test.
go -C catalog test -tags catalog_docker_integration ./apps -run '^TestImportInstallImageSetIntoEmptyDockerStore$'
# Use a fresh engine for the upgrade and rollback import fixture as well.
go -C catalog test -tags catalog_docker_integration ./apps -run '^TestImportVerifiedReleaseTransitions$'
# With Compose installed and another empty local engine.
go -C catalog test -tags catalog_docker_integration ./apps -run '^TestImportVerifiedRenderedCompose$'
```
