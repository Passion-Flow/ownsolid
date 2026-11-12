# Offline operator CLI (DEV-102)

## Operator workflow

Operators need the Admin entrance over SSH in installations where only the private
control plane is reachable. [kubectl get][kubectl] uses a table by default and
offers JSON and YAML from the same request. [OpenShift oc][oc] demonstrates a
login command that establishes a session for subsequent commands. ownsolid will
ship that pattern as a local Go binary, with examples in the root, group and leaf
help and with a version command that works without configuration.

Use the existing Admin contracts for [identity][identity], [nodes][nodes],
[models][models] and [deployments][deployments]. Login and logout obtain a fresh
CSRF token and its context cookie before the POST, and send the configured origin.
Persist only the Admin session cookie value. The server owns expiry, authorization,
tenant isolation and audit. Use an in-memory cookie jar for each invocation. Do
not follow redirects or use environment proxies. A configured PEM file supplies
an explicit trust pool for Go's TLS verification. Linux can use local system
roots by default. Require the PEM on macOS for HTTPS because [Go delegates
system trust on macOS][darwin-trust] to a verifier that [can fetch missing
certificates][apple-trust]. An explicitly configured
HTTP origin uses the server's separate plaintext cookie namespace. Normalize the
origin using the scheme, host and port [origin rules][origin].

## Scope and output

- List nodes through all server cursor pages. Find a node by exact name in the
  results of the documented name-prefix filter, or use a UUID. Reject ambiguous
  names and fetch the actual detail endpoint after resolving the ID.
- List models and deployments in a configured workspace, or the current
  workspace returned by `GET /api/v1/me`. Their APIs return server-bounded lists
  without cursors. Deployment records have UUIDs and model IDs, with no independent
  name field. A deployment UUID directly selects its record. A name means an
  exact model name and selects the deployment only if there is exactly one
  matching model and one retained deployment for it in the selected workspace.
  Read the server's existing public runtime lookup limit. If a full page could
  conceal a duplicate, or the selection is ambiguous, direct the operator to a
  deployment UUID.
- Preserve the API resource fields in JSON and YAML and use selected columns in
  tables. Measure table columns using the Go Unicode width data so East Asian
  wide characters occupy two columns. Escape control characters in human output.
  Write actionable failures
  and prompts to stderr so automation can parse stdout.
- Use `--server`, then `OWNSOLID_SERVER`, then the local JSON config. Follow the
  [XDG base directory specification][xdg], with `OWNSOLID_CONFIG_DIR` as an explicit
  override. Use an origin-specific credential file in a private directory. Open
  credentials without following final symlinks, require owner-only mode 0600,
  atomically replace the file, and serialize login and logout for that origin.
  Preserve the credential on a retryable logout failure. Read passwords from a
  masked terminal prompt or an explicitly supplied private file.

## Dependency review

| Runtime dependency | Pinned version | License |
| --- | --- | --- |
| [Go standard library][go] | Repository Go toolchain | BSD-3-Clause |
| [Cobra][cobra-license] | 1.10.2 | Apache-2.0 |
| [pflag][pflag] (Cobra parser) | 1.0.9 | BSD-3-Clause |
| [go-yaml v3][yaml] | 3.0.5 | Apache-2.0 and MIT for the libyaml-derived files |
| [x/term][term] | 0.47.0 | BSD-3-Clause |
| [x/sys][sys] | 0.49.0 | BSD-3-Clause |
| [x/text width][width] | 0.43.0 | BSD-3-Clause, with Unicode width data under the [Unicode data license][unicode-license] |

These licenses permit commercial use, redistribution and offline execution.
Distribute their license texts and applicable notices with the binary. No model
or dataset is needed. Cobra's [command implementation][cobra-command] and
[module manifest][cobra-mod] separate argument parsing, completion and help from
application execution. Its execution path runs callbacks provided by the binary
and does not perform an update check, download or telemetry request. Its
Windows-only `mousetrap` module is Apache-2.0 and is outside the Linux and macOS
builds. Cobra's documentation tooling is not imported. YAML conversion, local
terminal I/O and file operations are in-process. The CLI sends HTTP requests
only to the selected Admin origin and requires no external runtime service.

