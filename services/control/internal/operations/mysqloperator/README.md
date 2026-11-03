# Selected MySQL operator authority

This package is a shared proof and protected-file boundary for [bootstrap](../bootstrap/README.md), [database access](../databaseaccess/README.md), [store initialization](../storeinitialization/README.md), and [native backup/restore](../mysqlbackup/README.md). It is not a second connection pool or an independent operator command. [Selected operator construction](../../../cmd/server/selected_operator.go) remains with the control command layer.

## Borrowed resources and identity

[Context](context.go) borrows the configured MySQL kernel, actual SQL connection, transaction store, and held installation lock. It does not adopt or close caller-owned resources. Verification binds actual server UUID, database, authenticated principal, server version, deployment identity, schema/catalog state, and the selected configuration. A caller-provided target string is insufficient authority.

[Connector-input verification](connector_inputs.go) checks the protected configuration/password/CA bytes actually consumed by the opened connector. It proves connector inputs and session properties, not installation readiness or completed migrations. [Runtime verification](runtime.go) separately checks the restricted login against the same target and applicable ledger binding.

[Fresh](fresh.go) represents a locked connector before a restored installation can be proven. `RequireEmpty` and `Promote` are separate operations. Creating a Fresh value does not establish database emptiness, restore completion, or usable runtime grants. Verification rechecks connection identity and protected input fingerprints rather than trusting a stale construction-time assertion.

## Protected files and unknown writes

[files.go](files.go) owns strict bounded document decoding and protected file creation/readback. Inputs require canonical absolute paths and the enforced private parent/file ownership, permissions, and link constraints. Descriptor/name and parent checks detect replacement. Writes are exclusive, synced, and read back; credential-bearing bytes must not enter diagnostics.

An uncertain exclusive write may have left the intended file. Preserve it and use the owning operation's reconciliation procedure; do not overwrite it or delete it as a generic retry step. A changed configuration, CA, password file, lease, or target invalidates the old proof. Reopening the connector is necessary where inputs changed, but is not by itself permission to repeat a mutation.

## Extension and checks

Keep domain mutation policy in sibling operations. This package may prove selected authority and protected input integrity; it must not create application users, grant arbitrary runtime privileges, or commit a transaction borrowed from another owner.

From `services/control`, run `go test ./internal/operations/mysqloperator` within an admitted isolated test scope. [Private-file tests](files_test.go), [fresh-target tests](fresh_test.go), and [runtime selection tests](runtime_selection_test.go) exercise their declared proof/refusal boundaries. Real MySQL lock, authentication, TLS, and restore qualification still require their actual integration profile; mocks or file tests cannot establish them. No runtime checks were run for this guide.
