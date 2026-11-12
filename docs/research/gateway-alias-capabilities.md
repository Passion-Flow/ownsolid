# Gateway alias identity and capabilities (GW-104)

Reviewed 2026-11-13.

[LiteLLM deployment and model group documentation](https://docs.litellm.ai/docs/router/learn/deployments_and_model_groups)
separates the name callers request, deployment identity and metadata. A
published routing change replaces a registry snapshot. [Portkey's model
catalog](https://portkey.ai/docs/product/model-catalog) separates model
discovery and model details from provider credentials. The [OpenAI model
object](https://developers.openai.com/api/reference/resources/models)
provides a name, creation time and owner in the compatible model list.

## Local alias authority

Go publication and cutover validate the tenant, workspace, gateway node,
deployment and exact ready revision. The machine authorization snapshot and
the later durable admission already return the alias ID and deployment
revision. The Rust gateway authorizes the name before queueing and admits
it again after the queue. Go uses the same alias row for a retarget. Before the fix, Rust
compared only the workspace across those answers. An inconsistent
admission for the same public name reached the backend in the regression
fixture and produced HTTP 200.

Compare the stable alias ID from the first answer with the alias ID of
the durable admission before any backend call. Keep the fresh deployment
and backend selection when the alias ID is the same, so a valid retarget
can serve queued traffic. If the ID differs, persist the existing
terminal gateway-unavailable outcome to release the admission, with null
usage and no generated output. Comparing the whole binding would also
reject legitimate target revisions. Relying only on the public name leaves
an inconsistent control response undetected.

## Capability discovery

Keep the OpenAI-compatible list's existing shape. A versioned discovery
projection can use the exact ready target and serving recipe, plus the
enabled gateway adapter. The tokenizer currently enforces the tighter of
the runtime context window and the admission limit. A public capability
projection still needs DEV to define its fields and route, including
unknown values and how capabilities follow a cutover. Tools, embedding and
rerank require qualified serving support for their respective routes.
A model's family name or tokenizer window alone is insufficient evidence
of those capabilities.

## Licenses and verification

No dependency, model or dataset is added. The Rust routing fixture uses
the existing [Axum](https://github.com/tokio-rs/axum/blob/main/LICENSE),
[Tokio](https://github.com/tokio-rs/tokio/blob/master/LICENSE) (MIT),
[Serde](https://github.com/serde-rs/serde),
[UUID](https://github.com/uuid-rs/uuid) and
[reqwest](https://github.com/seanmonstar/reqwest) (MIT or Apache-2.0).
The licenses allow commercial use, redistribution under their notice
terms and offline use. The fixture replies contain synthetic text.

- Make the synthetic control return a valid admission for a different
  alias ID under the same name and workspace. Assert that the native HTTPS
  gateway fails before tokenization, records the original request's
  terminal outcome without usage and leaves no generation.
- Change only the target identity and revision in the admission for the
  same alias. Require successful generation with the correct public name
  and a single successful outcome. Existing Go/PostgreSQL cutover tests
  separately exercise the real persistence and authorization boundary.

The Linux loopback-only run rejected the different alias with HTTP 503,
made zero tokenizer and generation calls, and delivered one canonical
outcome with null usage and no output. The valid retarget produced one
generation and a receipt with nine tokens. Disconnected PostgreSQL
covered ready-revision publication, cutover and same-key rollback, and
the existing Admin TLS cutover contract. The Rust HTTP model backend
was a synthetic fixture. No serving recipe or public schema changed.

## Scoped target metadata candidate

The current deployment owner validates ready state, its exact revision
and lease, the scoped model, the pinned serving image and a positive
generation observation in `ReadyGatewayTargetWithin`. Model import
verifies the approved base catalogue by digest. The derived model owner
validates its immutable catalogue and context field before serving.
[vLLM's server contract](https://docs.vllm.ai/en/latest/serving/openai_compatible_server/)
separates text generation from tasks such as pooling and tool calls.
The [compatible OpenAI model object](https://developers.openai.com/api/reference/resources/models)
does not describe those capabilities.

Provide a separate server-internal, opt-in projection using the existing
Admin and Portal alias visibility and a single scoped transaction.
Require the current alias revision's immutable target snapshot to match
the exact ready deployment, model catalogue and serving recipe. For an
identified ready target, report verified text generation and the
configured context ceiling. Use the minimum of the pinned model limit,
serving recipe limit and current gateway admission policy. The live
tokenizer can apply a tighter limit. Missing or mismatched snapshot,
unknown kind or digest, disabled alias, unavailable deployment and
inactive workspace yield no metadata. The candidate PostgreSQL path
requires the local cell on the alias, target and deployment.

A name or family match could describe the wrong model after a retarget.
Reading the latest history row without matching the alias revision could
turn an old target into a current promise. Reading the host's agent
capability would describe its operator profile instead of its deployment.
Keep metadata reads off machine authorization. Human HTTP reads use the
separate composition below, with capability publication disabled until
DEV publishes the field and clients.

Test the projection through the real PostgreSQL authorization service
for an Admin and Portal member, a missing old snapshot, a disabled alias,
a stale deployment revision, and foreign workspace or tenant refusal.
Retarget and resume the same alias and verify the new target identity.
Compare the existing HTTP response against the released schema. Verify
the recipe selector refuses unknown digests and recipes. No dependencies,
models or data sets are added. The existing Go and PostgreSQL tools,
Apache-2.0 model catalogue and pinned engine remain within their notice,
commercial use and offline redistribution boundaries.

### Versioned contract handoff

The released `ModelAlias` and machine binding schemas are closed.
Keep the compatibility model list and machine admission unchanged.
For the Admin and Portal cell-scoped alias detail and list operations,
the proposed optional `capabilities` object can carry
`text_generation_verified` and `context_token_ceiling`. The first is a
positive assertion backed by the exact deployment's verification.
The second is a positive integer ceiling from the model, recipe and
policy, subject to the live tokenizer. When the server has no exact
proof, omit the object. An empty or false value would conflate missing
proof with a verified negative. The projected alias ID and revision
and deployment ID and revision must match the surrounding alias view.
Produce both views within the same transaction when wiring the
contract. Maintain existing disabled-alias visibility and the
not-found boundary for unauthorized scopes.

The candidate intentionally carries no provider address, credentials,
model manifest, raw observation, or API adapter capability. A claim
for tools, pooling, rerank, multimodal input or an individual gateway
adapter needs the owning versioned serving and gateway capability
source. DEV owns the schema, generator and operation binding. WEB
owns the catalogue display and its missing-metadata state.

### Disconnected qualification

`TestPostgresAliasCapabilitiesFollowVerifiedScopedTarget` passed with
PostgreSQL 18.6 and pgvector 0.8.7 in a loopback-only Linux namespace.
Admin and a Portal member saw the same verified base target and the
configured ceiling of 1,024 tokens. The service withheld metadata
for an otherwise callable predecessor alias without a target
snapshot and an alias bound to a deployment revision that did not
exist. It preserved cross-workspace and cross-tenant not-found
responses and Portal's hidden paused-alias behavior. After retarget
and resume it returned the replacement deployment identity. The
existing Portal HTTPS detail still conformed to `ModelAliasResponse`.
Deployment observations were synthetic. Recipe selection was tested
separately for unknown inputs and the approved derived recipe.
Production capability publication remains disabled pending the read contract.

With the protected fixture DSN file in `PROJECT_ONE_TEST_DSN_FILE`,
run this from the repository root.

```sh
go -C services/control test -p 1 ./internal/modules/distribution   -run '^Test(PostgresAliasCapabilitiesFollowVerifiedScopedTarget|AliasContextCeilingRequiresRecognizedSource|ActualAliasCutoverTLSNullableContractCSRFHistoryAndExactReplay)$' -count=1
```

### Compatibility boundary and DEV inputs

The shared capability selector backs `Service.AliasCapabilities` and
the registered human read composition. Both use the same workspace
and visibility rules as alias detail. In that transaction it requires the current alias
ID and revision, node, deployment ID and revision, model ID and kind,
catalogue digest and nullable recipe digest to agree across the
local-cell alias, immutable target, live deployment and ready model.
The deployment owner also requires a fresh generation observation.
A valid alias with unavailable or unidentified evidence yields a nil
projection. A paused alias yields no projection for Admin and the
existing not-found result for Portal. A refusal keeps its existing
problem code. The internal result carries the exact alias and
deployment revisions to prevent attaching it to a stale view.

The [OpenAPI Schema Object](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
and the released `ModelAlias` schema keep the current response closed.
Test all four registered Admin and Portal list and detail operations
against their released response schemas. Check that the existing
payload never acquires a capability field and that a workspace without
membership and a foreign tenant cannot read it. Admin still sees a
paused alias in its normal representation. Portal omits it from the
list and returns the existing not-found problem on detail.

The registered read composition binds the optional object to these
exact operations. DEV must publish the schema and generated clients
before enabling its composition switch. Use the same authorization,
page limit and cursor.
For a projected result, require both proposed fields together.
`text_generation_verified` is true and `context_token_ceiling` is a
positive integer. Omit the entire object when the projection is nil.
List and detail must both use the exact target in the same authorized
transaction. Publish the schema and regenerate both clients before
exposing the object. The public model list, machine binding and
mutation responses retain their current fields. No runtime, dependency
or product data changes are needed to verify this boundary.

The versioned operation IDs are `adminListModelAlias`,
`adminGetModelAlias`, `portalListModelAlias` and `portalGetModelAlias`.
The disconnected `TestPostgresAliasCapabilityPublicContractBoundary`
run checked all four through the real TLS dispatcher. Their published
response schemas accepted the replies. Each authorized item retained
its alias and deployment identity without a capability object. The
Portal member could not read a separate workspace, and neither
audience could read a foreign tenant. Admin could still read the
paused alias, while Portal detail returned MODEL_ALIAS_NOT_FOUND
and its list excluded the alias. This run used PostgreSQL 18.6 and
synthetic deployment observations.

```sh
go -C services/control test -p 1 ./internal/modules/distribution -run '^TestPostgresAliasCapabilityPublicContractBoundary$' -count=1
```

## Registered read composition

DEV's [publication boundary](public-api-inventory.md#gw-104-alias-capability-contract-preparation)
fixes the candidate object and the four operations. The released
ModelAlias component is shared by reads and writes and remains closed.
Use an independent AliasReadView for human list and detail, with the
existing Alias embedded and only the two approved capability fields.
Keep mutation results, machine bindings and the compatible models list
on their existing types. A composition-time PublishAliasCapabilities
switch defaults to false until DEV publishes and regenerates the read
contract. It is not a customer policy or an authorization bypass. The
registered handlers use the read type in both modes; the default mode
must still pass the released schemas without new capability queries.

When explicitly enabled, prepare availability and capability evidence
inside the same authorized transaction that selects the alias or page.
Match the current alias and deployment revisions again in the evidence
query. Do not call the standalone capability method after returning a
page, because a concurrent cutover could attach new evidence to an old
alias. Preserve the existing cursor scope and page order. Omit the object
when readiness or exact evidence is absent, including paused Admin
aliases, and preserve Portal's existing visibility rules. A database
failure returns the existing storage problem rather than a successful
response with silently omitted proof.

Verify the actual four HTTPS routes with a mixed page of a verified
alias and a callable legacy alias without a snapshot. Compare the
optional object to the draft closed schema and both revisions to the
surrounding view. Exercise cursor continuation, scope refusal, unavailable
or stale evidence, pause, retarget and resume. Check that mutation results
still pass the released ModelAlias schema while the read projection is
enabled. The existing default-mode contract test must continue to pass.
No new dependency, model or data set is added; the licences above apply.
The current upstream [OpenAPI schema rules](https://spec.openapis.org/oas/v3.1.1.html#schema-object)
and [vLLM serving overview](https://docs.vllm.ai/en/latest/serving/online_serving/)
continue to support the separation of metadata, enabled endpoints and
model tasks. The switch stays disabled in production composition until
publication. Full UI and actual engine qualification remain separate.


### Registered-route qualification

TestPostgresAliasReadCapabilitiesAtRegisteredEntrances passed on Linux
amd64 against PostgreSQL 18.6 with pgvector 0.8.7 in a loopback-only
namespace. The fixture explicitly enabled the actual registered
handler's service. Both audiences returned the exact two-field object
for the verified target and omitted it for a callable legacy alias
without a snapshot. Mixed one-item pages and detail retained exact alias
and deployment identities. Foreign workspace and tenant reads were
refused. A stale deployment revision omitted the evidence; pause hid
the alias from Portal, and retarget and resume exposed the replacement.
The actual cutover replies still passed the released closed mutation
schema while read capabilities were enabled.

With the owned evidence table temporarily unavailable, default-mode
list and detail still passed the published schemas. Enabling capability
publication returned STORAGE_UNAVAILABLE over HTTP 503 for both
audiences instead of treating the failed lookup as absent evidence.
The fixture restored its table and disposed its owned schema. The
internal selector, released default read boundary and recipe selector
regressions also passed. Deployment observations were synthetic; no
model or GPU ran. This verifies backend composition with a draft object,
not publication of a new OpenAPI contract or generated clients.

```sh
go -C services/control test -p 1 ./internal/modules/distribution -run '^Test(PostgresAliasReadCapabilitiesAtRegisteredEntrances|PostgresAliasCapabilityPublicContractBoundary|PostgresAliasCapabilitiesFollowVerifiedScopedTarget|AliasContextCeilingRequiresRecognizedSource)$' -count=1
```

Production composition still leaves PublishAliasCapabilities false.
DEV can bind the four read operations to a separate closed read
component and regenerate Web, Help, Go and Python clients without
adding the object to mutation responses. Activation and the catalogue
UI remain open. The complete repository check and build remain a separate publication
gate on the assembled migration and read-contract source.


### Exact schema-58 source handoff

The minimal gateway-only source and test patch against 106ca52d is
retained in /root/lane-gw-acceptance/gw104-main106-handoff.76uoxxyb,
with contract.json and a SHA-256 manifest. It contains the two read
methods, exact evidence selector, shared alias visibility helper,
composition switch, registered handler calls and focused tests. It
excludes metering, key-purpose DDL, shared migration files and all
public schema/generator changes. The patch applies independently to
the baseline; the prior actual handler and PostgreSQL execution used
the retained gateway stack, so the final assembled publication still
requires its own acceptance.

Enable PublishAliasCapabilities only in the verified full-platform
PostgreSQL composition after DEV publishes the four read contracts
and generated clients. Earlier cumulative database catalogues may not
have the target-history table; keep the switch off there. This is a
trusted server composition decision, not a deployment configuration
selector. The removed application_profile setting remains invalid.
Non-PostgreSQL projections remain absent. The switch does not change authorization,
and customer input never selects it. A missing evidence relation on
an enabled composition is a storage failure, not a supported legacy
snapshot. A legacy row in an existing evidence schema may omit the
object when it lacks an exact snapshot. Preserve that distinction
when wiring startup and qualifying the installed consumer.


The retained gateway source at 6f31064f was requalified against committed
schema 58 from 106ca52d. All four focused capability tests passed over
the real PostgreSQL and Admin/Portal TLS owner, alongside the native
receipt, historical request and CA regressions. The exact source and
logs are retained in
/root/lane-gw-acceptance/schema58-gateway-regression.zxq09kh2. This
remains an explicitly enabled candidate read test. Production publication
and earlier cumulative database catalogues retain the disabled switch.

### Retained source and read-contract alignment

The retained registered-read source originated at 5fb92e1d. DEV's
candidate 7705c5a341458cb1be8ead80844198378d98c4e3 binds only
adminListModelAlias, adminGetModelAlias, portalListModelAlias and
portalGetModelAlias to the new read representation. A static review
against gateway candidate 9fceebc9817e767a3cc31991eb004f4c59b81d1e
confirmed that its five source and four test files retain the exact
minimal handoff bytes, apart from the independent metering composition
flag in model.go. The contract bytes have SHA-256
66449b506190e37330558a77721a35c541827b5bec4e8a421f0cc452df47e69d.

ModelAliasRead retains every existing ModelAlias field and adds an
optional, non-null, closed capabilities object. If present, it requires
both text_generation_verified=true and context_token_ceiling, an integer
from 1 through 9007199254740991. Lists and details use this same read
component. Mutation components and operations, machine contracts and the
compatible model list are unchanged. Recursive inspection of successful
response schemas confirmed that only the four human read operations
reach the new read component.

DEV's retained qualification exercised the actual registered handlers
with its candidate schema, disconnected PostgreSQL and synthetic
deployment evidence, and checked the generated Go and Python response
decoders. The older SDK decoders retain their known fields, but the
older closed JSON schema rejects the new field. Publish the matching
schema, generated clients and Help before enabling the server
composition. That candidate execution does not establish production
activation or an installed UI flow.

The static review and source identities are retained in
/root/lane-gw-acceptance/gw104-retained-pair.ht2cqeo_/alignment.json.
No test or build was executed for this byte comparison. Do not rerun
unchanged predecessor receipt or CA fixtures to claim combined
acceptance. Qualify the exact published composition and contract after
the delivery and formal-migration publication order permits it; until
then PublishAliasCapabilities stays false and GW-104 remains open.


## Producer and typed startup integration

The minimal read source originated at 5fb92e1d; the retained nine-file
patch applies unchanged to schema-59 main 402d604e. Keep that source
separate from metering projection, key-purpose DDL and other runtime
candidates. DEV's exact draft remains 7705c5a3, distribution OpenAPI
0.12.0. The published 0.11.0 description flow remains independently
usable.

### Production evidence producer

The agent's inference verifier in
services/agent/internal/inference/verification.go runs Health and
Generate against the owned loopback serving engine. Generate validates
the model, nonempty assistant text and bounded positive usage, then
produces wire.GenerationVerification with prompt/response hashes,
input/output counts and observed time. This is an existing production
producer; GW does not manufacture those values or replace it with a
model-family inference.

deployments.Service.ReadyGatewayTargetWithin consumes the stored
deployment observation on the caller's transaction. Its owner validates
the exact deployment/model/GPU/revision, managed running state, fresh
node/deployment sightings, held GPU lease, the verification envelope,
allowed execution profile, fencing and ready model catalogue. The
deployment lookup locks the deployment and lease rows. Derived targets
also require their successful task and validated derived catalogue.
A target that lacks this evidence is unavailable.

distribution.Service.Publish and Cutover call recordAliasTarget in the
same scoped transaction as the alias change. The immutable
model_alias_target_versions row records the alias route revision,
deployment ID/revision, model ID/kind, catalogue digest and nullable
recipe digest. Description edits increment description_revision only;
they preserve this routing revision and its target snapshot.

The read path is ReadAlias / ReadAliasPage, Service.transaction,
publicAliasAvailability / aliasReadiness, ReadyGatewayTargetWithin,
aliasCapabilitiesWithin and aliasContextCeiling. The selector matches
the current alias and deployment tuple, node, model, catalogue and
nullable recipe across local-cell scoped rows in that transaction.
For an approved source it returns verified generation and the minimum
of pinned model context, serving recipe context and current gateway
policy. These fields describe evidence and a ceiling; enabled protocol
adapters remain a separate authority.

### Actual composition boundary

The current identity HTTP factory is:

```go
func New(s *identity.Service) *Handler
```

It constructs the shared owner services and retains this concrete
distribution instance:

```go
distributionService := &distribution.Service{
    DB: s.DB, Identity: s, Workspaces: w, Nodes: n, Deployments: d,
}
```

The Handler stores it as
Distribution: &distributionhttp.Handler{Service: distributionService}.
The candidate's composition field is
distribution.Service.PublishAliasCapabilities. Its zero value is false;
ReadAlias and ReadAliasPage omit the object in that mode. Set a trusted
startup decision before serving begins. It is not a user policy,
runtime configuration selector or authorization switch. Keep using
the same shared service instance and owner pointers.

ARC owns changes to that identity factory, typed startup and root.
Its enablement decision requires the normal full PostgreSQL catalogue
and restricted runtime admission to pass, including target history and
the existing producer tables. Earlier cumulative catalogues and other
database projections retain the omitted-object behavior. Schema
version alone does not establish those dependencies. The removed
application_profile selector remains invalid.

ARC's prepared wiring must retain the released 0.11.0 response shape
until the matching read contract is published and built. Qualifying
an explicitly enabled candidate in a controlled fixture can precede
publication; production enablement requires the matching compiled
contract and client/Help publication. Keep the service flag immutable
once the handler serves concurrent requests.

An empty installation remains ready: startup validates the schema,
runtime principal and constructed owner dependencies. Verified target
availability is decided per authorized alias read. It does not require
a populated catalogue or manufacture a ready deployment at startup.

### Read contract and refusal matrix

AliasReadView embeds Alias, including schema 59's paired description
fields. Its only extra JSON field is the optional non-null capabilities
object. Both text_generation_verified=true and a positive integer
context_token_ceiling are required together; no other capability keys
are emitted. DEV caps the wire integer at 9007199254740991.
Mutation Alias, machine bindings and the compatible model list keep
their existing types.

| Input/evidence state | Human read result |
| --- | --- |
| Exact visible active verified target and recognized catalogue/recipe | Both capability fields, attached to that alias/deployment revision |
| Callable legacy alias without an exact target snapshot | Existing alias with no capabilities object |
| Mismatched alias/deployment/model/catalogue/recipe or unknown source | No capabilities object |
| Unavailable target or inactive workspace under otherwise valid read visibility | No capabilities object |
| Paused alias in Admin | Existing paused alias with no capabilities object |
| Paused alias in Portal | Existing hidden list item / not-found detail |
| Wrong audience, tenant, workspace or cell | Existing authorization or not-found problem; no metadata disclosure |
| Invalid ID or cursor | Existing validation/cursor problem |
| Evidence query fails in an enabled composition | HTTP 503 STORAGE_UNAVAILABLE |
| Evidence query unavailable with publication disabled | Released read shape without querying optional evidence |

A description edit or clear must preserve the verified capability
tuple and routing revision while the read's description fields advance.
A replay may report the current description revision and an older
applied revision without changing capability evidence. Cover this
interaction through the registered HTTPS handler on normal schema 59,
beside the existing mixed-page, cursor, scope, stale-target, pause,
retarget/resume, mutation-contract and storage-failure checks. Synthetic
deployment observations qualify persistence and composition behavior;
they do not establish actual model/GPU execution.

### Owner sequence and exact acceptance inputs

1. GW lands the minimal original backend/read composition, default off,
   without metering, key DDL or public contract changes.
2. ARC prepares typed startup/root wiring at the real factory after
   validating the full PostgreSQL producer/read dependencies. Preserve
   the released response shape until contract publication permits it.
3. GW runs the actual registered Admin/Portal owner and producer/read
   cases on that exact assembled SHA, with explicit candidate activation
   and DEV's exact draft validation. Record native platform, schema,
   selected test roots and synthetic-versus-model evidence separately.
4. DEV publishes the matching four operation bindings, schema, Go/Python
   SDKs, Web types and Help from the same assembled source. ARC's startup
   condition may enable production only with that matching contract.
   WEB owns the catalogue display and its installed UI acceptance.

The four operations are adminListModelAlias, adminGetModelAlias,
portalListModelAlias and portalGetModelAlias. Existing strict closed
0.11.0 validators need the updated read schema before exposure. Scope
and error behavior remain identical in the generated operation bindings.
Do not mix this sequence with the separate already-published GW-120
description implementation or relabel old spool/CA evidence as a new
composition run. No new dependency, model or dataset is introduced.

### Native Mac fixture admission

The isolated read candidate at 219af27e3d9f73c78acc1b8ab8324e086a6a2f62
compiled with native Go 1.27.3 on darwin/arm64. The single admitted
permanent-file flock run rechecked every archived source file, disk and
memory admission, the cached linux/arm64 PostgreSQL image, and an active
sandbox denial of non-loopback outbound connections. The test-only
overlay selected DEV 7705c5a3's exact distribution 0.12.0 draft; it did
not change product code or enable the production composition.

The four PostgreSQL roots did not run. PostgreSQL became ready and
vector 0.8.7 was created, but Docker Desktop 4.94.0 / Engine 29.8.2
returned an empty port-binding list for the internal network. The
fixture stopped before launching the test binary. Therefore schema 60
and its 120-table catalogue describe the source's expected migration,
not a migrated database from this attempt. Native compilation is not
producer/read protocol acceptance or a full repository gate.

The harness incorrectly assumed that an internal Docker network could
publish a host loopback port. Docker's [internal network reference](https://docs.docker.com/reference/cli/docker/network/create/#network-internal-mode---internal)
describes direct host-to-container access; Docker Desktop places that
host inside its Linux VM. The [upstream port-publication issue](https://github.com/moby/moby/issues/36174) reports the same failure mode. A replacement transport must first prove real PostgreSQL
wire access and outbound isolation, then rerun all four roots without
skips. Do not remove isolation to obtain a passing fixture.

The original entry, source manifest, empty successful compilation log
and failure record are retained in
/root/lane-gw-acceptance/gw104-mac-native-failure.okfmTgXv.
The entry removed its own container, internal network and credential
files before the parent closed its shared lock descriptor. Post-run queries
confirmed that the owned container and network were absent; no host
port was assigned. That failed entry was not retried. The separately
authorized stdio attempt below retains its own inputs and scope. Earlier
native Linux evidence retains its original source and platform scope.

#### Replacement transport and qualification

Keep PostgreSQL on Docker's `none` network. An owned native loopback
listener forwards bytes through non-TTY `docker exec -i` to a small Go
standard-library helper, which dials PostgreSQL's container loopback.
The [Docker exec contract](https://docs.docker.com/reference/cli/docker/container/exec/)
keeps stdin attached without allocating a terminal. No host port is
published by Docker and no container external route is introduced.
Select and pin the local Docker Desktop context before creating any
resource; reject a remote daemon and remove inherited test-database
selectors.

The helper changes the database peer to container loopback.
[initdb authentication options](https://www.postgresql.org/docs/18/app-initdb.html)
default to local trust, so the fresh fixture explicitly sets
`POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256`. The
[image entrypoint](https://github.com/docker-library/postgres/blob/master/docker-entrypoint.sh)
passes those arguments to initdb. Native pgx must receive `28P01` for a
wrong password before the private credential completes the SQL preflight.
A passwordless loopback session cannot qualify this replacement.

A [PostgreSQL SSLRequest](https://www.postgresql.org/docs/18/protocol-message-formats.html#PROTOCOL-MESSAGE-FORMATS-SSLREQUEST)
checks the byte transport first. Native pgx then authenticates through
the same loopback listener and executes a read-only query, under the
original sandbox. Only after that proof may the unchanged four roots
run with the exact DEV draft overlay. Reuse the recorded native test
binary only after verifying its hash and the frozen source/overlay
inputs. The fixture has bounded connection children and must stop them,
remove its owned PostgreSQL container and credential files, and verify
listener closure before releasing the shared descriptor.

A normal externally routed bridge would weaken the offline proof, and
a simulated database would not qualify the PostgreSQL protocol. Reject
both. Reuse the pinned pgx driver instead of implementing authentication
or SQL framing. The existing [Go standard library](https://go.dev/LICENSE)
uses BSD-3-Clause; [pgx 5.11.0](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE)
uses MIT; [PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector 0.8.7](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE)
use the PostgreSQL License. These allow commercial redistribution with
the required notices and offline use. No product dependency, model or
dataset is added. This is a fixture prototype; its first execution
boundary is below, and the four business roots remain unqualified.

#### Native stdio preflight boundary

The single follow-up admission used parent 89817 and child group 91251.
It waited 209.066 seconds on the permanent carrier's FD 3, device
16777234 / inode 50313339. The lock-covered execution took 3.856 seconds
and exited 1. Exact 219af27e sources, DEV 7705c5a3's 0.12.0 overlay,
resources, local Docker Desktop, cached linux/arm64 image and active
non-loopback outbound refusal passed. The recorded darwin/arm64 product
test binary was reused; both small transport/preflight helpers compiled
from cached dependencies. PostgreSQL used network none, explicit SCRAM
host authentication and native loopback proxy port 54800.

The SSLRequest probe passed. The native pgx checker reached its SQL
check after its wrong-password 28P01 and successful-connection guards,
then returned PG_WIRE_QUERY_REFUSED. It emitted no observed SQL values,
so this output does not distinguish SQL/scan failure from result
mismatch. The four selected Go roots were invoked zero times, and none
of their migrations or 120-table catalogues ran. This is a temporary
fixture refusal, not evidence of a production protocol failure.

Source review found a definite fixture assertion defect:
inet_server_addr()::text includes its netmask, while the check required
a bare IP. PostgreSQL's [network address functions](https://www.postgresql.org/docs/18/functions-net.html)
specify that host(inet) returns the address alone and an explicit text
cast retains the mask. The prepared correction selects both forms,
compares host(inet_server_addr()), and records safe observed values or
SQLSTATE/error type before refusing. The old runtime output is
insufficient to prove this was its only failing condition. A new actual
wire run must establish that; no second queue or runtime retry was made.

The entry removed its owned proxy, PostgreSQL container and credential
files before the parent closed FD 3. The owned child group had exited,
the carrier identity was unchanged, and a post-exit probe of port 54800
returned connection refused. Gate proof SHA-256 is
ae9721c781a79a23614c189efb0de5496b007bbf118b78d4b1770115fce2d234.
The input manifest remains
d76d9afff7b0f531ad2aa36f6ae27f70f4753c498c5ed471cedd038bacf523f1.
The failed attempt stays at
/private/tmp/ownsolid-gw104-native-stdio.3i3mw2er; the source-only
correction is prepared separately at
/private/tmp/ownsolid-gw104-native-wire-fix.uxm7uway.
Production publication remains disabled, and complete repository gates,
matching contract/client publication and installed UI acceptance remain
separate unmet requirements.
