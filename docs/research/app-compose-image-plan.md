# Rendered Compose image verification (CAT-107)

Signed OCI preflight authenticates the imported images. The deployment
model also needs to reference those exact local image identities.
Docker Compose can render a resolved model as JSON and use the local
store when a service sets the never pull policy. Compose can also
load images through other fields, including init steps and image
mounts, and can delegate to providers. Use the documented Docker
Compose model for this boundary. [Compose config](https://docs.docker.com/reference/cli/docker/compose/config/),
[Compose services](https://docs.docker.com/reference/compose-file/services/),
[Compose trust model](https://docs.docker.com/compose/trust-model/).

The installer owns a restricted, disconnected renderer with access
only to the verified artifact and approved inputs. It gives the
catalog the source artifact digest and bounded JSON bytes rendered by
the pinned Compose CLI. The catalog matches the source digest to the
verified bundle and checks every rendered service against the signed
manifest digests registered by the existing Docker importer. Require
digest-only references and explicit never-pull, and refuse unsupported
image acquisition paths before contacting Docker. Use the model byte
digest and release pin as the handoff to the installer.

A renderer can access files and includes before a normalized model
exists. Rendering belongs to the installer sandbox. Workload
privileges, secret and mount admission, egress policy, authorization,
audit and startup are separate gates. The deployer verifies the same
rendered digest before use and supplies pull never and no-build.
A parser of raw Compose YAML would duplicate interpolation, merge and
profile semantics. Querying tags, registries or image metadata would
lose the signed image identity or the offline property.
[Compose up](https://docs.docker.com/reference/cli/docker/compose/up/).

A disconnected Docker Compose 5.0.0 prototype rendered two digest-only
services using the pinned Docker development fixture. It preserved the
digest references and never-pull policies. Activating all profiles
included the optional worker, and the volume used the canonical
object representation.

Reuse the existing BSD-3-Clause Go standard library and signed OCI
verification. The Compose test CLI and Docker fixture use their
existing Apache-2.0 terms. Those terms allow commercial and offline
use and redistribution with the applicable notices. The catalog
ships no new binary, dependency, model or dataset.
[Go license](https://go.dev/LICENSE),
[Compose license](https://github.com/docker/compose/blob/v5.0.0/LICENSE).

Test actual Compose JSON on the existing signed import path. Refuse a
missing or unsigned image, a mutable reference, a pull policy that
permits registry access, a build, provider, init image, model or image
mount, and a mismatched source artifact digest. Return a stable list
of service and image identities and the exact verified JSON digest.

The disconnected Linux amd64 integration rendered the actual signed
Compose artifact with Compose 5.0.0, checked the normalized bytes,
and loaded the one verified manifest into a disposable Docker 29.1.3
containerd image store. The store contained exactly that untagged
digest. The same suite refused unsafe fields and refused a mismatched
artifact or unsigned image before Docker access. No workload was
started.
