# Delivery and reliability lane boundaries

## Delivery and upgrades module review

[RKE2 air-gap installation](https://docs.rke2.io/install/airgap) supports
architecture-specific image archives and private registries. Its
[registry documentation](https://docs.rke2.io/install/private_registry)
requires configuration at every pulling node and documents disabling default
endpoint fallback. DEL-108 must prove those properties from a cell worker.
The [Helm chart format](https://helm.sh/docs/topics/charts/) supports
values schemas and local packaging. DEL-107 can own the chart under
`deploy/helm/ownsolid`, but admission still needs a real isolated cell,
a supported ingress path and the release's workload image inputs. Template
rendering alone cannot validate those prerequisites.

Keep the DEL-101 and DEL-110 boundary until the production plug-in, chart,
runtime, and notice inventory exists. DEL-103 and DEL-113 need setup and
console work outside this lane. DEL-109 keeps its issuer prerequisite.
DEL-114 and DEL-115 still need the shared listener, proxy and session
contracts. These boundaries complement [the previous lane decisions](lane-h-decisions.md).

## Platform reliability module review

[PostgreSQL continuous archiving](https://www.postgresql.org/docs/18/continuous-archiving.html)
supports full and incremental base backups, WAL retention, and recovery to
a point in time. DEL and REL keep using portable SQL dumps for application
backup. REL-109 needs a separate native physical backup path with its own
WAL and reconstruction checks. The
[GitLab restore procedure](https://docs.gitlab.com/administration/backup_restore/restore_gitlab/)
stops writers, restores the database and separately restores the keys needed
to read it. ownsolid additionally swaps databases before starting writers.
An interrupted swap needs an explicit, testable recovery procedure. REL-114
tracks this gap, distinct from ordinary command failures, which already
attempt rollback.

REL-101 requires control replica support across the installation lock and
a qualified PostgreSQL failover and gateway topology. REL-102 depends on
measuring that reference topology. REL-104 needs the release gate and
exact release payloads. REL-106 and REL-107 require component owners to
cover worker restarts and in-product alert delivery. REL-111 needs the
console and a backup history API. Leave these items open while progressing
the independent installer and protected operations paths.

## Recovery and release qualification boundaries

The schema 57 run validates restore and reapply at that schema. It does not
resolve REL-113, where a restored predecessor reaches migration 000056 with
a differently deparsed constraint. That work needs catalog comparison or
migration admission changes in the shared database owner. Keep the published
migration files immutable. REL-114 still needs durable recovery across an
interruption between swapping the database and completing restore adoption.

The single-node backup verified the database dump, keys and configured model
store. REL-103 also needs the actual selected object store and a consistency
point spanning its writers. A dump plus a copy of a live PostgreSQL data
directory is not a recovery path. REL-104 needs a clean target qualified from
the exact signed release and backup as part of the release gate.
