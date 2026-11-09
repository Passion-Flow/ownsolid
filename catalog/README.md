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

## Local image import

`apps.ImportInstallImage` verifies a signed single-image Linux amd64 release
on the factory's private staging mount and streams its tagless image into
a configured Docker Unix socket. The current factory transport takes a USTAR
archive with a direct platform manifest. Configure Docker Engine API v1.49
with its containerd image store and provide the compressed and expanded byte
budgets and an operation timeout.

The import checks expanded layers and DiffIDs, then reads back the image ID,
manifest descriptor and platform. A retry can reuse the exact registered
image. Use the returned `Archive.Images[0].ManifestDigest` as the digest-only
image reference in the deployment plan. `Archive.Digest` identifies the signed
archive bytes. Keep staging immutable throughout. The caller authorizes and
audits the operation. The installer owns release state, workload admission
and startup.

## Verification

Run the catalog's local checks from the repository root.

```sh
GOPROXY=off GOSUMDB=off go -C catalog test -race ./...
go -C catalog vet ./...
```


For the real Docker integration test, configure the socket of a disposable
empty engine and a separate private mounted staging directory. The test
checks that the store is empty, imports a fixture, retries it and confirms
the exact signed digest. Dispose of the test engine after the run.

```sh
export OWNSOLID_CATALOG_DOCKER_TEST_SOCKET=/path/to/private/docker.sock
export OWNSOLID_CATALOG_DOCKER_TEST_MOUNT=/path/to/private/staging-mount
go -C catalog test -tags catalog_docker_integration ./apps -run '^TestImportInstallImageIntoEmptyDockerStore$'
```
