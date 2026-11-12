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
  configuration owner passes its protected file through the explicit startup selection below. Read-only source permissions and a separate
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

The module constructor is additive; the control composition now passes the
protected configuration file when the site source is explicitly selected. The consumer uses the verified
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

The shared startup selection below now passes that typed reference. Neither an
HTTP request, an ambient business-module environment fallback nor a writable
registry response chooses it. Preserve the selected-store schema, installed
tenant, initialization and owner checks and the existing Close lifetime.
Configured source failures abort startup. Omission retains archive-backed mode
only after the module's existing disabled-owner admission has run.

The module native profile covers distinct TLS identities, current signed release
selection, exact digest reads, missing-source refusal and SQL rollback/recovery.
The shared-root qualification below covers both constructor modes and invalid
references. Those source-scoped results do not replace an installed real release
import and its tenant pin readback. NET-102 remains open.

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

Startup review found that the root skipped `openImageRegistry` entirely when
the optional configuration was omitted. That bypassed the existing module-owned
`RequireEmptyWhenDisabled` admission. Invoke the selected-store constructor
for both enabled and omitted configurations, while keeping the bootstrap fence
for configured operation and registering Close only for a constructed service.
Verify an empty omitted owner can start, but one retained pin refuses omission
without changing that row. Keep this check in the startup path rather than
duplicating Registry policy in readiness or inventing a new schema.

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

Input 387db4e95ca9a317a6799c71aa62310020ae8261 also runs the existing
disabled-owner admission from the real startup root. Its complete server race
suite passed on Darwin arm64 in 16.359 seconds, retaining the unchanged
configuration proof above. The same network-none Linux arm64 PostgreSQL fixture
passed the extended startup test in 0.49 seconds without skips: an empty omitted
owner is admitted, a retained importing pin refuses omission, and the refusal
leaves exactly that pin unchanged. Its server race and PostgreSQL log SHA-256
values are bbbf597f50103e8733eb1c3fe8fe53372f3f5a012355f37325492b7a44e56876
and 76572262940e149bb4681a86cc77676574fd3157fa77a1b3179bcbd00b324fc7.

After rebasing onto the formal schema-59 source 4f76102c, input
96eeef81b104932f60505375ff63a746bb04dabd passed the real startup test
without skips on the same network-none Linux arm64 PostgreSQL fixture. It
disposed exactly 119 owned tables. The PostgreSQL log SHA-256 is
9b46a6dea643d8f8869834d3176f75815189afc3b3b271bebfbd01298cd3fc6c.

The same input passed make check, make build and make test on Darwin arm64.
Console passed 1,075 browser cases and Portal 389, with their existing 28 and
nine live cases skipped and no added skip or retry. Go, Rust and Web unit
tests passed. The first gate attempt failed four code-generation assertions
because this new worktree had no offline Web dependencies. Installing the
locked dependencies from the existing cache, with zero downloads, corrected
the setup order; neither product code nor assertions changed. The passing
PostgreSQL result was reused rather than repeated. The successful check,
build and test log SHA-256 values are
167f7bd61b5796562288ae13f1779f20f297294a569cd1b0b6388a06468ba027,
536e832e0a186fe4cbd8d9fc368d19932177bdbe23e3b016e5251b45c07e788b
and a1a6fd01f973439403f7df3f85ffc4cbddffc8637abbc94b8a3b0e1c88bbf3ab.
This source includes both APP dispatch integration test files; their explicit
repository_integration and assistant_http_dispatch constraints keep them out
of the default gate. These results do not claim that tagged profile ran.

Installed source-to-pin acceptance remains with OBS and DEL, and NET-102
remains open.

## Installed release-to-pin acceptance preparation

Use the actual public Admin Registry handler, normal login/CSRF, and a new
independent session_file for this optional acceptance input. The existing site
module fixture already qualifies native digest transport and rollback; do not
repeat it with a synthetic signed archive and call that installed acceptance.
At reviewed main 38aad60337d3e696cd3536bab932b100c9013a45, the public handler
provides release-images, pins, exact-pin reads, reference inspection and the
remove/reconcile-removal actions. Registry HTTP schemas are not present as an
OpenAPI document in the existing policy/problem contract directory; retain that
contract/publication boundary rather than inventing additional response fields.

Prepare an external stdlib client against those existing routes. Require an
actually approved signed release asset, independent trust/license evidence and
exact manifest/config/archive digests and size from the release owner. Match the
normal release catalog before importing, require an empty disposable Workspace
pin list, preserve the same mutation key for replay, and verify the committed
pin through a normal scoped GET. The public Pin does not expose its private site
source receipt: verify that through the existing owner protocol or independent
installer readback, not a made-up HTTP field or a client assertion. These materials
and an installed fresh instance are mandatory; leave the item open if absent.

