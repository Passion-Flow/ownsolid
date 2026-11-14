# Whole-platform licensing

This module owns durable signed platform-license history and current admission. It does not create per-feature subscriptions or replace identity/Workspace permissions.

[service.go](service.go) verifies history, deployment identity, renewal linkage and import expectations before updating state. [configuration.go](configuration.go) owns construction from protected configuration; [diagnostics.go](diagnostics.go), [registry_admission.go](registry_admission.go) and [monitoring.go](monitoring.go) expose bounded owner-specific surfaces.

[inspection.go](inspection.go) runs the checks an import runs and reports each one, without changing history, so the console can show what a file would do and why one is refused. Inspect and Import share `judge`, so they cannot disagree.

Migration 000017 stores license state/artifacts. Import binds expected current license and writes its audit in the same selected transaction. Expiry, grace, missing/invalid history and commit-unknown must preserve existing admission/reconciliation behavior. Diagnostic or recovery exceptions remain explicit; adding a new feature must not introduce a blanket bypass.

Synchronize [contracts/licensing](../../../../../contracts/licensing), [M19](../../../../../docs/milestones/m19-license), protected config/schema and deployment inputs, [licensing frontend](../../../../../web/README.md), offline release trust and dependent owner admission tests.

Focused native check: go -C services/control test ./internal/modules/licensing. The real repository safety/commit-ack tests require a selected isolated SQL fixture; source verification does not establish a customer license installation.

The explicit Dameng branch uses quoted native identifiers, exact singleton locks,
validated constant LIMIT, nullable predecessor binds and binary artifacts. JSON
and provider SQL are not rewritten. It retains signature/history validation and
same-transaction audit through the selected identity owner. Native migration
adds a generated-UUID deployment seed and actual artifact constraints. Syntax
checks do not establish target licensing qualification; account/audit lane
integration and native concurrency/commit-unknown tests remain required.


The explicit `license_issuer_integration` test tag adds the real manufacturer
CLI-to-customer path. Build the current `tools/release/licensing/cmd/license-issuer`
and set `PROJECT_ONE_LICENSE_ISSUER_BINARY` to its canonical executable path,
then select `TestActualNativeIssuerOfflineImportEarlyRenewalAndExactRecovery`
with the ordinary isolated PostgreSQL fixture inputs. It uses only private
synthetic issuer state, actual signed files, SQL/history/audit and an explicitly
injected service clock. It is separate from HTTP/CSRF, browser, native Control
startup and logical restore qualification. No private key is passed to Control.
