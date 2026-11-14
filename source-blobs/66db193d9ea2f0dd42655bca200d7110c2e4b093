# Identity construction of Agent gateway published readers

Research for the IAM-owned `newAgentPath`/`NewAgentGateway` integration seam in
ARC-111, TRN-101 and DAT-101. Reviewed 2026-11-13 before implementation. The
[original owner handoff](training-published-file-readers.md#original-author-bundle-and-arc-lifecycle-handoff)
qualifies exact `e639db9035b0b2c0229c90175493c605f328e295` reader tests and
`5b73fa05305c2c5d8178a256683ede8b4e9ef99f` production bytes. Current main has
neither concrete reader. Integration source must retain those original bytes;
no equivalent writer, arbitrary callback or type assertion may replace them.

## Primary sources and current graph

The owner source is available as that exact shared Git object. Its twenty-one
runtime/readme/test paths are exported outside the product for a private Go
compile overlay. A separate SSH bundle-read attempt timed out after thirty
seconds; no successful bundle transfer or bundle hash verification is inferred.
The [reader research](training-published-file-readers.md) links the primary
[Go Root contract](https://pkg.go.dev/os#Root),
[Linux flock contract](https://man7.org/linux/man-pages/man2/flock.2.html),
[Docker read-only bind contract](https://docs.docker.com/engine/storage/bind-mounts/)
and [MLflow artifact architecture](https://mlflow.org/docs/latest/self-hosting/architecture/artifact-store/).
The mechanism and actual owner SQL/file flows already passed their separate
qualifications. No MLflow service or dependency is introduced.

`identity/http/agent_path.go` builds one concrete dataset and training service,
then shares them with preparation, Node and their machine callbacks. Control
later opens its writer stores. The separate gateway currently opens neither;
its real training-claim failure is recorded in
[the role research](agent-gateway-database-role.md#reproduced-gateway-training-claim-failure).
Preserve [ADR 0048](../decisions/0048-agent-gateways-beside-control.md): no
processor, trainer, GPU worker, transfer endpoint or second writable Store is
started by the gateway constructor.

## Chosen additive constructor

Keep `NewAgentGateway` unchanged for the current composition. Add
`NewAgentGatewayWithPublishedReaders` with the same Identity/readiness owners
and a tenant resolved by ARC's trusted installation startup, never HTTP input.
It opens the existing configured dataset and training namespaces through the
two exact `OpenPublishedReader` constructors. It creates no namespace and
never acquires or weakens writer ownership. Both readers are required; partial
failure closes only the readers this call opened and returns no gateway.

Bind the concrete readers to the already shared dataset/training instances in
`agentPath`, leaving both Store fields nil. Preparation continues to see those
same instances. Do not recover them through Node interfaces, expose an internal
writer, open a replacement Store or confuse preparation output ownership with
published training input. No per-exchange SQL or authority order changes.

Return one explicit reader-close lifecycle operation on the gateway. Its owner
calls it after stopping admission and draining/joining active handlers and
claims. It closes only the two independent readers, is idempotent under
concurrent calls and retains the first close result. Caller-owned returned
file descriptors keep their upstream contract. ARC retains startup rollback,
request drain/join, namespace mounts, trusted tenant resolution, process
registration and all other graph resources.

Reject changing the existing constructor signature, implicit file access from a
configured pathname, a second writer, filesystem initialization, guessed remote
mounts, fake verification, new SQL privileges or a gateway processing worker.
The additive constructor remains an unmerged candidate until the original
producer and actual integrated graph can be admitted together.

## Licences and failure acceptance

There is no new dependency, model or dataset. Go/os/sync and x/sys retain their
BSD-3-Clause notices; pgx retains MIT, and PostgreSQL retains its PostgreSQL
License. Commercial use, redistribution and offline use follow those existing
terms. Runtime construction performs only local namespace reads; file and
current SQL authorization remain with their actual owners.

Focused no-database composition tests must use real initialized namespaces and
still-held exclusive writers, an actual lazy pool whose dial hook refuses any
network, and the original concrete readers. Verify exact shared service pointers,
no writer/processor, reader shutdown with retained writer exclusion, wrong
tenant/missing namespace refusal, first-reader cleanup on second-open failure,
and both-reader cleanup on graph-construction failure. Count only descriptors
for those owned namespace inodes, not a process-wide unstable total.

These tests cannot qualify a training lease or restricted database role. Actual
acceptance must repeat the earlier real training claim through the new public
constructor, with original accepted bytes/current actor/attempt/retirement
checks, trusted startup rollback, active request drain/join, installed read-only
mounts, Model/Node/protected-lock grants, complete repository gates and unchanged
fleet budgets. No IAM/TRN/DAT/ARC checkbox is earned by a source or compile check.

## Exact source and execution boundary

The uncommitted four-path IAM review is Git tree
`1af0ca5497fbbe83875eb6af4769fad4198135ac` on
`5bbc324d0bb9edb6f127dff469edadc5b2fee20c`. Its explicit patch SHA-256 is
`8907bbf2f38ce3aec7effb424d33aa4f40bff795ac72fd638d14d1208e0fb53d`.
The source bundle is
`/Users/passion./.codex/ownsolid-evidence/iam-agent-readers-20261113.6ticw6lg/review-source-v1.tar`,
SHA-256 `e475f54c97137d11b7a9f40b687d809f69e4c65d0068822cd1d707858fd8feaf`.
Its adjacent `review-source-v1-hashes.json` has SHA-256
`346e20c56a8032fba2815d4b06522c98ed9d96d7c814d9711f7161061757f6f5`.
All twenty-one original producer paths are included without changing the product
worktree. The bundle also contains the private Go overlay, exact old failed-check
source reconstructed against its recorded hashes, raw command/output and the
current unqualified correction. No producer file was applied to main.

The exact additive public API, in `identity/http/agent_published_readers.go`, is:

```go
func NewAgentGatewayWithPublishedReaders(
    s *identity.Service, ready func() error, tenant string,
) (*AgentGateway, error)
func (g *AgentGateway) ClosePublishedReaders() error
```

`tenant` is ARC's already resolved installation tenant. Roots come only from
`s.Config.Datasets.ArtifactRoot` and `s.Config.Training.ArtifactRoot`.
Missing/foreign namespaces fail before readiness. Both readers open before the
ordinary shared graph is returned; graph-construction failure closes both.
Binding uses the private `agentPath.datasets` and `agentPath.training` pointers,
not a production interface assertion or replacement service. The default
constructor's signature and behavior remain unchanged. ARC can select the new
constructor after admitting the exact producer and close it on later startup
failure, or after stopping admission and joining active calls during shutdown.
The close method does not drain sessions or close the SQL pool on ARC's behalf.

The first no-database candidate compiled and executed three roots: two passed,
one failed, with three passing failure-cleanup subtests and no skips. Its graph
root reached the actual concrete service bindings, then failed an observer
assertion reporting zero namespace descriptors before and after construction.
On macOS, `stat(/dev/fd/N)` describes that path instead of the target inode.
A separate Python descriptor prototype confirmed that direct `fstat(N)` sees
the owned root and that the descriptor appears in the inventory. The corrected
Go observer uses `syscall.Fstat` without opening or closing borrowed descriptors.
It has not run. This failed check is not product/SQL/gateway qualification.

The initial resource sample had only 454,164,480 free bytes (below 2 GiB), but
was mistakenly combined with test launch instead of evaluated first. The
short-lived test had already exited when the error was identified; no background
work was retained. The corrected entry now evaluates admission before Go.
A separate guarded opportunity returned raw 78 with 709,476,352 free bytes,
against 2,147,483,648 required; disk free was 165,373,104,128 bytes against
16,106,127,360 required. It started no compiler/test/database and queued no
retry. Both raw attempts remain preserved. The permanent workstation carrier
was released and not recreated, truncated or removed.

No corrected unit pass, race run, real SQL claim, provider, processor, GPU,
installed gateway or full repository gate is claimed. The actual no-database
fix needs a fresh admitted opportunity; the actual integrated gateway still
needs ARC's original native construction/startup/drain/role and fleet acceptance.
