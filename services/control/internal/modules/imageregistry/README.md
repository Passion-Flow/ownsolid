# Pinned image registry ownership

This owner coordinates signed/pinned image imports, content-addressed blobs, grants and deployment/offline references. It is not a general registry proxy and must not authorize an arbitrary image merely because its bytes can be downloaded.

[config.go](config.go) defines choices and policy; import, catalog, distribution and pull paths retain signed identity and source constraints. [cas.go](cas.go) and [integrity.go](integrity.go) manage admitted bytes. [deployment_actions.go](deployment_actions.go), reference/grant paths and removal/GC logic consult current consumers before removing retained content. Outcome, stored-state, failure and snapshot code preserve uncertain imports and readback.

Migration 000024 stores pins, blobs, imports, grants, deployment/offline references and mutations. Current identity, Workspace, license, node and deployment owners are dependencies. Registry transport success is distinct from import commitment, deployment readiness and successful target execution. Control and agent registry behavior must agree on the same digest and receipt contracts.

Use [registry configuration examples](../../../../../config/examples/registry/README.md) and the typed control/agent policies. Synchronize [M24](../../../../../docs/milestones/m24-image-registry), [frontend](../../../../../web/README.md), release/HTTP contracts, agent/imageregistry and [quality harnesses](../../../../../tools/quality/registry). The imported `projectone/offlinefactory/pkg/bundle` library is a production dependency even though its source lives under tools/release.

Focused native check: `go -C services/control test ./internal/modules/imageregistry`. The native-transport, stored-state and runtime-repository integration sources exercise different failure boundaries and need their declared fixtures. Do not replace those profiles with a mocked successful pull.

The [installation operator snapshot](../../../../../docs/milestones/m24-image-registry/operator-snapshot.md) adds explicit borrowed-session/all-Workspace CAS export/import and independent byte readback. It is a distinct supplementary payload, preserves the original four domain stores and browser APIs, and grants no HTTP path or browser actor bypass. The native owner retains canonical maintenance/physical stop and completion authority.
