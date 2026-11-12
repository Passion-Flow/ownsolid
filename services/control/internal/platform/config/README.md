# Control typed configuration boundary

[config.go](config.go) owns the main configuration load and cross-component validation. [policy.go](policy.go) owns adjustable policy defaults/ranges. Component files such as datasets.go, knowledge.go, training.go, sso.go and object_storage.go validate their respective inputs; explicit environment mapping files apply documented deployment overrides. Configuration is constructed before domain services, then injected rather than reread inside business methods.

Database selection/connection, public origins, listener/TLS, protected component references, private artifact paths, runtime executables, budgets and enabled features have distinct owners. Required values and contradictory combinations must fail. A configuration reference never grants a user permission or authorizes a provider operation. Keep credentials behind protected file/secret references and browser-visible runtime settings free of secrets.

Cross-field validation matters: request/shutdown budgets must accommodate child/repository cleanup, private storage roots must not overlap incompatibly, and enabling a dependent component requires its actual prerequisites.

Workflows require either enabled Knowledge or enabled signed Plugins. Explicit
`workflows=true/plugins=true/knowledge=false` selects the tool-only profile;
publisher trust and conservative workflow/plugin HTTP budgets remain mandatory.
Enabled Knowledge still validates its own assets/gateways and budgets, with no
fallback. The flags belong to typed configuration, never ambient environment. Workflows
now also maps through the documented protected env-file below; plugin selection
retains its independent typed configuration. The existing protected HTTP env mapping remains available.
See [tool-only configuration/deployment](../../../../../docs/milestones/m14-plugins/tool-only.md)
and `workflow_profile_test.go` for actual complete-loader source regressions.

[server_transport.go](server_transport.go) owns named `ServerConfiguration`, `TransportMode`, canonical `BrowserOrigin` parsing and the explicit transport environment mappings. The zero mode retains legacy `direct_https`; explicit `http` or `trusted_proxy_https` requires matching origins, listener and mutually consistent TLS/proxy inputs. `Config.Validate` checks structure without opening TLS files; direct HTTPS `TLSConfig` still checks actual protected key/pair/validity and both DNS/IP SAN identities. Only validated HTTP/proxy configurations return a nil TLS configuration. Enabling the current HTTPS-only SSO callback profile in HTTP is rejected. This configuration slice does not establish complete HTTP/proxy listener, cookie, browser or deployment support; those owners must consume the same policy and pass the [frozen transport matrix](../../../../../docs/engineering/deployment-transport/README.md).

Use [transport.env.example](../../../../../config/examples/control/transport.env.example) with the existing protected explicit `serve --env-file`. Valid YAML is checked independently before merging; unknown/duplicate/null/empty/malformed or contradictory input never selects a fallback. Ambient environment is ignored. The lower-level policy resolver still permits independent owner tests without an unrelated server configuration; complete decode and `Config.Validate` always validate the server. Empty environment values cannot unset a field: select a matching valid YAML snapshot when changing modes. Noncanonical addresses fail rather than being rewritten; DNS, localhost, IPv4 and bracketed IPv6 remain distinct. A wildcard bind is not an advertised origin. References remain protected, and provider/gateway/SMTP/Kubernetes TLS configuration is independent.

For a changed setting, update its schema, [commented examples](../../../../../config/examples), explicit env-file projection, Compose/Helm mapping, owner constructor and invalid/unknown/false/nondefault tests in one batch. Do not edit localization_policy_generated.go independently of its contract generator. Protected-file behavior belongs in the existing protected/private-file helpers rather than new ad hoc reads.

Focused native check: `go -C services/control test ./internal/platform/config`. Then use the affected full-server/deployment profile to prove that the same target artifact accepts the nondefault configuration and preserves authentication, storage and shutdown behavior. Parsing alone is not deployment acceptance.

`tls_material.go` owns the actual startup certificate/private-key snapshot. Both
canonical directory and regular-file descriptors remain pinned through bounded
reads and metadata checks, then `TLSConfig` parses the byte pair once. Public
certificate readable modes and private-key permissions remain distinct; stable
actual ownership is checked without requiring public certificate ownership by
the runtime UID. Links, aliases, special files, size bounds and observed metadata
replacement fail closed. This is not an atomic filesystem transaction or kernel
I/O cancellation guarantee. Other `Secret` consumers are outside this repair.
The read-only serving verifier delegates to this same owner; see its [contract](../../../../../docs/engineering/deployment-transport/serving-check.md).

