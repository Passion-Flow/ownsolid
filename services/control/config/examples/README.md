# Configuration examples and live schema owners

These files are inputs to review and provision, not an executable default
installation. Placeholder IDs, image references and paths must be selected through
the owning supported boundary. Copy only the required samples into an exclusive
private configuration directory; create real secret references separately. Loading
or rendering an example does not grant permission to contact a provider.

## Select by operation

| Directory | Inputs and actual owner | Companion delivery checks |
| --- | --- | --- |
| `control/`, `compose/` | Root Go YAML and protected serving-policy env; `services/control/internal/platform/config/`; public `control/serving-expectation.example.json` is verification intent only | Owning config tests, `deploy/compose/base/workspace-policy.yaml`, applicable feature overlay and Helm policy projection; `control verify-serving-transport` compares explicit intent with effective typed configuration |
| `agent/` | Agent profile and management/lifecycle settings | `services/agent` typed configuration plus fixed runtime recipes / deployment unit templates |
| `gateway/` | Gateway startup JSON and separate systemd profile | Rust config tests, schema agreement, `tools/deploy/gateway/render.py` |
| `aws/` | Explicit connection array, protected credential references and bounded discovery policy | `control` AWS config/adapter tests; existing control policy bind/Helm Secret projection |
| `monitoring/` | Production protected monitoring policy; separate quality-only capacity profile | `services/control/internal/modules/monitoring/config.go`, `tools/quality/reliability/capacity_configuration.py`; distinct env authorities |
| `registry/`, `object-storage/`, `middleware/`, `task-events/`, `mongodb/` | Typed component configuration and protected reference inputs | Owning loader/adapter tests and same-named Compose overlay where delivered; native target verification remains separate |
| `database/mysql/` | TLS connection selection | `services/control/internal/platform/database/` and config database selection; isolated real-database tests after admission |
| `kubernetes/` | Operator, lifecycle, installation, cleanup and fixed Job request inputs | `tools/deploy/kubernetes` strict loaders and explicit target harnesses |
| `release/` | Offline factory, operator, restore/backup/process/journal policy | `tools/release/offline`, `tools/deploy/offline` and corresponding evidence readers |
| `localization/`, `licensing/`, `qualification/` | Artifact manufacture/review, license and target qualification inputs | Their typed producer/consumer and fail-closed schema checks; no runtime translation or fabricated qualification |
| `development/` | Synthetic fixture/toolchain/storage/resource profiles | `tools/dev/configuration/settings.py` and operation-specific fixture owners; never a customer database profile |
| [private-cluster](private-cluster/README.md) | Typed M28 component configuration, secret-reference env example and separate local validation inputs | Owning privateclusters/config tests and mainline integration contract; an empty plural reservation is not a configuration authority |

Some `*.schema.json` files here are authoritative dependencies. For example,
`monitoring/monitoring.schema.json` is read by
`tools/contracts/http/generate_monitoring.py` to bound public response schemas;
`monitoring/capacity.schema.json` describes a separate quality workload. Changing
an "example" schema can therefore change a generated protocol. Preserve its
exact path until a coordinated migration supplies a compatibility and caller map.

Each env key needs adjacent English purpose/owner, type/units, required/default,
range, secret-reference rule, precedence/mapping and restart/reload behavior.
Duplicate, unknown, empty-required, wrong-typed, invalid and contradictory values
must be rejected by the actual loader. Literal false needs an explicit regression
case. Existing JSON-only boundaries (notably gateway startup) are not evidence
that the user's complete deployment env-file mapping requirement is satisfied;
record or implement the owning mapping instead of adding unused keys.

`control/serving-expectation.example.json` contains public verification intent,
not runtime settings or secret bytes. Its eight required fields, version/mode
rules, units and empty/nonempty TLS/proxy behavior are defined in the
[serving-check contract](../../docs/engineering/deployment-transport/serving-check.md)
and [structural schema](../../contracts/configuration/v1/control-serving-expectation/schema.json).
The example uses nonworking DNS/path references; select the deployment's exact
bind, distinct origins and protected references before invoking the command.
Runtime YAML plus the existing seven commented `control/transport.env.example`
keys remain the only transport authority. Intent never overrides them. Changes
require an explicit verifier invocation and the normal serving restart; no hot
reload, CA installation or provider contact is implied.

`control/helm.serving-check.values.example.json` is a nonworking overlay for the
first direct-HTTPS chart verifier variant. Supply a real isolated installation
ID and merge the existing full Helm example; select exact origins/bind/TLS
inventory and matching runtime YAML/protected policy. The legacy origin aliases
are exact compatibility checks, not override settings. The old origin-only
address renderer and complete HTTP/proxy chart modes remain a tracked subsequent
increment. A rendered overlay is not an installed or qualified deployment.

Before a change, identify every reference using `rg` on the exact filename/key.
Then compare samples to the typed loader and generated contract, render the
nondefault selected Compose/Helm variant without starting it, and run the affected
configuration tests in an admitted window. `tools/quality/configuration/check.py`
contains Docker Compose invocations and broad syntax checks: it is not an inert
schema linter and must not be launched without its prerequisites and budget.
