# Admit fair queue turns with the owner transaction

Queue order must still hold when the workload owner creates a task. A
read-only fair-share snapshot followed by an independent reservation can
admit two consumers using the same old share or a dequeued entry.

Keep the workload owner's PostgreSQL READ COMMITTED transaction as the
single commit owner. Scheduling takes the existing shared quota-policy
lock, validates the complete authorized workspace domain and takes its
existing workspace advisory locks in canonical ID order. Only then does
it call the owner's read-only eligible-queue reader through that same
executor. It ranks current holds, checks the selected head and uses the
existing reservation path. Exact held attempts replay without requiring
the already-dequeued entry. Owner dequeue, physical holds, task and audit
commit with the scheduling hold.

The owner retains authority checks and actor, node, job and physical GPU
fences. It must call this operation before other scheduling writes. The
reader cannot lock other jobs, write, open another transaction or use
network I/O. A missing candidate, reader failure, changed replay or
storage failure cannot create a task. A queue-turn wait uses the owner's
savepoint rollback before persisting its wait state. A cancellation
request cannot release a running attempt without the existing positive
stop or non-delivery proof.

This reuses the quota and cell boundary and does not add a scheduler-owned
durable queue or a global exclusive admission lock. Unrelated domains can
continue independently. The production training readers, dispatch hooks
and catalog migration remain separate owner integrations. Persistent
owner fixture tests qualify transaction behavior, not that production
flow. See the [queue integration contract](../research/scheduling-queue-policy.md#training-owner-patch-contract).