The standard `flag` package would require maintaining subcommand dispatch and
persistent flags ourselves. A YAML serializer avoids hand-rolled quoting and
implicit scalar parsing. A server API token or a client-generated session would
introduce a second authentication flow and change authorization semantics.
Shelling out to curl and saving a complete cookie jar would add a runtime
dependency and persist the CSRF context unnecessarily.

## Verification criteria

- Exercise all eight commands. Check root and leaf help on the compiled binary.
- Cover table alignment, JSON and YAML, empty results, cursor iteration and
  exact or ambiguous resource selection against HTTP fixtures.
- Check flag, environment and file precedence and ensure connection errors
  report the configured origin and a recovery action.
- Assert mode 0600 and origin isolation, refusal of insecure files, CSRF and
  cookie handling, redirect refusal, permission failures and logout retry.
- Run the CLI flow on a network with only the test Admin service reachable.
  Verify repository build, checks and tests before marking the item complete.

[kubectl]: https://kubernetes.io/docs/reference/kubectl/generated/kubectl_get/
[oc]: https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/cli_tools/openshift-cli-oc
[identity]: ../../api/openapi/v1/identity/openapi.json
[nodes]: ../../api/openapi/v1/nodes/openapi.json
[models]: ../../api/openapi/v1/models/openapi.json
[deployments]: ../../api/openapi/v1/deployments/openapi.json
[origin]: https://www.rfc-editor.org/rfc/rfc6454
[xdg]: https://specifications.freedesktop.org/basedir/latest/
[go]: https://go.dev/LICENSE
[cobra-license]: https://github.com/spf13/cobra/blob/v1.10.2/LICENSE.txt
[cobra-command]: https://github.com/spf13/cobra/blob/v1.10.2/command.go
[cobra-mod]: https://github.com/spf13/cobra/blob/v1.10.2/go.mod
[pflag]: https://github.com/spf13/pflag/blob/v1.0.9/LICENSE
[yaml]: https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE
[term]: https://go.googlesource.com/term/+/refs/tags/v0.47.0/LICENSE
[sys]: https://go.googlesource.com/sys/+/refs/tags/v0.49.0/LICENSE
[width]: https://pkg.go.dev/golang.org/x/text/width
[unicode-license]: https://www.unicode.org/license.txt
[darwin-trust]: https://go.dev/src/crypto/x509/root_darwin.go
[apple-trust]: https://developer.apple.com/documentation/security/sectrustsetnetworkfetchallowed(_:_:)

## Integration evidence

`TestLaneDRealCLI` ran the compiled Linux arm64 CLI as separate processes against
the real Admin HTTP dispatcher over verified TLS and PostgreSQL 18 with pgvector
0.8.7. The disposable Docker container used `--network none` and had no network
route beyond loopback. The fixture enrolled a synthetic node, inserted approved
model metadata and created a deployment through the service. It verified both
help pages, local version, login, both node operations, model list, both
deployment operations by name and UUID, and logout. A read after logout failed
and PostgreSQL held the logout audit event. The test covers the control-plane
workflow with synthetic inventory and deployment records.

## Common Admin operations

Follow the actual Nodes and Model Services flows in the Console. The first
terminal workflow is to find a node, disable it for maintenance, enable it
again, inspect where an approved model can run, submit a deployment, check
its state, and stop it. The same screens expose rename, removal, fact
collection and task detail for nodes, and start, observe, verify and removal
for deployments. Pending node invitations can be issued, listed and revoked.
Task logs use the [node contract][nodes] cursor. Return one bounded page and
its cursor so an operator or script can continue from the last line.
The Models screen also reads the approved manifest and retires unreferenced
versions. Model import, gateway publication and node monitoring components
each have their own workflows and are tracked separately in the lane decisions.

Kubernetes provides distinct [cordon and uncordon commands][kube-node] and
[version preconditions][kube-scale] for deployment changes. The [EC2
idempotency guide][ec2-retry] shows why an uncertain response must be retried
with the original key and payload. Use the existing Admin node PATCH version
and deployment action revision, CSRF and per-origin session, and the
server's idempotency key on task and deployment writes. Do not infer that a
write succeeded from a read, or retry a version conflict using a newer
version without operator review. Keep site, cell, and workspace checks on
the same paths as the read commands.