The restored-parent-mtime regression first establishes an observable native
change-time mutation within a bounded fixture deadline. Linux may assign the
same coarse-clock ctime to immediate permission changes even with nanosecond stat
fields. The fixture retains the original inode/mode/size/mtime checks and all
replacement refusals; failure to establish the mutation fails the test rather
than skipping it. Production snapshot admission is unchanged.

`LoadProtectedWithEnvironment` is the health-probe entrypoint for a bounded256KiB
main snapshot and the existing explicit policy-file mapping. It shares the
nonblocking descriptor reader with private component references. Registry JSON
is read here as a pinned private1MiB snapshot and passed to the pure
`registryconfig.Decode`; the probe does not invoke the legacy bundle-file reader.
FIFO/special/link/permission changes fail closed; descriptor/path comparisons
also retain native change time so restoring mtime cannot conceal an equal-length
same-inode rewrite. Regular-file and metadata
syscalls on a hung filesystem still require outer process supervision. Ordinary
`LoadProtected` retains its historical API and behavior. The [probe contract](../../../../../docs/engineering/deployment-transport/health-probe.md)
owns the new scoped regression roots and separately retained execution evidence.

[server_transport_test.go](server_transport_test.go) contains source tests for canonical address/port parsing, malformed YAML before overrides, mode/TLS/CIDR contradictions, all seven explicit environment mappings, ignored ambient values, current SSO restrictions and actual generated synthetic TLS material. The original admitted native race gate passes10roots/145entries, including these tests; the lexical numeric-host check was repaired before that first run to reject values beyond machine integer ranges. This establishes configuration assertions only; see [scoped evidence](../../../../../docs/execution/mvp/deployment-transport-boundaries.md) for actual listener controls and the remaining full-profile gate.

`machine_server.go` owns optional top-level `machine_server` and its four required
fields: `listen_address`, `origin`, `tls_certificate_file`, `tls_private_key_file`.
Presence selects dedicated direct HTTPS in the same control process; omission
retains legacy shared secure Admin routing. Explicit null, partial, empty,
unknown, duplicate, aliased or mistyped groups refuse. The bind and origin retain
their exact canonical identity and differ from the browser listener/origins.
Configuration checks exact string collisions without DNS or sockets; the serving
owner additionally prebinds both sockets to refuse wildcard/address overlap.

Direct machine validation and the raw YAML shape precheck use
`CONFIG_MACHINE_SERVER_INVALID`. The public `Decode` boundary retains its
existing `CONFIG_INVALID` redaction for errors returned by YAML decoding,
including an empty/partial group or an invalid field value rejected by
`UnmarshalYAML`. Such a base snapshot fails before environment merging;
overrides cannot repair it. Errors do not expose the offending value or path.

`Config.MachineTLSConfig` returns nil for absence, otherwise uses the sole shared
TLS pair/SAN/time/minimum-version parser and the existing pinned1MiB material
reader. The machine certificate covers only its actual origin; browser TLS still
covers both browser origins. A nil HTTP/proxy browser TLS result does not verify
machine material. `Config.MachineOrigin` selects the dedicated exact origin, or
the selected canonical HTTPS Admin origin for legacy direct/proxy modes. The
legacy accessor does not require unrelated listener/material fields or read a
file. HTTP without the group returns `CONFIG_MACHINE_TRANSPORT_REQUIRED` when a
new machine binding is requested; it remains valid for browser-only use.

The [machine group schema](../../../../../contracts/configuration/v1/control-machine-server/schema.json)
describes the object embedded under `machine_server`;
[machine-server.example.json](../../../../../config/examples/control/machine-server.example.json)
is that complete group. Merge the four commented
[machine environment keys](../../../../../config/examples/control/machine-server.env.example)
into the existing protected `serve --env-file`. They override only an independently
valid existing group, never create/unset one, have no defaults or ambient policy,
and require restart. An override clones the selected group rather than changing
another Config snapshot's pointer. Validate the final machine/browser combination
after all explicit overrides. No certificate/key contents enter examples or
browser runtime configuration.

Before moving an agent/gateway origin, reconcile pending outcomes, fence/revoke
the old scope and preserve its origin-bound evidence/spool. Issue a new explicit
identity; do not rewrite stored bindings or return one-time secrets on replay.
Machine-specific deployment projection, Compose/Helm mapping, verifier/probe and
operator examples are a separate immediate increment. The current browser-only
serving-expectation command does not claim machine validation. See the
[machine ingress contract](../../../../../docs/engineering/deployment-transport/machine-ingress.md).
New source tests cover schema/example/env agreement, structural isolation,
explicit nondefault/invalid/false input and actual generated TLS refusals.
Scoped execution evidence, actual dual serving/SQL and target qualification
remain separately owned; source presence is not a passing result.

## HTTP listener, admission and retry policy

