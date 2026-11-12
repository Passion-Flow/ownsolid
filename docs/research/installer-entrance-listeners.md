# Installer browser-listener projection (DEL-114)

Reviewed: 2026-11-13.

## Proven boundary

The original ARC-110 implementation in main `d6fdfaed` and
[ADR 0074](../decisions/0074-bind-split-browser-listeners-to-their-audiences.md)
provide `server.portal_listen_address`. A selected bind makes the original
browser socket Admin-only and the new socket Portal-only, before the existing
identity dispatcher. Omission preserves shared serving. The browser TLS pair
still covers both advertised identities. The separate machine server and Rust
inference gateway keep their own ownership and contracts.

The [serving intent schema](../../api/configuration/v1/control-serving-expectation/schema.json)
adds the optional nonempty field. An old expectation asserts absence; it is not
permission to ignore a configured Portal bind. The existing
`verify-serving-transport` command validates exact intent and calls the sole TLS
owner without opening a database, listener, provider or worker. Its early CLI
dispatch is present from the initial ownsolid backend import `df6fbd57`, so the
existing shared expectation can be exercised against historical release fixtures.
Go internal-package boundaries prevent importing the Control operation into the
installer; consume its published CLI and JSON contract instead of another TLS
validator or a new public facade.

[Compose port publication](https://github.com/compose-spec/compose-spec/blob/main/workload_spec.md#ports)
distinguishes the container target, published port and host IP. Its long form
keeps the published port a string. The native
[Compose config command](https://docs.docker.com/reference/cli/docker/compose/config/)
resolves the selected data model and provides a no-start parser check. Use only
the established port fields already supported by the installed Compose 2.40.3;
do not select newer spec hooks or resolve remote image digests.

## Selected first path

- Add an optional typed `network.portal_listener` containing an explicit host
  bind address, published HTTPS port and unprivileged container port. Require a
  different internal port from the existing shared/Admin socket. Advertised
  origins remain separate inputs. Require distinct browser hostnames for this
  first path; IAM-114 owns the shared-hostname opt-in and cookie explanation.
- Project only a selected Portal bind into both generated Control snapshots.
  Omission emits no empty or null server field. Preserve the existing TLS files,
  server identity policy and keys across repeated materialization.
- Keep the one shipped Compose source. Add the selected publication to its
  generated installation copy, preserving the other services and security
  settings. A shared configuration retains the original source bytes. Returning
  to shared serving regenerates the single publication rather than relying on
  hand-edited files, another service or a profile-specific Compose template.
- Construct explicit public serving intent from the site inputs and invoke the
  existing checker after local image admission and before stopping Control or
  changing PostgreSQL. Require its exact success marker. Preserve configured
  machine intent using only its four allowlisted public fields and the existing
  machine contract; never forward arbitrary settings or secrets as arguments.
- Upgrade and rollback have stop stages outside ordinary Apply. Check their
  selected images before those stages as well. Keep listener selection compatible
  with both images: upgrade an older shared binary first, then select the new
  listener on that release. Refuse a changed or unsupported serving intent before
  stopping the writer; never silently remove the Portal field for an old binary.
  A temporary private Compose image override can select the exact admitted image
  without overwriting the installed environment or publishing service ports.
- Check both selected publications against Docker and host sockets. Route each
  readiness probe through its own publication with the unchanged origin Host,
  SNI and CA verification. Select the correct loopback family for wildcard binds.

No new dependency, model or dataset is introduced. Go uses
[BSD-3-Clause](https://go.dev/LICENSE), the existing
[YAML v3.0.5](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE) uses MIT and
Apache-2.0, and [Compose 2.40.3](https://github.com/docker/compose/blob/v2.40.3/LICENSE)
uses Apache-2.0. They permit commercial use, offline operation and redistribution
with the required notices. No upstream implementation is copied.

## Failure coverage and completion

Before installed acceptance, test strict nested input types, missing and invalid
ports/addresses, internal and published collisions, same-hostname refusal,
shared/split/shared materialization, unchanged keys, both runtime snapshots and
the actual native Compose parser. Refuse malformed templates. Prove that checker
failure or a missing success marker stops before Control shutdown or database
work and that explicit retry retains state. Include historical shared-image
refusal of new Portal intent, target refusal and safe application rollback;
the ordinary Apply step alone cannot qualify the earlier upgrade stop. Cover both publications, foreign
ownership, occupied ports, IPv4/IPv6 probe selection and wrong-audience responses.

The installed Linux amd64 drill must use the exact source/image, actual trusted
TLS and both UI entrances, persistent data across a repeat, failure/retry and
the existing egress recorder with no unintended outbound attempts. A parser or
unit pass does not establish that flow. Keep HTTP laboratory mode, trusted-proxy
delivery, shared-hostname opt-in, the complete four-entrance installation and
address/session/SSO migration in their existing open items. This research does
not mark DEL-114 or ARC-110 complete.

## Pre-stop admission boundary

The installer now supplies the eight explicit shared-server intent fields to
the existing Control command. Apply requires its exact success line between
local image admission and the first stop. Upgrade checks both recorded source
and candidate immutable images against the currently installed runtime snapshot
before reserving recovery state or stopping Control. Rollback checks the prior
image before its stop. Schema observation remains read-only and can precede
these upgrade checks; the checker itself opens no database.

An ephemeral JSON Compose override selects only `control-operator.image`.
It inherits the canonical operator's local-only image policy, existing mounts,
no published ports and no dependencies. Both refusal and success remove that
override and preserve the installed Compose file, environment and two Control
snapshots. Machine intent uses its four exact public string fields. Null,
unknown, missing and mistyped fields are refused without forwarding arbitrary
settings to a command line. Control remains the sole TLS validator.

The actual installer package passed on macOS arm64. The three new unit/filesystem
roots also passed on Linux amd64 as UID/GID 65534 in a private mount, PID and
network namespace with no route and loopback down: 10 RUN records, no skips,
0.130869 seconds. The retained `serving-filesystem-native.oyzxeur4` receipt binds
the exact seven source-file hashes and test ELF
`9a8f06336af1bf3e543b388c29bd9b241e88343acc3bdbe53c64e017d853995d`.
Tests cover exact public intent, malformed machine intent, rejected/missing/
embedded success markers, no writer/database step after refusal, unchanged keys
on explicit retry, and image-override removal on success and failure.

Those tests use controlled command responses. They do not establish native
Compose merging, historical-image acceptance/refusal, the Control command or an
installed upgrade/rollback run. The optional Portal configuration/projection is
not enabled by this preparatory admission change. Keep those qualifications and
the complete address matrix open.
