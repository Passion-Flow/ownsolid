# Scheduling priority and fair sharing

[SCH-103](../product/roadmap/scheduling-and-quota.md) orders competing jobs.
[Kueue priority classes](https://kueue.sigs.k8s.io/docs/concepts/workload_priority_class/)
separate workload admission priority from pod priority. [Kueue fair
sharing](https://kueue.sigs.k8s.io/docs/concepts/fair_sharing/) compares dominant
shares when choosing between queues. The
[Dominant Resource Fairness paper](https://www.usenix.org/conference/nsdi11/dominant-resource-fairness-fair-allocation-multiple-resource-types)
explains why GPU-only ordering can disadvantage CPU- or memory-heavy users.

Rank eligible work first by the operator-resolved priority, then by the lowest
current dominant share of its queue's capacity allocation, then by a durable
enqueue sequence. Compare integer ratios exactly. Rank only queues competing
in the same policy domain. Recompute shares after a reservation when dispatch
is composed. A queue's individual runtime age and caller clock do not affect
ordering. This is a snapshot ranking rule. Kueue's decayed historical share
requires a durable usage history and remains a separate policy option.

FIFO alone favors a team that can submit the biggest backlog first. Floating
point shares can collapse distinct integers into the same rank. Strict priority
is intentional and needs limits on who can select priority classes, since an
unbounded high-priority queue can starve lower classes.

Only Go's [BSD-3-Clause standard library](https://go.dev/LICENSE) is needed.
Kueue is an Apache-2.0 design reference. No upstream code, model, dataset or
online service is used.

Exercise priority, dominant-resource switching, equal shares, near-integer-limit
quantities, invalid dimensions, and FIFO ordering with stable ties. Full
acceptance needs durable queue and priority configuration, authority, repeated
reservation and offline workload submission from a product entrance.