`http_policy.go` owns all ten HTTP defaults/bounds and relations; the request
versus reset-budget relation remains in `RuntimePolicy.Validate`. All documented
`PROJECT_ONE_HTTP_*` keys map through `http_environment.go` into the existing
protected explicit policy file. See the [effective HTTP schema](../../../../../contracts/configuration/v1/control-http-policy/schema.json),
[adjacent-commented example](../../../../../config/examples/control/http-policy.env.example)
and [focused contract](../../../../../docs/engineering/deployment-transport/current-m01-retry-policy.md).
Sparse YAML keeps owned defaults; invalid independent YAML cannot be repaired by
an overlay. Restart applies valid changes without rebuilding. Retry advice does
not authorize replay of an uncertain mutation. `http_policy_test.go` checks real
loader/schema/example/environment agreement and invalid/false/nondefault values.
The mandatory positive `installation_lease_interval_seconds` (default5, 1..60)
bounds idle installation-lease observation; request/shutdown retain their existing
probe/join budgets. It cannot disable authority checks. Native Dameng uses the
shorter of this cadence and its existing readiness cadence in one loop. See the
[lease-monitor contract](../../../../../docs/engineering/deployment-transport/installation-lease-monitor.md).

The optional `release_view_config_file` / `PROJECT_ONE_RELEASE_VIEW_CONFIG_FILE` reference
uses the same explicit env-file precedence and private JSON-redaction rules. The releases
module owns its strict protected configuration; omitted disables assembly, explicit invalid
values fail. See `config/examples/release/admin-view.env.example` and schema.

## Account application profile

The `nodes` application profile extends `workspaces` with M03 discovery only.
Use `config/examples/control/nodes.example.yaml` and `nodes-profile.env.example`.
It requires PostgreSQL and the exact eight-entry minimal ledger, rejects later
configured services, and supports the existing dedicated machine_server group.
Browser HTTP requires that independent verified HTTPS listener; direct/proxy
HTTPS may use the exact Admin HTTPS origin. Credentials remain separate.

`distribution_environment.go` maps every DistributionPolicy field through the existing protected policy input. Defaults/bounds stay in DistributionPolicy; distribution-policy.env.example documents units, ranges and restart/precedence. No additional environment reader is introduced.

## Current cumulative M11 source integration — 2026-10-26

All18 SSO policy fields plus enabled/keyring/provider-inventory inputs map through
[sso-policy.env.example](../../../../../config/examples/control/sso-policy.env.example).
The private provider JSON reference replaces the same typed `sso.providers` array,
with1MiB/64-provider bounds and exact-field/duplicate/null/type validation.
Disabled recovery does not load absent keyring/CA assets or open provider sockets.

## Cumulative M12 source integration

## Current cumulative M13 source integration

`workflows_environment.go` maps every WorkflowPolicy integer and the strict boolean
`PROJECT_ONE_WORKFLOWS_ENABLED` to the existing protected `--env-file` owner.
[Schema](../../../../../contracts/configuration/v1/control-workflows/schema.json)
and [commented example](../../../../../config/examples/control/workflows-policy.env.example)
match defaults/ranges and enforce run/node/lease/heartbeat/HTTP relationships.
`false` never becomes true. `values-workflows.yaml` selects the same protected
`workflows.env`; Compose mounts the complete private configuration and that file,
not ambient policy variables. Restart changes the same compiled artifact.

## Current cumulative M14 source boundary — 2026-10-27

Implementation and current local execution evidence are recorded separately in the
[current M14 handoff](../../../../../docs/execution/mvp/m14-closeout-current.md).
Physical qualification and real manufacturer approval remain separate.

## Cumulative Portal composition — 2026-10-27

## Cumulative localization composition — 2026-10-27

## Cumulative M17 source boundary — 2026-10-27

## Topology and serving rules

`topology.size` is the only deployment shape: `single-node`, `ha`, `cell`, `multi-cell`
or `multi-site`, defaulting to `single-node`. Only `single-node` is accepted today; see
[topology.go](topology.go) and ADR 0003. `Config.ValidateServing` holds the rules that
must pass before `serve` accepts traffic.

The optional `image_registry_site_source_file` selects the Registry owner's
protected read-only site configuration alongside `image_registry_config_file`.
`PROJECT_ONE_REGISTRY_SITE_SOURCE_FILE` in the explicit protected environment
file overrides a valid YAML reference. Empty, null, relative or noncanonical
references, and a source without a writable Registry, are refused. The module
constructor owns source JSON, private CIDRs, trust, credentials and source
separation. An explicit invalid source aborts startup. The shared runtime
retains the existing Registry cleanup registration.
