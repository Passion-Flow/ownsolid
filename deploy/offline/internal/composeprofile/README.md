# Ordinary Compose port projection

The additional native [private configuration intent](../../../../../docs/milestones/m17-offline-compose/native-private-configuration.md)
is owned by `private_configuration.go`. It validates the complete exact file
manifest and an optional explicit nine-key environment overlay before filesystem
work. Paths, source/target ownership and operation budgets have no ambient/default
fallback. Immutable scope/operation/volume identities and file hashes cannot be
overridden. `cmd/operator compose-private-configuration` hands only this validated
request to the scope filesystem transaction; this package performs no I/O.
Focused roots: `TestComposePrivateConfigurationStrictIntent` and
`TestComposePrivateConfigurationExplicitEnvironment`. These source additions
await native execution; they do not establish installed Compose acceptance.

This package owns the explicit public port intent used by the early offline
operator command `compose-port-values`. It is independent of the signed isolated
scope, constrained Compose renderer, Engine, image admission and runtime lifecycle.
It reads no files or ambient settings and starts no services. The command owns
protected input reads; `Load` owns JSON and optional environment validation, and
`Parameters.Values` owns the final public YAML port override.

The [frozen contract](../../../../../docs/engineering/deployment-transport/compose-port-bindings.md)
requires exactly four JSON fields: `format_version` is integer `1`,
`bind_address` is a canonical unbracketed numeric IPv4/IPv6 address, and
`published_port`/`target_port` are independent integer ports from `1` through
`65535`. Explicit loopback, unspecified wildcard and global unicast addresses
are admitted; DNS names, zones, mapped addresses, multicast, link-local and
noncanonical addresses are refused. The complete base must be valid before an
overlay is applied. JSON is bounded to 64 KiB and must be valid UTF-8; missing,
unknown, duplicate, null, wrong-type and trailing values are refused.

An optional UTF-8 environment overlay is bounded to 16 KiB and accepts only
`PROJECT_ONE_BIND_ADDRESS`, `PROJECT_ONE_HTTPS_PORT` and
`PROJECT_ONE_CONTROL_PORT`. These map to the three corresponding public fields;
there is no alias, default or ambient precedence. Assignments use exact names
and `KEY=value` syntax, canonical decimal ports, and optionally JSON string
quotes. LF and CRLF, blank lines and full-line comments are accepted. Empty,
duplicate, unknown, padded, shell-expanded and inline-comment values are refused.
The historical `HTTPS_PORT` name does not select application transport.

`Values` revalidates even directly constructed parameters. It emits only
`services.control.ports: !override` with one long-syntax binding and JSON-quoted
text scalars. Supply it after the base and other selected overlays so actual
pinned Compose replaces raw interpolated ports rather than appending another
binding. The existing base template alone retains its historical defaults.
Changing listener or advertised origins remains the control typed configuration
owner's responsibility; this projection neither infers nor rewrites them.

Failures return only `COMPOSE_PORT_VALUES_INVALID` or
`COMPOSE_PORT_ENVIRONMENT_INVALID`. No partial intent or YAML is returned. Correct
the explicit input and rerun; no state has been mutated and no recovery operation
or Engine access is required. Broader base environment keys, machine HTTPS
publication, installed networking and full address/transport policy remain
separate responsibilities.

The three focused roots are `TestComposePortValuesStrictInputs`,
`TestComposePortValuesExactEnvironmentMapping` and
`TestComposePortValuesSafeLongSyntaxOverride`. They cover pure validation and
public output only. The actual pinned Compose suite must separately consume the
real command output and inspect its final model; neither test source nor a render
establishes installation, nonroot port permission, IPv6 routing, TLS or readiness.

## Machine publication and native health profile

`LoadMachine` and `MachineProfile.Values` implement the separate
`compose-machine-values` public projection defined by the
[machine profile contract](../../../../../docs/engineering/deployment-transport/compose-machine-profile.md).
`Binding` reuses the existing numeric address/port validation. A complete JSON
base has eleven exact fields, including complete browser and machine bindings,
policy/CA basenames, loopback probe dial and bounded probe/scheduler settings.
It is independently validated before the explicit fourteen-key overlay. The
64 KiB JSON and 16 KiB environment limits, UTF-8, duplicate/null/unknown/case
refusals and canonical overlay grammar remain strict. No default or ambient
environment authority is introduced, and the original port-only API is unchanged.

The two published ports and two targets must be distinct even across different
bind addresses. This conservative profile does not claim every possible
same-port/different-host topology. Basenames are distinct, nonreserved ASCII
names beneath the existing `/run/projectone` container layout. Probe dial is an
exact numeric loopback `AddrPort` matching the machine target; IPv6 dial uses
brackets while published `host_ip` does not. Timeout relationships and scheduler
bounds are validated before any output is returned, including explicit start
period zero. Invalid inputs return only `COMPOSE_MACHINE_VALUES_INVALID` or
`COMPOSE_MACHINE_ENVIRONMENT_INVALID` with no partial profile or projection.

The final override replaces all previous control port mappings with exactly the
two selected bindings. It also supplies `serve` and the exec-form native
machine-ready healthcheck with identical config and protected policy references.
The probe explicitly names `/usr/local/bin/control`, selects its dedicated
machine audience and verified CA reference, and has no shell, retry, redirect or
TLS-disable argument. Compose scheduling remains explicit profile input. The
renderer never reads or rewrites policy, CA, TLS or SQL files, changes mounts or
images, contacts an Engine, starts a service or sets runtime listener/origin
configuration. Browser HTTP/HTTPS policy remains with the existing control
configuration owner; machine protocol continues to require dedicated HTTPS.

Supply this output last in the selected Compose file list and accept it only
after a successful command exit. Correct malformed intent and regenerate a new
private output; no external state needs rollback. Before separate installation,
use the actual native serving verifier against exact mounted browser/machine
runtime intent, inspect the pinned Compose model and retain its evidence. Missing
or mismatched runtime policy, CA permissions, SANs and listeners are failures, not
conditions this public renderer repairs.

The new pure roots are `TestComposeMachineProfileStrictInputs`,
`TestComposeMachineProfileEnvironmentMapping` and
`TestComposeMachineProfileNativeHealthProjection`. Their synthetic public intent
does not provision runtime files or listeners. Actual command/Compose rendering,
installed health transitions, both browser entrances, real TLS/readiness,
restart/recovery, IPv6 routing and target qualification remain separate gates.
