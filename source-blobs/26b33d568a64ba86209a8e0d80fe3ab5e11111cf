# Signed release contracts

This Go module owns the signed release manifest and its strict JSON, the asset identity and
license rules, and the PostgreSQL backup and restore request formats. The factory in
`tools/release/offline` and the control plane consume them. This library does not build an
image, create a package or install anything.

[`Manifest.Validate`](v1/manifest.go) checks bounds and required assets. There is one release
profile, `mvp-postgresql-cpu-v1-linux-amd64`, and one PostgreSQL lineage it binds through the
[versioned descriptor](v1/mvp-postgresql-cpu-v1-lineage.json). The fixture profile exists for
tests and is admitted only when a test allows it.

The installer does not use signed releases yet (ADR 0004). DEL-101 replaces the profile's
name and its milestone capability list when signed bundles come back.

`releasetest` builds synthetic manifests for tests. They describe metadata only.

```sh
go -C api test -count=1 ./release/...
```
