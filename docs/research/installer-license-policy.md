# Installer license import policy

## Sources and current behavior

[Elastic's self-managed license administration](https://www.elastic.co/docs/deploy-manage/license/manage-your-license-in-self-managed-cluster)
uses an authenticated management page or API to apply a license file.
[GitLab's offline activation](https://docs.gitlab.com/administration/license/#for-offline-environments)
also selects a transported license file. ownsolid retains its existing
whole-platform file format and server authorization.

The owning [Control policy decoder](../../services/control/internal/platform/config/licensing.go)
already reads runtime.licensing.maximum_imports and supplies the shared
[policy defaults and bounds](../../pkg/license/v1/policy.go): 64 imports by
default, with valid limits from 1 to 1024. Its YAML decoder retains the other
policy defaults when only that setting is supplied.
The [installer renderer](../../deploy/offline/internal/install/materialize.go)
copies control_settings into the Control document and owns the separate
top-level licensing trust reference. It does not overwrite runtime.
Therefore control_settings.runtime.licensing.maximum_imports is already an
installer input. The statement that a hand edit of control.yaml is required
does not describe this implementation.

## Decision and verification boundary

Document the existing install.yaml input and test its values through real
materialization, repeat materialization and an explicit change. Keep unrelated
runtime settings, the protected trust reference and existing credentials intact.
A generated-file edit must be replaced by the next materialization from the
same authoritative install.yaml. Test serving and operator configurations.

A second installer policy field was rejected because it would create two
owners for the same existing runtime setting. Changing the signed wire,
resetting import history or introducing an issuer belongs to the remaining
DEL-109 lifecycle work. The local entitlement checkout remains at its
foundation revision and is not an implemented issuer acceptance source.

Run the existing typed Control licensing decoder tests independently. File
materialization and decoder tests do not establish an installed quota change,
issuer-signed activation, renewal history or deployment migration. Keep
DEL-109 open until those actual flows meet their acceptance requirements.

No dependency, model or dataset is added. The existing Go standard library
uses BSD-3-Clause. The pinned
[go-yaml v3.0.5 licence](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE)
applies MIT to the listed libyaml-derived files and Apache-2.0 to the remaining
implementation; both permit commercial/offline use and redistribution with
their required notices. The first-party policy and wire remain unchanged.

## Verification

The targeted installer test
[TestLicenseImportCapacitySurvivesMaterialization](../../deploy/offline/internal/install/license_settings_test.go)
passed on macOS arm64. It materialized 128 imports, reapplied 128 after replacing
the generated serving configuration, and changed the input to 256. Both generated
configurations retained the selected limit and another runtime setting. Existing
authentication and trust bytes remained identical.

The existing Control tests matching `^TestLicenseConfiguration` also passed on
macOS arm64, including strict input refusal and the protected-file boundary.
These are file and decoder checks; no installed Linux quota or issuer lifecycle
acceptance was run for this change.
