# Verified offline image loading

[loader.go](loader.go) admits actual signed, tagless OCI archives into the existing protected Docker owner. It verifies the release/asset identity and configured root/budgets before loading; image availability does not prove GPU execution or authorize an arbitrary workload.

Config owns private import root, real Docker data root, verifier/trust inputs, archive/expanded/response bounds, operation deadline and storage headroom. Admission accounts for archive copies and expanded bytes rather than checking only the final file size. It rejects fixture-verification policy in the production path. [journal.go](journal.go) and [recovery.go](recovery.go) retain exact import, load and receipt identity across uncertain outcomes.

The loader borrows [client](../client/README.md), [inference](../inference/README.md) Docker transport and the shared offlinefactory bundle verifier. Runtime chooses the explicit task or operator operation. Cleanup must preserve required imported images, original failure receipts and active consumers; it is not permission to prune Docker globally.

Synchronize control/imageregistry, [release contracts](../../../../contracts/release), [registry examples](../../../../config/examples/registry/README.md), agent registry operations and actual import/recovery tests. Focused check: `go -C services/agent test ./internal/imageregistry`. Native Docker/image readback is a separate admitted integration profile; parsing a signed archive alone does not prove the daemon loaded the expected image.
