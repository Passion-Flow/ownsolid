# Private Registry configuration boundary

This package owns the control-side private Registry configuration type, defaults, protected-file loader and validation. The [image-registry module](../../modules/imageregistry/README.md) aliases this type; it does not maintain a second independent configuration schema.

## Loading and authority

[config.go](config.go) exposes `Defaults`, `Decode`, `Load`, `LoadEnvironment`, `Validate` and `OperationTimeout`. Pure `Decode` applies owned defaults and rejects unknown/duplicate/null/trailing values through strict validation. `Load` uses the production-shared offline bundle library, then delegates to `Decode`. That library's [pinned-input repair](../../../../../docs/engineering/deployment-transport/release-inputs.md) has a separate source and executable acceptance gate; source presence does not establish that gate passed. `LoadEnvironment` accepts the explicit map containing `PROJECT_ONE_REGISTRY_CONFIG_FILE`; it does not read ambient environment.

[The central selector](../config/imageregistry.go) resolves `image_registry_config_file` using its own bounded1MiB pinned private snapshot reader, then calls `Decode`. It does not use the legacy `Load` file reader. Omitting the selector disables that component; malformed/empty references are not a valid enabled configuration. The central configuration boundary also rejects overlap with active model, dataset, training or preparation roots. Reconfigure through the documented protected input and restart/reload procedure of the parent composition, not by mutating a request or this snapshot.

## What the policy binds

The endpoint must be HTTPS with no credentials, query or fragment. Allowed network prefixes must fit entirely inside permitted private/loopback ranges. CA, credential, CAS, prepared-release and verification/trust references must be canonical absolute paths. CAS and prepared-release roots are distinct nonoverlapping descendants of the verification mount, and release archive/manifest hashes must be explicit. Test-fixture verification is rejected by this production configuration.

Byte limits account for archive, expansion, blobs and retained copies; policy also bounds pins, references, concurrent imports, headroom, operation/request/grant lifetimes and pagination. The configuration enforces relationships such as retained space covering archive coexistence and request/grant lifetimes fitting the operation. It does not itself verify a live mount, connect to Distribution, import an image or reconcile an interrupted import.

The [configuration examples and mount guide](../../../../../config/examples/registry/README.md) own the JSON schema, component env mapping and Compose/Helm seam. The imported [bundle library](../../../../../tools/release/offline/pkg/bundle) is a runtime dependency despite its repository location under tools; do not omit it from the source/release boundary.

## Failure, changes and verification

Invalid inputs produce stable `REGISTRY_CONFIGURATION_*` errors without exposing credentials. Fix the protected source and re-enter the parent loader. Runtime import/transfer uncertainty must be resolved by the image-registry owner; changing these settings does not retroactively validate retained artifacts or authorize cleanup.

The focused real configuration tests live in [config/imageregistry_test.go](../config/imageregistry_test.go); [probe_decode_test.go](probe_decode_test.go) adds strict pure-decoder controls. Protected main/Registry snapshot and CLI refusal tests are frozen in the [health-probe contract](../../../../../docs/engineering/deployment-transport/health-probe.md):

```sh
go -C services/control test ./internal/platform/config -run TestImageRegistry
```

They cover omission, env precedence, malformed YAML/private JSON and store separation. Runtime Registry and node-image acceptance remain separate. Keep the control schema, documented examples, deployment mounts and consumer aliases synchronized when a field or bound changes.
