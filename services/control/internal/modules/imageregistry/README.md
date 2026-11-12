# Pinned image registry ownership

This owner coordinates signed/pinned image imports, content-addressed blobs, grants and deployment/offline references. It is not a general registry proxy and must not authorize an arbitrary image merely because its bytes can be downloaded.

[config.go](config.go) defines choices and policy; import, catalog, distribution and pull paths retain signed identity and source constraints. [cas.go](cas.go) and [integrity.go](integrity.go) manage admitted bytes. [deployment_actions.go](deployment_actions.go), reference/grant paths and removal/GC logic consult current consumers before removing retained content. Outcome, stored-state, failure and snapshot code preserve uncertain imports and readback.

Migration 000024 stores pins, blobs, imports, grants, deployment/offline references and mutations. Current identity, Workspace, license, node and deployment owners are dependencies. Registry transport success is distinct from import commitment, deployment readiness and successful target execution. Control and agent registry behavior must agree on the same digest and receipt contracts.

Use [registry configuration examples](../../../config/examples/registry/README.md) and the typed control/agent policies. Keep the [registry policy and problem contracts](../../../../../api/openapi/v1/image-registry), [release contract](../../../../../api/release/v1), [frontend](../../../../../web/README.md), agent/imageregistry and [module profiles](testdata/) consistent. The [offline bundle library](../../../../../tools/release/offline/pkg/bundle) is a production dependency, imported as `github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle`.

Focused unit check: `go -C services/control test ./internal/modules/imageregistry`. Native transport uses the `registry_integration` build tag. The site-admission profile additionally requires `repository_integration`; the operator snapshot profile requires `repository_integration` and `registry_operator_integration`. These profiles exercise different failure boundaries and need their declared database, signed release, storage and native registry fixtures. An ordinary unit run does not execute them or qualify a production registry.

The [operator snapshot API](operator_snapshot.go), [file custody checks](operator_snapshot_files.go) and [repository integration profile](operator_snapshot_repository_integration_test.go) define explicit borrowed-session/all-Workspace CAS export/import and independent byte readback. The snapshot is a distinct supplementary payload, preserves the original four domain stores and browser APIs, and grants no HTTP path or browser actor bypass. The coordinator retains maintenance, physical writer exclusion and completion authority. A historical receipt cannot authorize a different live database session.

Upload session locations are resolved against the initiating POST URL, as
required for a path-relative Location. The resolved URL must stay on the
configured registry and within that repository's upload collection.
TestActualNativeRegistryTransport covers the path-relative case using
a header rewrite over the real TLS Distribution connection and verifies
the blobs by digest after upload.

Import checks the authenticated scope and any existing idempotency key before
verifying the signed asset. It verifies the asset before reserving a new pin.
An idempotent replay of a committed reservation does not reread the release
source. Preparations that fail after reservation use the existing abandon,
discard and reconciliation paths. The
[site release pin boundary](../../../../../docs/research/site-registry-pin-admission.md)
records the separate source and destination and the current-profile verification path.

A control composition can opt in through `NewWithSiteReleaseSource` with
the usual writable tenant registry configuration and a separate protected
JSON source file. The source file contains these fields:

```json
{
  "endpoint": "https://10.42.1.30:5443",
  "allowed_cidrs": ["10.42.1.30/32"],
  "ca_file": "/etc/ownsolid/registry/site-ca.pem",
  "credential_file": "/etc/ownsolid/registry/site-reader.json"
}
```

Use an HTTPS endpoint and a private allowlist. The credential file contains
the site's read-only Basic username and password and is readable only by
the service owner. Use a different credential pair and origin for tenant
registry writes. Keep the verified prepared release mounted for signed
asset selection and offline archive export. The source reader fetches only
signed content by digest. An unavailable or invalid source stops preparation.
A complete verified cache can be reconciled after a failed SQL bind while
the site is unavailable. An incomplete cache uses the existing abandon and
discard flow before a new import.

The signed profile selects the OCI reader for import and reconciliation.
Shared control startup still needs to pass the protected source file.
To exercise the module boundary on Linux, use
[the loopback runner](testdata/run-site-admission.sh) with the verified
Distribution binary, its SHA-256 and a local PostgreSQL image ID.
