# Hierarchical scheduling quota

## Upstream practice and policy

[Kueue cohorts](https://kueue.sigs.k8s.io/docs/concepts/cohort/) aggregate quotas
across a hierarchy, and [ClusterQueues](https://kueue.sigs.k8s.io/docs/concepts/cluster_queue/)
distinguish nominal quota, borrowing limits and resource flavors.
[Volcano hierarchical queues](https://volcano.sh/docs/v1.12.0/keyfeatures/hierarchicalqueue/)
divide parent capacity among descendants and can reclaim sibling allocations.

ownsolid owns platform, tenant, department and workspace capacity. Guaranteed
quota is the allocation entitled to priority when competing for capacity. Burst
is the inclusive hard ceiling, so it is at least the guarantee. For every
resource and every parent, the sum of the immediate children's guarantees and
the sum of their ceilings must each fit the matching parent limit. This admits
unused parent headroom without allocating more capacity than the parent has.
Borrowing and reclaim (SCH-109) must respect these hard ceilings.

Use a versioned PostgreSQL snapshot for the first, single-platform policy.
Quota configuration updates are infrequent. Validate the complete proposed
tree and use a compare-and-set write against the revision the operator read.
A second concurrent edit must reread and validate the combined changes.
PostgreSQL [transaction isolation](https://www.postgresql.org/docs/18/transaction-iso.html)
documents the behavior of concurrent updates and predicate rechecks. An exact
retry immediately after a committed update resolves to the same revision.
Runtime admission must use the effective revision and own its reservation
locks separately. A tenant/workspace binding must come from authenticated
control scope before the caller enters the module.

Pure in-memory checks cannot serialize distinct control processes. Independent
Kueue or Volcano policy edits would bypass ownsolid's allocation and split
authority. Global locking on every job is unnecessary for quota configuration.
Keep configuration changes and future runtime accounting separate.

## Licensing

Only existing Go, pgx, UUID and PostgreSQL are required, with commercial,
redistribution and offline terms documented in [the job model note](scheduling-job-model.md).
The two Apache-2.0 scheduler projects are design references here. No additional
dependency, model or dataset is required.

## Failure tests and acceptance

Cover invalid parent kinds, cross-tenant attachments, missing dimensions, sums
over budget for guarantees and ceilings independently, exact fits, parent
reductions, concurrent siblings competing for the final capacity, rollback and
restart. An operator must ultimately be able to inspect and change authorized
limits from the Console, see actionable failures, and have those limits enforced
during real offline admission. Keep the roadmap open until that path is tested.
