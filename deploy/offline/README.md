# Offline delivery

`cmd/install` is `ownsolid-install`, the install path; see
[Install](../../docs/operations/install.md) and
[`install.example.yaml`](install.example.yaml). `internal/install` holds its typed
configuration and steps.

```sh
go -C deploy/offline build -o ownsolid-install ./cmd/install
go -C deploy/offline test -count=1 ./...
```