Record the returned owned pin before subsequent operations. Cleanup can only
address that exact pin through the existing authenticated remove/reconcile path
and must not delete shared CAS files, unknown rows or earlier pins. Keep a failed
or uncertain operation explicit and retain the fresh session for safe retry.
Use a separate new session file and never open the old 4463 session. Configuration,
pin writes and cleanup belong before the history driver's exclusive quiet-count
phase; never overlap that phase or introduce a second history actor. This input
must not delay the already prepared GW120/Help/Scope/CLI acceptance path.

Before running the external client, test normal TLS/Origin/CSRF wiring, no
redirects, response and deadline bounds, missing approval, wrong digest/scope,
replay identity, source-independent committed readback, and cleanup ownership
using controlled HTTP replies. Those cases test the client, not server identity,
permissions, PostgreSQL durability or installed release qualification. A real
run still needs a newly selected coordinator window and its source/binary and
instance bindings. No install request or Linux admission is authorized by this
preparation. Existing Go BSD-3-Clause, Registry Apache-2.0 and PostgreSQL license
requirements above remain unchanged; no dependency, model or dataset is added.

### Selected external client source

The selected external source is
/root/lane-obs-tools/installed-registry-driver-v5. Its source-inputs.json
SHA-256 is 800a63ca0f5b56418cac5d6bbacad5826305b130a7ab5a7b10071b89d0df7f84.
The retained 81,920-byte source archive has SHA-256
1a904372e8ca094b6c4f6db84883b17dee3a2bc08cf301221baf1c55b4843358.
The optional input descriptor is
/root/lane-obs-tools/installed-registry-optional-input-v5.json; it explicitly marks
installed_input_ready=false and binds this source, the required real materials
and the pre-history schedule. Earlier source drafts remain unrun and unselected.

The client has separate init, verify, cleanup and logout phases using normal
Admin login/CSRF and an independent protected session. It records an import
intent before sending its fixed key, so a lost response can be resolved by that
same key without guessing a pin. That intent is not a delivery receipt. The
baseline requires an explicit null continuation and an empty Workspace list;
a missing next_cursor does not establish absence. Cleanup uses only an
acknowledged pin identity and current reference checks. A deleted tombstone does
not establish local cleanup: without its prior acknowledgement, the client
uses the existing explicit reconcile-removal path before releasing its session.
No direct CAS or database cleanup is introduced.

Sixteen focused tests are prepared with controlled TLS/HTTP peers, temporary
credentials and metadata. Their normal flow uses distinct source and tagless receipt values. They cover
the client flow, source/digest/scope and
input boundaries, replay and lost acknowledgement, cancellation, TLS/redirect
refusal, cleanup uncertainty and session isolation. gofmt passed on both host
copies and the Linux source manifest verified. The workstation nonblocking
flock refusal started no compiler or tests. No binary has been qualified or
executed, no installed request or Linux lock attempt was made, and no old
session or real credential file was opened. Syntax formatting and controlled
client tests cannot substitute for native or installed qualification.

The actual instance, approved signed asset/trust/license material, independent
fresh session, source/binary binding, independently checked source receipt and
verified owned cleanup are still required. This is an optional preparation
input only. It must not delay the already ready main acceptance flow or overlap
history's quiet-count stage. NET-102 remains unticked.

### Source archive and tagless transport are different receipts

The public release catalog reports the signed asset's original archive size.
The public pin reports ImportReceipt.TransportSHA256 and TransportBytes from
[WriteTagless](../../tools/release/offline/internal/factory/import.go), invoked by
[prepareImage](../../services/control/internal/modules/imageregistry/cas.go).
They cannot share one expected archive field. The original source SHA/size belong
to the approved asset; the tagless SHA/size belong to the derived runnable-platform
archive. The manifest/config digests remain the selected runtime descriptors.

WriteTagless checks the unchanged source and its signed SHA/size, preserves the
selected runtime blobs in source order, emits fixed USTAR headers without a wall
clock, and constructs a single-platform index. Its receipt is deterministic for
the same unchanged approved source and selected inventory. Derive the expected
transport receipt with that existing production library over the actual approved
asset, not a hand-written repacker, an assumed equality, or a synthetic archive
substituted for installed acceptance. The private source receipt still requires
independent owner verification.

The unrun client draft had conflated those two sizes. Correct its private input
to retain original source SHA/size and derived tagless transport SHA/size
separately, match the former catalog size and the latter pin metadata, and use
distinct values in controlled client tests. None of the earlier drafts was
compiled or invoked against an installation; no installed failure or passing
qualification is inferred. Missing actual transport material keeps this optional
input unready and leaves the main acceptance schedule unchanged.