Use the placement endpoint to discover GPUs and require explicit model, node
and GPU identifiers on creation so a retry retains the same placement.
Keep desired and observed state in the output, because HTTP 202 records
acceptance and the agent may not have acted yet. Require explicit
confirmation for removal and stopping a deployment. An issued enrollment
credential belongs in a private file and must never appear in normal
command output. Since issuance has no idempotency key, inspect and revoke
pending invitations after an ambiguous failure before issuing another.
These additions use existing dependencies and Go standard-library code.

Exercise both the server response and subsequent reads for mutations. Verify
that a caller without permission cannot change data, a stale version cannot
overwrite a newer update, and replay after a lost response retains the
original deployment and task. Run the CLI as separate processes against the
real Admin TLS dispatcher and PostgreSQL with only loopback networking.

`tools/cli/testdata/run-admin-tls.sh` exercised the node, model and deployment
commands as Linux processes with verified Admin TLS and PostgreSQL 18. The
fixture checked persisted state, removal of an agent credential, audit rows,
permission and version refusals, and workspace mismatch. It forced the reply
to both creation and stop to fail after the real handler accepted the
operation, then replayed each original key and checked that only one task
existed. Synthetic agent reports drove the observed deployment states. The
run verifies the control path and does not qualify physical inference.
The pinned fixture uses [PostgreSQL][postgres-license] and
[pgvector 0.8.7][pgvector-license]. Both use the PostgreSQL License, which
allows commercial use and offline redistribution with its notices.
Neither is included in the standalone CLI.

[kube-node]: https://kubernetes.io/docs/reference/kubectl/
[kube-scale]: https://kubernetes.io/docs/reference/generated/kubectl/kubectl-commands#scale
[ec2-retry]: https://docs.aws.amazon.com/ec2/latest/devguide/ec2-api-idempotency.html
[postgres-license]: https://www.postgresql.org/about/licence/
[pgvector-license]: https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE

## Resumable model import

Use the same [Admin import contract](../../api/openapi/v1/models/openapi.json)
and store exercised by MOD-119 and DEL-117. The [tus specification][tus]
uses a server-owned offset and rejects writes beyond it. ownsolid's
equivalent is the import's `uploaded_bytes` and a SHA-256 per chunk.
Read the installation's catalog and limits, acknowledge its license, and
create the intent with the caller's idempotency key. Reopen the same local
bundle to resume and replay the final recorded chunk before appending. The
CLI reads only one bounded chunk at a time. The server validates the final
archive against every approved path, size and digest before publishing it.

Reuse the Admin CSRF token and context cookie for a transfer, refreshing
them on an explicit CSRF refusal. A lost chunk response stops the process
with the import ID and resume command. The next process reads the durable
offset and replays safely. This avoids a local transfer journal, trusting a
client offset, parallel writes to a single import, or a second archive
verifier that could disagree with the server. Verification, cancellation and
status use the existing import lifecycle endpoints.

The real fixture uses a lane-owned archive of the cached, catalog-checked
[Qwen3-0.6B revision][qwen-license]. Its Apache-2.0 license permits commercial
use and offline redistribution with the required notices. Preserve its
license and model card in the archive. All CLI additions use existing Go
dependencies. Test a lost response after a durable chunk, an alternate file
of the same size, an out-of-scope request, exact-byte continuation, and the
real verifier publishing a ready model with its approved manifest.

[tus]: https://tus.io/protocols/resumable-upload
[qwen-license]: https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE

## Node exporter maintenance

Expose the [component contract](../../api/openapi/v1/components/openapi.json)
for the single managed node exporter. Read desired and observed state
separately. Submit the server's typed action, optional catalog version and
bounded configuration using the existing task idempotency key. Inspect
the returned task for completion and use the separate journal snapshot
endpoint for collected logs. Never issue SSH or systemd commands from the
CLI or transfer arbitrary executable files. The agent and its execution
profile own host changes, and control checks pinned assets before leasing.

