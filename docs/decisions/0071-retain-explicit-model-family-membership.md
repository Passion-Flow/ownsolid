# Retain explicit model family membership

Status: accepted for the staged PostgreSQL MOD-103 domain component.

## Context

An immutable model version is already the identity used by imports, deployments,
training and retained derivations. The registry has no established family identity.
Names, model keys, digests and derivation edges cannot establish one safely.

## Decision

Assign each legacy version a distinct persisted random family UUID in one atomic
backfill. Keep its exact version ID and references unchanged. First registration,
reimport after retirement and derived publication create independent families by
default. Reusing identical bytes does not join identities. A publisher can explicitly
select an authorized exact anchor version before inserting its new version in the
same transaction. The new member then belongs to that anchor's established family.
Derived publication never inherits its base's family implicitly.

Retain immutable family and membership rows with scoped deferred foreign keys to
the existing versions. A candidate BEFORE INSERT trigger serializes the exact
version ID and supplies the default membership inside the publication transaction.
Explicit selection uses the same transaction advisory lock before inserting its
membership. Matching replay returns the established identity; a different choice
fails. Hash collisions can serialize unrelated IDs but never join them. Failed or
rolled-back publication leaves no family, membership or selection audit behind.
No independent lineage store or mutable latest-version pointer is introduced.

Admin historical reads resolve a family from an exact anchor under current actor
and workspace authority. Validate the existing retained catalogue and derivation
graph before releasing metadata, including retired entries. Retirement still
refuses execution through existing exact-version admission. A signed continuation
is navigation state, never an authorization grant; every page rechecks authority.

## Consequences

Existing versions become separate families even when an operator would have grouped
them by name. A future audited correction workflow can establish an explicit policy;
this component provides no reassignment or family merge. Existing public DTOs and
execution references retain their shapes. Candidate DDL stays unnumbered and outside
the installed migration chain until ARC admits numbering, roles, catalog, backup and
restore. DEV owns public contract generation and Web owns S-202. See the
[domain contract](../research/training-dataset-lineage.md#durable-family-domain-contract).
