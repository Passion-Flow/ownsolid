# cellclusters

Installs and manages one Kubernetes cluster per cell (ARC-104,
[ADR 0051](../../../../../docs/decisions/0051-kubernetes-cells-with-rke2.md), research in
[Kubernetes cells](../../../../../docs/research/kubernetes-cells.md)). PostgreSQL only.

Control decides; agents act. A cluster has one server node and any number of worker nodes,
all enrolled with the `kubernetes_node` profile. Control writes node tasks; the agent on each
node installs RKE2 from the offline bundle and reports back.

## Flow

1. An administrator creates the cell's cluster (`POST /api/v1/admin/cell-clusters`, with an
   `Idempotency-Key`). Every node is checked and locked: active, of the Kubernetes profile, with
   the address it enrolled from, in no cluster, and with an address no other member has. The
   server's join task is written in the same transaction when the server is idle.
2. The server's agent joins (`kubernetes.join.v1`) and reports the cluster CA hash.
3. While workers wait, control asks the server for a bootstrap token
   (`kubernetes.token.v1`) that outlives a join. The token is sealed with AES-256-GCM under a
   key derived from `kubernetes_cells.join_token_key_file`, bound to the cluster ID, and never
   stored in a task row: a worker's claim adds it to the lease.
4. Workers join in batches of at most `join_batch`. A busy node stays pending and joins later;
   an offline node fails at once with `NODE_UNAVAILABLE`.
5. A failed member keeps its error code and can be retried. `observations` asks settled members
   to report their readiness again (`kubernetes.observe.v1`).

## Who writes what

- Results only record state (`RecordKubernetesResultWithin`); they never write tasks for other
  nodes, so a result never waits on another node's lock.
- `Reconcile`, run every `reconcile_seconds`, is the only place besides an administrator's
  request that writes tasks. It locks the cluster row (skipping one another process holds),
  settles members whose task ended without a result (deadline, lost delivery), asks for tokens
  and joins workers.
- Lock order is cluster, then nodes. A create also locks the cell row first, so two creates
  cannot both find the cell empty.

## Files

- `service.go`: construction, administrator writes, refusal codes.
- `orchestration.go`: the reconciler and the task-writing steps.
- `authority.go`: the node module's `KubernetesAuthority`: lease completion, result recording
  and RKE2 file ranges.
- `reads.go`: cluster summaries and paginated members.
- `seal.go`, `artifacts.go`: join-token sealing and verified RKE2 files.
- `http/`: the administrator routes.

Tests in `service_test.go` run against PostgreSQL (`PROJECT_ONE_TEST_DSN_FILE`) and skip
without it.