The approved Linux amd64 archives for [1.12.0][exporter-120] and
[1.12.1][exporter-121] are Apache-2.0, permitting commercial use and offline
redistribution with the required notices. Prepare both in the fixture's
catalog directory and check their size and SHA-256 against the repository.
The CLI consumes the Admin API using its existing Go dependencies.
Test install and upgrade with verified assets, configure, rollback, task
replay, refusal on an ineligible profile and a journal snapshot through
the existing log store. Distinguish synthetic agent results from systemd
execution on a node.

[exporter-120]: https://github.com/prometheus/node_exporter/blob/v1.12.0/LICENSE
[exporter-121]: https://github.com/prometheus/node_exporter/blob/v1.12.1/LICENSE

## Gateway alias publication

Use the [Admin distribution contract](../../api/openapi/v1/distribution/openapi.json)
to list existing gateway instances and list, inspect, publish or disable
their aliases. A publish pins the selected workspace, gateway, deployment
revision and idempotency key. Disabling requires the current alias revision
and explicit confirmation. Let control decide whether the gateway and target
are ready. The existing [gateway delivery plan](gateway-delivery.md) gives
agent-managed startup and credentials to GW-114. Keep that separate from
publication, and keep caller credential handoff outside this CLI slice.

The CLI uses existing dependencies. Verify the mutation against the real
Admin handler and PostgreSQL with a provisioned gateway record and a
synthetic deployment observation. Test a lost publish response, exact
replay, cross-workspace and stale-revision refusal, and the persisted disable.
This verifies the publication record. A live route requires an installed
gateway process and model backend.

## Priority 2 integration evidence

The disconnected Admin fixture uploaded the 1,519,216,640-byte archive
assembled from nine catalog-verified Qwen3-0.6B files. A response lost after
the first committed chunk was followed by a same-size wrong-file refusal,
then a successful continuation and publication by the real verifier. A
foreign file in a lane-owned verification stage produced
`MODEL_STAGE_INVALID`. The CLI requeued verification and the next attempt
published from the same upload. A separate cancelled import had its stored
bytes removed. The fixture also checked refusal after the session lost
administrator eligibility.

The same runner leased and checked both pinned node exporter archives and
settled install, configure, restart, upgrade, rollback, stop, start,
observe, log collection and removal tasks using synthetic agent reports.
It read the settled journal snapshot from real [VictoriaLogs 1.53.0][vlogs],
an Apache-2.0 test dependency. The gateway fixture tested the persisted
publish, retry and disable against a provisioned gateway record. Agent
systemd execution and gateway process delivery have their own qualification.

[vlogs]: https://github.com/VictoriaMetrics/VictoriaLogs/blob/v1.53.0/LICENSE

## Agent-managed gateway registration

GW-114 added the agent-managed branch to the existing
[Admin distribution contract](../../api/openapi/v1/distribution/openapi.json).
Register a node's managed gateway with the same node UUID, name, audit reason
and idempotency key used in the console. Omit the origin to let control check
eligibility and let the node declare it. Accept the contract's HTTP 201
creation and HTTP 200 replay responses. Render the workspace-scoped gateway
record with its delivery state and error so an operator can inspect progress
before publishing the alias. The registration and publication commands stay
separate because startup settles asynchronously and has its own retry path.

Do not infer an origin, issue a legacy placement credential, or chain alias
publication into a request that only queues a gateway task. The local
command uses the existing session, CSRF and mutation machinery. It adds no
dependency, model or dataset. The current CLI dependencies and offline use
are covered by the licence review above.

Exercise the command on the real Admin TLS handler with PostgreSQL and an
eligible enrolled fixture node. Verify that creation queues one task, a
lost response is retried with the same key, replay keeps the same gateway,
foreign workspaces are refused, and the response carries no credential.
The node execution and TLS gateway qualification are recorded by GW-114.

The Linux amd64 Admin TLS fixture passed with real PostgreSQL in a Docker
network namespace with no external route. It observed the committed 201
creation before losing the response, replayed the same key over Admin TLS,
read the queued gateway, and found one gateway task and one audit event.
The fixture refused a different workspace and verified the CLI output
contained neither credential field. Model import and exporter fixtures
were skipped in this run because their optional assets were not supplied.

## Training attempt and metric reads (DEV-102, TRN-106)

