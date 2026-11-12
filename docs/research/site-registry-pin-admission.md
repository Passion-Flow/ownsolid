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
  uses the signed release profile for prepared verification and cached proof
  readback since the [DEL-108 proof change](https://github.com/Passion-Flow/ownsolid/commit/b30e8665).
  The consumer selects the OCI reader using the verified profile returned
  by that proof or by full prepared verification during reconciliation.
  It does not retry a refusal under a different profile. The original
  release verification still owns the license notices and approved digest.
- [Registry configuration](../../services/control/internal/platform/registryconfig/config.go)
  has one endpoint, used for writes, verification and consumer references.
  The module now accepts a separate typed private source, with its own
  endpoint, allowed CIDRs, CA and protected credential. The control
  configuration owner still needs to pass its file at startup. Read-only source permissions and a separate
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

## Initial Linux amd64 result

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

## Read-only consumer slice

Use an explicit protected source settings file at module composition. The
typed file owns the site HTTPS origin, private CIDR allowlist, CA file and
credential file. The writable configuration still owns the tenant registry,
and the two endpoints and credential pairs must be distinct. Reuse the
existing private dial, certificate and redirect rules for both. The source
wrapper can only construct GET requests under the signed asset's exact
`ownsolid/release/<id>` repository. A remote Location, tag, host or
repository from a user, manifest or provider never supplies request routing.
The [OCI pull contract](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pulling-manifests)
defines digest pulls and descriptor verification. [Go's HTTP client](https://go.dev/src/net/http/client.go)
supports refusing redirects before sending a second request.

Before any source I/O, keep existing actor, license, idempotency and signed
prepared proof checks. Use the verified archive's manifest and blob
descriptors to stream the exact site responses into private CAS staging.
Keep the verified tagless archive for offline export. Missing, redirected,
wrong-size or wrong-digest bytes fail preparation without falling back to
the archive or writing the tenant Registry. Once all reads verify, bind the
source origin/CA fingerprint, repository and selected manifest digest to
the immutable inventory with its existing signed asset, archive digest and
license notices. Existing inventories omit the new receipt. A committed
key replay retains its original pin. A complete staged inventory supports
explicit reconciliation after a SQL rollback without reading the site
again. An incomplete stage retains the existing abandon and discard flow.

The module constructor is additive and the control composition owner still
needs to pass the protected configuration file. The consumer uses the verified
profile for initial archive reads and retry after SQL rollback. Legacy bundle
admission, tenant permissions and destination routing are unchanged. Verify against
the small loopback Distribution/PostgreSQL fixture with distinct source
credentials and the same authenticated import entry point. Check wrong
origin and trust, missing source bytes despite a usable archive, successful
source readback and a post-read SQL rollback followed by explicit retry.
No new runtime dependency, model or dataset is added.
### Verified consumer boundary

The loopback runner passed with real PostgreSQL and writable and read-only
Distribution listeners using different credentials. The module constructor
selected the read-only listener. Its import refused the absent signed manifest
even though the local archive and the other site blobs were available.
The writable tenant path stayed empty and key replay returned the same
unbound intent. The incomplete intent was explicitly abandoned and discarded.

After seeding the approved manifest, import streamed the site closure into
CAS. An actual SQL bind rollback left the exact staged inventory intact.
Explicit reconciliation completed the same tenant pin with source I/O
disabled. The immutable SQL inventory matched the local cache and preserved
the site receipt, signed archive digests and license notices. The destination
verified every descriptor, and a different Workspace could not read the pin.
Unit refusals covered a missing trust or credential file, a source using the
destination authority or credential pair, a redirect toward the destination,
and asset names that could escape the release namespace. The small signed
current-profile fixture uses synthetic OCI content. An incorrect expected
release digest was refused before reservation. The installed DEL-108 release
group and control startup still require integrated acceptance.

## Selected startup handoff

The module entry is
NewWithSiteReleaseSource(identity, workspaces, nodes, deployments, license,
destinationConfig, sourceFile) in
[repository.go](../../services/control/internal/modules/imageregistry/repository.go).
Its first six arguments match New. The final nonempty file reference selects
the independently configured, read-only source. The source JSON contains
endpoint, allowed_cidrs, ca_file and credential_file; the constructor reads
it as protected configuration. New keeps the existing signed-archive path.

At main 785cc4eb,
[openImageRegistry](../../services/control/cmd/server/imageregistry.go)
still calls New. The composition owner must introduce and validate an
explicit typed startup file reference before selecting the site constructor.
Neither an HTTP request, an environment fallback in the business module nor
a writable registry response can choose that reference. Preserve the
selected-store schema admission, installed tenant, initialization checks,
owner identity and service Close lifetime. Missing or invalid source
configuration must refuse configured startup rather than silently select
the archive path. Leaving the site source unconfigured retains the
existing archive-backed mode.

The module's native profile already covers the two distinct TLS registry
identities, current signed release profile, exact digest reads, missing
source refusal without archive fallback and SQL rollback/reconciliation.
The root owner still needs startup tests for both explicit modes and
invalid references, then the installed release-to-pin flow on the same
source commit. This handoff does not change that acceptance status or
qualify a new deployment.

### Shared control startup selection

The shared startup root selects the existing module constructor with an optional
`image_registry_site_source_file`. The canonical protected JSON reference
requires the writable `image_registry_config_file`. The explicit protected
environment file may override a valid YAML reference through
`PROJECT_ONE_REGISTRY_SITE_SOURCE_FILE`. Invalid original YAML, empty overrides,
unknown or duplicate settings fail. Ambient process variables grant no selection.

Keep source JSON decoding, TLS and credential admission, private CIDR policy,
source/destination separation and transport cleanup in
`NewWithSiteReleaseSource`. The shared root first retains selected SQL,
initialization and installed-tenant checks, then passes the source file. An
explicit source constructor failure aborts startup. The existing lifecycle
closes the returned Registry service during shutdown and later startup failures.

The [OCI digest pull contract](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pulling-manifests)
and [Go HTTP client redirect control](https://pkg.go.dev/net/http#Client.CheckRedirect)
remain the transport basis. A second source parser in the shared root would
duplicate the module's policy and could drift. An automatic archive fallback
would accept bytes through a source the operator did not select. The existing
Go, PostgreSQL and Distribution licenses above apply, with no new dependency,
model or dataset.

Verify strict optional selection and overrides, a source without a writable
Registry, and missing, linked or public source files. Through the actual startup
constructor, test invalid trust and credential references, public CIDR policy,
unknown scope fields and identical source/destination authority. Refusals must
leave deployment Registry hooks unbound. Successful construction must use the
existing Close chain. Cross-tenant pin reads, byte-source egress and signed
import acceptance remain covered through the Registry owner's real protocols.
Use the configured startup deadline for constructor calls, as the real startup
root does. The disabled-owner check enters a repository transaction and must
retain its deadline requirement; an unbounded test context is not a valid
startup fixture.

The shared-root regression sources are
`TestImageRegistrySiteSourceSelection`,
`TestImageRegistrySiteSourceRefusesMalformedSelection`,
`TestRegistrySiteSourceRequiresWritableOwner` and
`TestRegistrySiteSourceStartupAdmission`. The last test needs an owned
PostgreSQL fixture selected through `PROJECT_ONE_TEST_DSN_FILE` and proves
constructor and installation admission. Run the focused configuration,
startup and existing lifecycle checks with:

```sh
go -C services/control test -p 1 ./internal/platform/config ./cmd/server   -run 'TestImageRegistrySiteSource|TestRegistrySiteSource|TestRuntimeNormalCancellationAndReverseOwnerJoin' -count=1
```

Input 8025163372b36b18e2e9352a3c241e0e917a0622 passed the complete
configuration and server package suites with the race detector on Darwin arm64
in 54.171 and 16.269 seconds. The first real PostgreSQL run passed all twelve
source refusals and both configured constructors, then refused the unbounded
disabled-owner test context with REPOSITORY_TRANSACTION_DEADLINE_REQUIRED.
The repository guard and every assertion remain unchanged.

Input 2d1513b0d549110f20b18579bdbc3c2ef982405c corrects that fixture to
use the configured startup deadline. Its complete server race suite passed in
16.872 seconds; the unchanged configuration suite retains the earlier result.
The real TestRegistrySiteSourceStartupAdmission passed without skips in
0.52 seconds on Linux arm64, PostgreSQL 18.6 and pgvector 0.8.7, in a Docker
network-none fixture on the Darwin arm64 workstation. It exercised installation
admission, all twelve source refusals, both explicit startup modes, idempotent
Close and disabled-owner admission. No Registry request occurred during
construction. The owned fixture's 118 tables, container, volume and credential
files were removed. The successful server race and PostgreSQL logs have SHA-256
4ce26a15b1a3a2760126d6d9ca5e576c21106a34fe271c8d0564485a2b49722e and
4330a5fdaabf9f4b85fd23e52b3eb9d5f981ab3720b562c59dcb724564276cde.

The shared-root candidate has not passed the full repository gate or installed
source-to-pin acceptance. That installed flow remains with OBS and DEL, and
NET-102 remains open.
