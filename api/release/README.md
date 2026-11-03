# Signed release contracts

This Go module owns closed first-party manifest/signature structures, strict JSON,
asset identity/license/profile requirements and operator/runtime recovery formats.
The factory and bundle verifier consume these contracts; the library does not
manufacture an image, create a package, grant business authority or install a
deployment. Third-party OCI parsing deliberately retains its separate extensible
UniqueJSON boundary.

[Manifest.Validate](v1/manifest.go) applies bounds and declared capability rules.
RequiredAssets returns a fresh requirement map; callers cannot mutate a different
release. M19 needs schema17 and its public trust/validation assets. M28 needs at
least schema27; its production amd64 profile requires the matching probe image.
Only the additional named local-arm64 probe role may use arm64. See the
[M28 release contract](../../docs/milestones/m28-private-cluster/release-contract.md).
These fixed protocol invariants are not deployment environment switches.

The unique final `mvp-postgresql-cpu-v1-linux-amd64` selection binds the
`mvp-postgresql-cpu-v1` application and schema lineage, exactly M01–M30, schema29
and predecessor22–29. The [versioned SQL descriptor](v1/mvp-postgresql-cpu-v1-lineage.json)
retains all22 account-prefix checksums and the seven independent MVP migrations;
the mandatory signed `schema-lineage` asset must contain its exact bytes. This
is independent of the historical platform27 catalog. It retains every cumulative
CPU and prepared-cluster delivery asset, and requires the inference image/recipe/
terms, native PostgreSQL client origins, current MVP component origins, deployment
prerequisites and amd64 private-cluster probe image. Component origins and runbooks
cannot substitute for a missing executable, library, model or other runtime input.
The declaration alone establishes no built image, native/vendor connection,
installed deployment, upgrade or four-store recovery qualification. The declared
CPU profile does not turn GPU or external-cluster execution into CPU execution.

The distinct `m23-postgresql-cpu-cache-linux-amd64` selection binds
`releases-cache-v1`, account22, predecessor22, and exactly M01–M17 plus M23. It
retains the existing cumulative CPU assets and installed Engine/Compose
prerequisites. Cache enablement remains the typed runtime configuration owner's
optional policy: SQL is authoritative and disabled cache performs no connection.
The declaration does not activate M18–M22 or later owners, nor change the old
M16/M17/M18 profiles. This is signed metadata admission for the selected source
slice; a current built Control image, complete factory/consumer package and
offline installation/upgrade/recovery qualification remain separate gates.

The separate `m18-postgresql-cpu-kubernetes-linux-amd64` profile selects the existing Localization/account22 core and Kubernetes release CLI for an already prepared cluster. It retains every cumulative asset, requires M01–M18, the signed chart, both operator ELFs, Linux amd64 Helm and component-origin metadata. Its declared predecessor window is account21–22. Legacy Ubuntu host-install and M16/M17 profiles retain their original rules; metadata admission does not establish actual assets, migration, installed-cluster qualification or the unsupported Kubernetes dynamic release-view variant. New-profile complete wire race/vet passes (21 roots/179 entries); factory/default/current consumer and real manufacturing checks remain pending at this source checkpoint.

Keep producer, factory, signed image-subset verifier, configuration examples and
consumers aligned when changing a format or requirement. Old signed bytes and
failed receipts remain immutable. Synthetic manifest tests establish metadata
admission only; real assets, signatures, offline installation and target execution
need their owning acceptance.

After storage/resource admission and pinned toolchain setup:

```sh
go -C contracts/release test -race -count=1 ./...
go -C contracts/release vet ./...
```

Factory refusal and archive/lifecycle tests live under
`tools/release/offline/internal/factory`; tests requiring an actual private mounted
filesystem must use their existing isolated runner. Never substitute fake image
bytes or a fixture profile for a production manufacturing result.
## Exact restore compatibility authorization

The additive [restorecompat](restorecompat/README.md) package owns the separately
signed account22/four-store CPU restore authorization. Existing v1 manifests and
ordinary same-release requests retain their original encoding and guards.

## Measured PostgreSQL restore roots

The optional `PostgreSQLRestoreRequest.target_roots` binds measured directory
identities in fixed four-store order. The typed runtime configuration still owns
paths; the native restore owner compares current and exclusively locked roots.
Omission preserves legacy JSON. See the [slice contract](../../docs/milestones/m18-kubernetes/postgres-restore-roots.md).
These facts grant no Kubernetes/CSI authority and add no deployment setting.

## Native MVP profile selection

`v1/native_postgres_reference.go` defines safe cross-module references to the
original native MVP backup and complete restore. `Valid` checks shape only:
schema29/108, exact application selector, original file/canonical-receipt hashes,
and matching optional registry snapshot/import references. It does not verify a
signature, inspect a filesystem, execute SQL, import files or authorize replay.
The scope must independently compare `source_manifest_sha256` with its verified
signed source and preserve the signed profile. Native backup `candidate_sha256`
is distinct from the ordinary offline wrapper's backup manifest digest.

Backup completion and its durable independent readback remain separate original
files; a completion hash must never be written into the readback it already
hashes. Restore references require the original completion, files readback and
readonly post-restore observation. A missing completion yields no complete
reference. Registry receipts are hashed as canonical original typed JSON; paths,
directory identities, connection settings and credentials are never projected.
Original native typed-value hashes use `json.Marshal(value)` followed by one LF,
as defined by the native owner's existing `canonical` function. File/stdout
hashes cover their exact original bytes. Native request hashes also differ from
the offline wrapper's separate source-request hash; consumers must preserve each
identity instead of exchanging similarly named digest fields.
`files_readback_sha256` binds the original durable restore readback. Each fresh
readonly observation has its own time and `observation_sha256`; consumers compare
the static original binding/data/file hashes when reopening, rather than treating
an observation-time change as permission to repeat a write.
The native owner retains all verification and effect ordering. These DTOs do
not establish a working offline native adapter or installed recovery acceptance.

Native PostgreSQL backup/restore requests and restored runtime contexts share
optional `application_profile`. Omission preserves historical encoding and the
legacy `releases` selection. Explicit values are exactly `releases` or
`mvp-postgresql-cpu-v1`; empty, null, aliases and unknown selectors refuse. Their
`EffectiveApplicationProfile()` methods never infer a profile from a schema
number. Explicit MVP contexts require the new schema29 lineage (108 tables),
which is independent of old platform27. Legacy context validation retains its
historical positive-version shape; the native consumer keeps owning its exact
legacy schema check. These selectors do not establish actual SQL/native recovery
or complete release manufacturing qualification.