[Kubeflow Trainer's progress and metrics guide](https://trainer.kubeflow.org/en/latest/user-guides/trainjob-progress.html)
exposes training progress through the admitted job. Ownsolid already
retains optimizer steps and event sequence by attempt. The existing
Admin training routes read attempt history and the metrics for one
selected attempt with the operator's scoped workspace and cell. Use
the published API to show attempt IDs and page metrics in sequence.
Keep cursors opaque and bind each continuation to the same job, attempt
and workspace. Attempts use the standard 512-byte cursor limit. The
metrics endpoint accepts up to 2,048 bytes. This uses the existing
Cobra, Go and SDK-reviewed dependencies and synthetic fixture inputs.
Their licences and offline redistribution terms are listed above.

Reading local trainer logs would bypass scoped access and merge metrics
across retries. A new metrics stream would duplicate the stored event
owner. Add one-page CLI reads for both resources and use the existing
table, JSON and YAML output. Verify the empty queued history, an admitted
attempt and two metric pages on a disconnected HTTPS/PostgreSQL fixture.
Check a cursor sent to the wrong collection or attempt, foreign workspace,
unknown attempt, and successful repeat reads. The fixture supplies
synthetic agent events through the existing training event admission.

The Linux amd64 verification used the compiled CLI against the real Admin
HTTPS dispatcher and loopback-only PostgreSQL 18.6. It confirmed an empty
queued history, a real dispatched attempt with synthetic events, stable
pagination on retry, and a measured zero in the last metric page. The
CLI preserved the table continuation. It refused cross-workspace reads,
an unknown attempt, a missing attempt flag, and cursors reused across
attempts and collections. The independent Admin and Portal HTTPS fixture
passed training intent, history, metric and recovery checks. The fixture
did not execute a GPU worker.

## Training checkpoint and recovery history (DEV-102, TRN-104)

[PyTorch Elastic](https://docs.pytorch.org/docs/main/elastic/train_script.html)
describes progress restored from a checkpoint after restart.
[Kubeflow Trainer](https://trainer.kubeflow.org/en/latest/user-guides/trainjob-lifecycle.html)
keeps the job lifecycle separate from the workers that execute it.
Ownsolid retains the accepted checkpoint's producing attempt, optimizer
step and receipt digest. Recovery history links the preceding attempt
to a new one when dispatch occurs. A queued or withdrawn recovery can
have no next attempt. A restart has no checkpoint reference.

Use the two registered Admin GET operations in the
[training API](../../api/openapi/v1/training/openapi.json). Keep the
existing TLS session and workspace selection, with a single page of
opaque continuation bounded to 512 bytes and at most 100 rows. Display
the checkpoint identifiers and the recovery links in the existing
table, JSON and YAML formats. Follow [AIP-158](https://google.aip.dev/158#opacity)
by replaying the cursor on the same collection, job and workspace. The
server rechecks access on each read. Local files cannot establish
accepted checkpoint identity. A separate attempt filter or recovery
mutation would expand the existing read contract.

The current Cobra (Apache-2.0), Go and x/sys (BSD-3-Clause), and
[SDK fixture licences](sdk.md#licenses) allow commercial and offline use
and redistribution under their notice terms. The fixture adds no
dependency, model or dataset.

In a disconnected PostgreSQL and Admin HTTPS run, read both empty
collections and page retained checkpoints and recovery events. Verify
the exact job and attempt IDs, the completed and pending recovery links,
stable retry and final page boundaries. Use actual recovery admission
and dispatch with synthetic node and stop evidence. Use clearly labelled
synthetic checkpoint metadata to isolate the read path. Exercise a
different job, workspace and collection, and a revoked Admin session.
The fixture qualifies the CLI metadata path. Checkpoint file sealing and
real GPU resume have separate acceptance.

The Linux amd64 compiled CLI passed the Admin HTTPS fixture on
loopback-only PostgreSQL 18.6 with pgvector 0.8.7. It preserved the
checkpoint digest, optimizer step and producing attempt on both pages.
Recovery history retained the dispatched predecessor and successor and
the still-pending intent without a successor. Empty and final pages,
retry, help and table continuation passed. Both commands refused
cross-job, cross-collection and cross-workspace cursors. Role removal
and session revocation refused a saved cursor without history data.
A fresh authorized session could use that cursor again. The separate
Admin and Portal HTTPS training fixture also passed.

## Fleet history reads (DEV-102, OBS-109)

[Kubectl get](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_get/)
uses a readable table by default and structured JSON or YAML for scripts.
[VictoriaMetrics range queries](https://docs.victoriametrics.com/victoriametrics/url-examples/#apiv1query_range)
evaluate a selected interval with a step. Ownsolid's public
[Admin history operation](../../api/openapi/v1/monitoring/openapi.json)
already owns authorization, tier selection, the evaluation grid and audit.
Expose that operation as `ownsolid monitoring history` with explicit
`--from` and `--to` canonical UTC seconds. Reuse the CLI's workspace
selection, local CA and origin-bound session. Do not accept a store address,
selector, step or tier override: those choices belong to the server.

Keep the returned evaluation interval, tier and step in structured output.
Flatten only the table presentation into site, cell, timestamp, observed
and reachable columns. Cells can span the authorized workspace; the URL's
cell selects the serving control plane and is not a client-side data filter.
Preserve all uint64 counts without floating-point conversion. Sparse
intervals stay absent, measured zero remains zero, and an empty collection
stays a successful result. Reject malformed time arguments before I/O.
Do not invent default relative windows: the server's settling and retention
policy can refuse them and a retry should keep its original interval.

The existing [CLI dependency review](#dependency-review) and
[SDK licences](sdk.md#licenses) apply. Cobra is Apache-2.0; Go and x/sys
are BSD-3-Clause. No dependency, model or dataset is added. These licences
permit commercial use and offline redistribution with their notices.

Tests must verify canonical arguments, exact selected workspace and
origin-bound cookie, JSON/YAML/table output, maximum unsigned counts,
sparse zero and empty results. Server Problems must produce nonzero exit
with no successful output and remain retryable. Run the compiled CLI
against the real Admin TLS/CSRF/login/cookie dispatcher, PostgreSQL and
native raw and rollup stores without an external route. Verify foreign
workspace and revoked session refusal, request-bound audits, long-tier
failure without raw fallback and retry. These checks qualify the CLI read
path; installed retention delivery and the Console history view remain
with their owners.

The Linux amd64 run passed with the compiled CLI under the race detector,
loopback-only networking, PostgreSQL 18.6 and native raw and rollup stores.
It preserved sparse zero, the selected tier and evaluation grid, an empty
YAML collection, and table and JSON reads. A foreign tenant's workspace
and a restored revoked session were refused without a successful audit.
A failed long-tier read made no raw-store request; retry and a fresh
login recovered. Each successful history request had exactly one
request-bound audit. The CLI unit suite additionally preserved maximum
uint64 counts in JSON, YAML and table output and rejected malformed
intervals before I/O. All fixture stores and PostgreSQL were stopped.


## Workspace discovery (DEV-102)

[Kubectl's context inspection](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_config/kubectl_config_get-contexts/)
makes the available operating scopes visible before an operator selects one.
Ownsolid's [published workspace contract](../../api/openapi/v1/workspaces/openapi.json)
and [registered handler](../../services/control/internal/modules/workspaces/http/handler.go)
already provide platform-admin list and detail reads. The CLI currently asks
for a workspace UUID for deployment and training operations without a command
to discover it. Expose these existing reads as workspace list and
workspace get with an exact workspace UUID.

Use the authenticated Admin origin and its saved session. These global
workspace URLs do not use the configured site, cell or current workspace.
The server resolves the installed tenant and current platform-admin authority.
Accept no tenant selector and do not change the session or local configuration.
A local cached scope list would outlive permission changes; a new endpoint or
a name-based implicit selection would duplicate server behavior and risk
selecting an ambiguous workspace. Require an exact canonical nonzero UUID
for detail and check that the returned identity matches it.

Read one bounded page using the existing cursor helper. Expose --query as
the server's case-insensitive literal name substring, preserving spaces and
wildcard characters. Validate its one-to-100 Unicode character bound and
control-character refusal before I/O. Preserve items and nullable
next_cursor in JSON/YAML; show the continuation after a table. The server
binds cursors to the authenticated actor, tenant and search text. Keep the
same search text when continuing. A disabled workspace remains visible to
an authorized platform administrator.

The [CLI dependency review](#dependency-review) applies, including pinned
[Cobra 1.10.2's Apache-2.0 licence](https://github.com/spf13/cobra/blob/v1.10.2/LICENSE.txt).
No dependency, model or dataset is added. Existing licences permit commercial
use and offline redistribution with their notice obligations.

Test explicit and omitted pagination, literal Unicode and wildcard searches,
table/JSON/YAML output, empty/final pages, malformed/repeated cursors and wrong
detail identity. Invalid arguments fail without a network request or output.
Run the compiled Linux CLI through the real Admin TLS, login, CSRF and cookie
dispatcher with PostgreSQL and no external route. Verify continuation and
retry, foreign-tenant refusal, search-bound cursors, current administrator
permission and revoked sessions. This read slice leaves broader common
operations and installed exporter qualification in DEV-102 open.


## Browser session maintenance (DEV-102)

[GitHub session management](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/viewing-and-managing-your-sessions)
lets an account inspect and end its own sessions. Ownsolid's published
identity contract already provides listBrowserSessions and revokeBrowserSession
on the current account and entrance. The registered browser-sessions handler
requires an empty DELETE body, no query, current authentication and CSRF, and
clears identity cookies when the caller ends its current session.

Expose session list and session revoke with an exact canonical nonzero UUID.
Read one bounded page, preserving the current flag, authentication method and
timestamps. Use the existing Admin-origin session and local CA. There is no
actor, tenant, workspace or Portal selector. Global session management is
independent of the configured cell and workspace.

Hold the existing origin-specific credential lock across revocation. Check that
the credential read before acquiring that lock is still current. This prevents
a concurrent login from being discarded when a response clears the caller's
cookie. Remove the saved credential only after an acknowledged successful
revocation that cleared this client's session. A failed or uncertain response
keeps the credential available. Report uncertainty with the exact target ID
so the operator can inspect sessions and retry under the published contract.
Do not send a synthetic JSON null or an idempotency header to an operation
whose public handler accepts neither body nor query.

The Go [cookie jar](https://pkg.go.dev/net/http/cookiejar) processes the server's
cookie expiry. The existing CLI dependency and licence review applies:
Cobra Apache-2.0, Go and x/sys BSD-3-Clause, and the reviewed YAML and terminal
dependencies. These permit commercial and offline use and redistribution under
their notice terms. No new dependency, model or dataset is added.

Test global read scope and pagination; exact DELETE, CSRF and cookie behavior;
retention after authorization/storage refusal; current-session local cleanup;
and rejection of a credential changed between read and lock acquisition.
Invalid IDs and missing confirmation fail before I/O. A real installed run
must separately verify owned-session revocation, foreign-account/entrance
refusal and the audit through the actual Admin HTTPS dispatcher and PostgreSQL.
Keep this terminal slice and the broader DEV-102 item open until that run and
the relevant repository gates pass.

## Workspace membership discovery (DEV-102)

Reviewed 2026-11-12. Operators need the member UUID, current workspace role and
account state when diagnosing access from a terminal. The published
[workspace contract](../../api/openapi/v1/workspaces/openapi.json) already has
listAdminMembers; no new server operation or authority is needed.

Primary references:

- [Kubernetes kubectl get](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_get/)
  supports bounded list chunks and table, JSON and YAML presentation.
- [GitHub CLI API](https://cli.github.com/manual/gh_api) keeps extra page requests
  explicit through its paginate option rather than conflating one response with
  the whole collection.
- [Go URL query encoding](https://pkg.go.dev/net/url#Values.Encode) encodes cursor
  bytes as a query value; the CLI must not interpolate a cursor into a URL.
- The local workspace Members implementation in lists.go resolves current actor,
  installed tenant and exact workspace before reading memberships. Its cursor
  binds list kind, tenant, workspace, audience and actor. The Admin dispatcher
  remains the platform-administrator boundary. The response uses user_id for a
  membership identity, rather than the workspace resource's id field.

Choose workspace member list <workspace-id>, with an exact canonical nonzero
lowercase UUID, one bounded page, the existing limit/cursor flags and existing
Admin cookie/TLS transport. An explicit workspace argument keeps this global
Admin route independent of configured site, cell or selected workspace. Retain
the complete membership wire fields in JSON/YAML; tables show display name,
email, user ID, role and user status. A disabled account must remain visible.
Reject invalid returned user IDs before printing any row.

Reject implicit context selection, client-side tenant authority, unbounded page
aggregation, local role caches and invented member detail endpoints. Membership
writes remain a separate flow with their existing version/audit requirements;
this read must not issue CSRF, mutation or login requests.

No dependency, model or dataset is added. Existing Cobra v1.10.2 and pflag
v1.0.9 use Apache-2.0 and BSD-3-Clause respectively; Go uses BSD-3-Clause and
go.yaml.in/yaml/v3 v3.0.5 uses MIT/Apache-2.0. They permit commercial use,
redistribution and offline use with their notices. See the pinned CLI go.mod
and the existing dependency review above.

Verification must cover exact GET path and untouched opaque cursor, configured
scope independence, all output forms, maximum safe version precision, disabled
and viewer records, empty final pages and continuation, bad arguments before
I/O, invalid IDs in later rows without partial stdout, malformed pages,
permission/session/cross-tenant/cursor failures and manual retry after a safe
storage error. The complete installed qualification needs real HTTPS/PostgreSQL
through an admitted owner window; a TLS fixture alone does not qualify
tenant authorization or finish DEV-102. Keep the roadmap open while either
installed verification or repository gates remain pending.

## Account discovery (DEV-102)

Reviewed 2026-11-12. The CLI has workspace/member discovery but no account
directory command. An operator diagnosing a membership needs the account UUID,
active/disabled state and platform-administrator flag. The public
[identity contract](../../api/openapi/v1/identity/openapi.json) already exposes
listUsers and getUser; these reads do not need a new identity provider or API.

Primary sources:

- [GitLab Users API](https://docs.gitlab.com/api/users/) offers bounded account
  lists, administrator-only fields, status/search filters and exact-ID details.
  Its administrator visibility remains a server decision.
- [GitHub CLI API pagination](https://cli.github.com/manual/gh_api) makes
  additional page requests explicit. Preserve ownsolid's existing one-page
  envelope and continuation rather than materializing an unlimited directory.
- Identity lists.go defines a literal printable-ASCII email prefix, a Unicode
  substring query and active/disabled status. The list cursor includes the
  filters. The Admin HTTP dispatcher enforces platform-administrator access;
  ListForActor rechecks current actor state. GetUser is a trusted lookup behind
  that dispatcher. Users are the installation's account directory, not a
  workspace membership list; do not invent a tenant or workspace selector.

Choose user list with --email-prefix, --query, --status, --limit and --cursor,
and user get <user-id> with an exact canonical lowercase nonzero UUID. Keep
filter text untrimmed, leave LIKE escaping/case comparison to the server and
URL-encode query values through the existing HTTP client. Reads use only the
configured Admin origin/session, with no cell/workspace context lookup. JSON
and YAML preserve the public User fields, including nullable verification time,
false administrator state and safe integer version. Tables show ID, email,
display name, status and the administrator flag.

Reject email/name-based detail resolution, client role caches, automatic
unbounded pagination, synthetic user IDs, password/status writes and external
directory calls. Do not infer account permissions from the displayed admin
flag; each later API operation still authorizes the current session.

No dependency, model or dataset is added. This slice uses the pinned standard
library, Cobra, pflag and YAML libraries in the dependency review above. Their
BSD-3-Clause, Apache-2.0 and MIT notices permit commercial redistribution and
offline use and remain required with the CLI distribution.

Test exact global GET routes, all three output forms, unchanged filters/cursor,
maximum-safe integer version, false/null fields, disabled records, server page
defaults and manual continuation. Reject malformed rows and wrong detail IDs
without partial stdout. Argument failures must happen before I/O; stable
401/403/404/400/503 errors must remain failures and storage retry must be
explicit. The complete installed qualification still requires the real Admin
dispatcher and PostgreSQL in an admitted original owner window. TLS fixtures
prove client behavior, not account isolation, installed acceptance or DEV-102
completion. Keep those gates and the roadmap open until actually run.
