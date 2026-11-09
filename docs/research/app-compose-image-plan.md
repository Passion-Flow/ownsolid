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
image acquisition paths before contacting Docker. The handoff binds the
release pin, signed source digest, exact model byte digest and ordered
service-to-manifest pairs to the installer's scoped operation.

A renderer can access files and includes before a normalized model
exists. Rendering belongs to the installer sandbox. Workload
privileges, secret and mount admission, egress policy, authorization,
audit and startup are separate gates. A model can preserve every
image rule while replacing a command, mount or privilege setting.
Rechecking image rules alone would admit that replacement at import.
Pass the trusted plan recorded at admission back into the importer
and compare it with the complete plan rederived from the staged
bundle and the rendered bytes. At startup, recheck the retained
model bytes against that plan and pass the same private buffer to the
launcher. Compose can read a sole configuration from standard input
and disable pulls and builds. The installer owns the execution
context, including profiles, environment, interpolation and project
paths, and must preserve the admitted model when consuming it.
[Compose CLI](https://docs.docker.com/reference/cli/docker/compose/),
[Compose up](https://docs.docker.com/reference/cli/docker/compose/up/).

A parser of raw Compose YAML would duplicate interpolation, merge and
profile semantics. Querying tags, registries or image metadata would
lose the signed image identity or the offline property.

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
Use signed fixtures to verify that changing only the command still
passes image policy but fails the admitted plan comparison before
Docker access. Also refuse a missing model or mismatched pin, source,
service binding or exact model bytes. The tagged Docker fixture
derives its expected plan before image import and verifies the
retained bytes before passing control to a prospective launcher.

The installer integration needs an authorized installation with a
correlated audit record containing the pin and digests. Exercise a
replaced source file before rendering, a command or privilege change
with unchanged images after admission, a staged-file replacement
before launch, and a stale release pin. Deny cross-scope use of the
admission record, verify that rejected attempts reach no launcher,
and keep the installation unsettled when startup fails.

## Service environment and label inputs

A Compose 5.5.1 render-only fixture retained an `env_file` reference
using `config --no-env-resolution`. Changing the file from
`CATALOG_MODE=serve` to `CATALOG_MODE=debug` changed evaluation of the
same JSON. Normal environment resolution embedded the original value
and removed the file reference. The same probe resolved a null
environment entry from the process environment on replay. It also
retained a `label_file` path and read a newly added label from that file
on replay, while existing inline labels took precedence.
[Compose config](https://docs.docker.com/reference/cli/docker/compose/config/),
[Compose service inputs](https://docs.docker.com/reference/compose-file/services/#env_file).

The first Compose path accepts environment values as explicit strings
in the normalized service environment object, including empty strings.
Omit keys whose values should stay unset. Use inline labels and omit
`env_file` and `label_file` from the normalized model. Resolve approved
non-secret inputs inside the trusted renderer and use the same
normalized bytes for admission and launch. The catalog refuses file
references even for optional files and refuses null environment values
at admission, image import and launch verification, including plans
retained from an earlier verifier.

Hashing an external path or checking its content only at admission
would leave Compose free to reopen a replacement. Admitting mutable
external inputs would require a new signed inventory and a launcher
that consumes locked content. Keep the first path inline. The installer
owns the interpolation environment, project directory and profiles.
It must disable implicit project `.env` loading, omit launch-time
`--env-file` overrides, and preserve the effective admitted model.
Configs, mounted secrets, volumes and other workload resources stay
under separate installer admission. Deliver credentials as scoped file
references outside the model's environment values.
[Compose variable interpolation](https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/).

Use the existing signed bundle fixtures. Verify that the same rendered
JSON with an `env_file` reference is refused before Docker and by the
launch verifier even when it matches a retained plan digest. Exercise
optional and raw file entries, label files and null environment values.
On the pinned disconnected Compose and Docker fixture, change the
referenced file, then verify the model with embedded values keeps the
original effective environment and imports exactly the signed image.

## Disconnected image import

The initial disconnected Linux amd64 integration rendered the actual signed
Compose artifact with Compose 5.0.0, checked the normalized bytes,
and loaded the one verified manifest into a disposable Docker 29.1.3
containerd image store. The store contained exactly that untagged
digest. The same suite refused unsafe fields and refused a mismatched
artifact or unsigned image before Docker access. No workload was
started.
