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

## Projection prototype

A temporary program outside product code used the pinned YAML library to add
one long-form Control publication to the canonical Compose document. Parsing
the output and removing only that added publication reproduced the entire
original data model exactly, including other services and security settings.
The published port remained a string and the target remained an integer.
The source SHA-256 was
`3c753f781471272963ec2fcfd7b88dff19fdb484c0e7242054ca79fa817f0dca`;
the prototype output was
`eb00992f5c9977b3683046a91cd99b1c2debf8d0b14d73422260d3ee60405fb6`.
This establishes the local projection technique, not native Compose admission.

Before enabling Portal selection on an existing installation, use its actual
Control container image to check a private, temporary desired snapshot through
the same CLI. Do this before replacing installed configuration or stopping the
writer. If a first install was interrupted before creating Control, resolve the
recorded environment's local image instead. Local image admission precedes this
check so an interrupted first install can resume after archive loading. Never
infer compatibility from an image tag or from the candidate's version string.
Require canonical Compose port structure and reject ambiguous anchors, duplicate
fields or preexisting extra publications rather than silently modifying them.
Operational restore can reuse the installed two-port copy only when its extra
publication exactly matches the selected typed listener. Other changes use the
delivered canonical source. Existing advertised origins must remain exactly
unchanged during listener selection; changing their port or hostname belongs to
the session/SSO address migration item. A fresh installation selects them together.

## Typed projection verification boundary

The selected direct-HTTPS implementation passed the complete installer package
on macOS arm64 (31.340 seconds), vet, the Linux amd64 installer build and full
`make check`. The actual Linux amd64 test ELF
`40f20c25cd507983794454d68bf8c2cf187c0064cfdf8430fbec607bec5b572f`
then passed 15 selected unit roots and 50 RUN records, with no skips, in
0.279832 seconds. The retained `portal-unit-native.de8rfidx` receipt binds the
source-file hashes. It ran as UID/GID 65534 with a private 64 MiB tmpfs, mount,
PID and network namespaces, only loopback up, one Go worker and a 128 MiB soft
Go heap limit. The namespace launcher exited after the run; no host port,
Docker container, Control process or database was created.

Coverage includes explicit/null/merged-null input, port collisions, unchanged
shared fingerprints, canonical template refusal before writes, shared/split/
shared configuration and key preservation, exact installed projection reuse for
restore, current-image ownership, source refusal before configuration changes,
explicit retry, unchanged advertised origins, one Docker publication snapshot,
and release of a first port reservation when the second cannot bind. Real local
TLS servers verify Host/SNI and CA handling, refuse the wrong publication and an
untrusted certificate, and reject a readiness redirect to a successful page.
Docker and Control command responses remain controlled unit fixtures.

The first Linux launcher refused before running any test because inherited
sysfs showed the host network devices. Its zero-RUN result
`portal-unit-native.r6pilce4` is retained separately. The corrected launcher
uses the new namespace netlink view; it does not count that refusal as a test
pass. Native Compose parsing is qualified separately below. The historical
Control image command, installed split-listener upgrade/rollback, browser flow
and recorded zero-egress drill remain unqualified. DEL-114 stays open.


## Native Compose parser verification

The original generated shared, split, shared-again and split-override inputs
passed the real Linux amd64 Docker 29.1.3 client and Compose
2.40.3+ds1-0ubuntu1~24.04.1 `config --format json` parser. The candidate was
`1a41d97981798440b7f02061d3a73853726b03bc`, tree
`398d104c91ce834bec81b9231e28d2a2f6733548`; the 13-file input manifest was
`bd0b6a78cbd04eb00551d8d6b59e9613fa19a888d2a6b6a354e67226d5c6f7cd`.
All four actual invocations exited 0 with JSON and empty standard error. After
normalizing only the generated installation root, shared/split/shared returned
the same shared data model, the split case added only its explicit Portal
publication, and the override changed only the operator image. The operator
had no publication and all services retained `pull_policy: never`.

The retained `compose-parser-native.qb17th2d` result SHA-256 is
`b5a156ea12b5079ce3082aeceeaf6cfcc931e172ef64516cc69eb11ad77b50d8`.
The parent finished in 1.191533 seconds under a 30-second total deadline that
included cleanup. Each parser ran as UID/GID 65534 in private mount, PID and
network namespaces, with read-only namespace sysfs, only loopback down, an
empty owned Docker configuration, the original private HOME, an absent
configured daemon socket,
NoNewPrivs and an inherited ABI-checked seccomp filter refusing socket calls.
These controls and the input pins were checked before the Docker exec attempt;
the successful JSON comparisons establish completion separately from that
attempt marker. One Go worker and a 128 MiB soft Go heap limit applied to this
client-only experiment. It acquired no heavy-job locks.

The first parser attempt, `compose-parser-native.sr2wi7jd`, stopped at the
route-file line-count guard before Docker execution. No route bytes were
retained, so its cause remains unknown. The reviewed
[Linux 6.8 route iterator](https://raw.githubusercontent.com/torvalds/linux/v6.8/net/ipv4/fib_trie.c)
can emit no header when the main table is absent. The revised guard accepts
only bounded ASCII empty output or a recognized header without route data,
and preserves byte count, hash, row counts and stage before refusal. Nine
synthetic guard cases covered empty/header-only output and refusal of route
rows, unknown headers, non-ASCII and oversized input. The new native run
observed an absent table with zero bytes in all four cases. That observation
does not establish the previous attempt's cause or replace its retained refusal.

The parent reported joined children and removed its fixture. Independent
readback confirmed the parent and all four launchers were absent, no process
referenced either recorded network namespace inode, the exclusive owned
fixture prefix was empty, and the frozen source remained clean. The wrapper
did not retain the successful fixture's random suffix; this readback records
prefix absence rather than claiming an exact path it did not observe. No
container, image code, Control CLI, PostgreSQL, host publication or installed
flow ran. No egress-attempt recorder ran. Historical-image admission, real
TLS/UI installation, upgrade/rollback and the recorded zero-egress drill remain
open; parser success does not tick DEL-114.
