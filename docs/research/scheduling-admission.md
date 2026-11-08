# Whole-job quota admission and preflight

SCH-104, SCH-108 and SCH-110 share a decision boundary.
[Kueue all-or-nothing scheduling](https://kueue.sigs.k8s.io/docs/concepts/all_or_nothing/)
reserves all pod-set demand and can additionally confirm cell readiness.
[Volcano PodGroup](https://volcano.sh/docs/concepts/podgroup/) provides a minimum
member and resource threshold at placement. [Kueue troubleshooting](https://kueue.sigs.k8s.io/docs/tasks/troubleshooting/troubleshooting_jobs/)
shows quota and placement as separate conditions.

Preflight checks the normalized demand against all ancestor ceilings and a
cell's aggregate capacity, then considers current reservations. Explain a hard
limit separately from an exhausted allocation, with resource, requested units
and available units. A snapshot may not prove the physical gang fits across
nodes. Real placement must rely on the cell's all-or-nothing adapter. Any
reservation writer must reevaluate against current locks and store all resources
or none in one transaction. The scheduler never reports a preflight as an
admission and does not release quota merely because a cell call timed out.

Partial reservation can deadlock distributed jobs. Checking only the workspace
misses ancestor pressure. A second heuristic placement engine would disagree with
Kueue and Volcano over actual node availability. Use adapter conditions for
physical placement and scheduler policy reasons for quota.

Only the Go standard library is needed in this slice
([BSD-3-Clause](https://go.dev/LICENSE)). Apache-2.0
[Kueue](https://github.com/kubernetes-sigs/kueue/blob/main/LICENSE) and
[Volcano](https://github.com/volcano-sh/volcano/blob/master/LICENSE) are upstream
references here. No runtime download, model or dataset is required.

Exercise multi-group demand, a missing dimension, one insufficient group in a
gang, quota changes, a full cell, overcommitted live counters, scoped diagnostics
and unchanged inputs on failure. Full acceptance also needs transactional
reservation, contention tests, adapter placement and the preflight and waiting
states in the offline product entrance.
