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
