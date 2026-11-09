# Admit site release images to tenant pins (NET-102)

The [signed registry installer](signed-registry-bootstrap.md) publishes the
selected Linux amd64 manifests, config and layers in the read-only
`ownsolid/release/<asset-id>` namespace. A tenant pin belongs to a separately
writable registry repository and SQL scope. The [OCI Distribution specification](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pulling-manifests)
specifies pulls by digest. [Harbor immutability rules](https://goharbor.io/docs/2.14.0/working-with-projects/working-with-images/create-tag-immutability-rules/)
illustrate the difference between a tag and content identity.
[Distribution read-only mode](https://distribution.github.io/distribution/about/configuration/#readonly)
separates publishing from serving.

## Authority and current boundary

- [Release proof](../../tools/release/offline/pkg/bundle/image_proof.go)
  exposes only legacy `PreparedImageProof` and `VerifyImageProof`. The former
  calls the legacy prepared verifier. The latter directly calls
  [the legacy manifest validator](../../api/release/v1/manifest.go), which refuses
  the current profile. [The current bundle API](../../tools/release/offline/pkg/bundle/current.go)
  exposes archive inspect, unpack and image reading, but no equivalent durable
  prepared or cached proof verification. Current verification also owns versioned
  release trust. The release owner needs profile-specific prepared, issue-proof
  and verify-proof entry points under the same current manifest and trust policy,
  with the expected archive and manifest hashes. Both catalog/import and
  recovery/restore need the same verification selection. Reinterpreting the
  current manifest as legacy or importing from a TLS digest alone loses signed
  approval and license authority.
- [Registry configuration](../../services/control/internal/platform/registryconfig/config.go)
  has one endpoint, used for writes, verification and consumer references.
  The control configuration owner needs a separate typed private source,
  with its own endpoint, allowed CIDRs, CA and protected credential, and to
  pass it to the module at startup. Read-only source permissions and a separate
  writable tenant endpoint are mandatory. User requests continue to select a
  signed asset ID. The server derives the exact digest and repository.
- Once those prerequisites are available, the import must verify source
  manifest, config and complete layer closure by exact size and digest,
  publish the admitted bytes through the existing CAS and destination write
  path, and retain the source identity and readback in the immutable
  workspace-scoped inventory. The existing archive and signed license notices
  stay available for offline export and restore. Avoid a general registry
  proxy, mutable tags, fallback sources and cross-origin redirects.

## Safe change in the current module

Verify the signed release and exact asset after scope checks and before
committing a new pin intent. An unapproved asset or digest, unavailable
release or wrong profile must consume no pin capacity, idempotency record
or writable registry call. Existing idempotent replays must still recover
their original result even when the source is unavailable. Recheck scope
and license in the final reservation transaction and retain the existing
acknowledgement, ownership and rollback behavior for admitted writes.

Exercise the release-profile refusal, wrong asset and workspace, replay
and rollback using real PostgreSQL, and the authenticated digest namespace
with Distribution. A completed positive import from the installed site
requires the profile and configuration boundaries above.

No dependency, dataset or model is added. The test registry remains
[Distribution v3.1.2](https://github.com/distribution/distribution/blob/v3.1.2/LICENSE)
under Apache-2.0. The Go runtime is [BSD-3-Clause](https://go.dev/LICENSE),
PostgreSQL uses the [PostgreSQL License](https://www.postgresql.org/about/licence/),
and the existing test extension pgvector uses the
[PostgreSQL License](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE).
These permit commercial use, redistribution with the applicable notices
and offline operation.

## Linux amd64 result

The [isolated runner](../../services/control/internal/modules/imageregistry/testdata/run-site-admission.sh)
passed on PostgreSQL 18.6 and Distribution 3.1.2 with only loopback.
The fixture ran writable and read-only TLS listeners in one namespace.
The read-only endpoint returned the exact site manifest, config and layer
digests and refused an upload. It also served an unapproved manifest to
show that registry availability alone does not authorize a pin.

The real current release unpacker accepted a small signed current-profile
fixture. The legacy prepared proof refused its inventory. A separate
synthetic legacy-profile fixture exercised the existing import entry point,
local CAS, tenant registry writes and readback. An actual SQL failure
rolled back the pin and its key. Retrying that key created one pin with
signed source digests and license notices. A different workspace could
not retrieve it, and replay returned it after the prepared source became
unavailable. This does not qualify a DEL-108 bundle-to-pin import.
NET-102 remains open.
