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

Run the catalog's local checks from the repository root.

```sh
GOPROXY=off GOSUMDB=off go -C catalog test -race ./...
go -C catalog vet ./...
```

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
