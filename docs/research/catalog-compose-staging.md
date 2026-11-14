# Stage and launch a closed Compose model (CAT-107)

[Docker's Compose trust model](https://docs.docker.com/compose/trust-model/)
describes host reads and execution through env files, include, extends,
providers, bind mounts, configs and secrets. A signature on the source or a
digest of rendered JSON cannot pin the bytes of a referenced file. OPS
demonstrated the gap with env_file: content changed while the normalized
JSON produced without env resolution stayed the same.

The installer's initial admission profile is a closed set of services with
digest-only images, explicit never-pull and network mode none. It permits
literal command and entrypoint arrays, string environment maps, Linux amd64,
and read-only container root filesystems. It rejects all file references,
mounts, privileges, providers, builds, profiles, interpolation, YAML aliases
and extensions before Compose can read them. A bounded syntax and field
check is an admission rule. The pinned Compose binary still interprets the
source and renders the canonical model. Compare the admitted source and
rendered services so runtime defaults cannot silently change this profile.

Read the signed source from the caller's pinned private staging root using
the one path in the catalog contract. Keep the verified bytes in memory
and send them as the sole Compose stdin file. Keep the normalized model
private and expose a copy for catalog image checks. Recheck source identity,
signed digest and admitted model digest at the launch boundary. The
launcher receives the exact checked buffer through stdin and uses the
same explicit project and private working directory. The process
environment is built locally. Use an empty env file, no ambient Compose
variables or Docker credentials, and a site-selected Unix socket. Keep
the renderer away from the engine socket. Open and verify a protected
Compose executable before rendering and retain that executable identity.
[Compose CLI](https://docs.docker.com/reference/cli/docker/compose/),
[Compose config](https://docs.docker.com/reference/cli/docker/compose/config/),
[Compose up](https://docs.docker.com/reference/cli/docker/compose/up/).

A pre-implementation prototype on Ubuntu 24.04 amd64 used the packaged
Docker Compose 2.40.3 executable, SHA-256
`d87a11e944c990dc9f2186115b1136c1cbffffc870845caff0cbdcce0780f41d`.
With a deliberately absent engine socket and no external routes, it read
the source from stdin, rendered JSON, then rendered that JSON again from
stdin. The second output was byte-identical. The repository recorder
observed only IPv4, IPv6 and DNS test probes. No daemon or workload ran.

Use the existing BSD-3-Clause Go runtime and the MIT and Apache-2.0
[Go YAML v3](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE) parser
already in the installer. The [Compose executable](https://github.com/docker/compose/blob/v2.40.3/LICENSE)
is Apache-2.0 and supplied by the site. Those licenses permit commercial
and offline use and redistribution under their notice terms. This change
ships no executable dependency, image, model or dataset.

Cover source replacement, same-inode modification, symlink or hardlink
source, rejected fields and tags, canonical output changes, wrong admitted
model digest, private render context and launcher refusal. The launcher
must see the exact admitted bytes on success, and receive no call after an
admission failure. A launch failure leaves success unset.

The catalog changes `c5b0fb94` and `a30bb44f` must merge before the
authorized caller connects its trusted release pin and
`ComposeImagePlan` to this boundary. Keep preparation independent of those
unmerged types. Production must verify publisher trust and installation
scope, record the release pin and the source and model digests with its
authorization and audit, and pass the same recorded plan to image import
before calling startup. A failed startup must remain unsettled. Production
authorization, persistence, audit and Console wiring belong to that
integration. There is no new unauthenticated installer command.

## Disconnected Linux acceptance

On `ownsolid-cpu`, `unshare --mount --net --propagation private` put
IPv4 and IPv6 default routes on the repository egress recorder. The
Linux amd64 test binary used the pinned packaged Compose executable
through its retained descriptor. The native renderer ignored an ambient
Compose file, Docker endpoint and app variable as well as a staged
.env file. A controlled launcher received the exact admitted JSON.

The joint fixture overlaid the unmerged OPS files from `a30bb44f` without
changing the catalog checkout. The real catalog verifier checked the
temporary-signed manifest, locales, source and a closed OCI fixture
archive with SHA-256 `3f00fdeb3bfa53dc623056ae17055686c17bf8ec953d913c94294b0e937b5f61`. The catalog plan retained
the signed release pin, image digest and exact native Compose model
digest. The installer passed those model bytes to the controlled
launcher. A different signed source retained no launch authority. The
changed file, an independently signed env_file reference, a changed
rendered command, an unexpected model property and a replaced source
inode were refused. The recorder saw exactly its IPv4, IPv6 and DNS
probes. No workload or Docker engine was started.

Run the installer tests with `go -C deploy/offline test ./internal/install
-run '^TestCatalogCompose'`. To exercise the native renderer, run the
Linux amd64 test binary in the disconnected namespace with
`OWNSOLID_CATALOG_COMPOSE_TEST_BINARY` set to the absolute Compose
executable path and `OWNSOLID_CATALOG_COMPOSE_TEST_SHA256` set to its
`sha256:` digest.
