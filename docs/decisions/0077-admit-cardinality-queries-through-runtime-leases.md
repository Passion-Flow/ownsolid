# 0077: Admit cardinality queries through runtime leases

Status: Accepted
Date: 2026-11-13

## Context

OBS-110 daily cardinality is correct only for an approved raw source with its
per-day index enabled and bounded native searches. A protected URL says where to
send requests, but does not prove which process is serving it or which native
profile that process actually uses. The retained native fixture proves selected
source semantics; its test assertion cannot grant installed-product authority.

## Decision

Require a typed runtime source owner at internal Go composition. It resolves a
configured canonical source reference against the installed cell's authenticated
inventory and supplies a generation-bound query lease. The observation binds the
approved and running executable digests, actual arguments and endpoint. A live
context invalidates the lease on lost source authority, process exit or changed
endpoint/profile. Root owns real process supervision and installed admission.

The monitoring module checks this observation on opening and on every query.
It requires explicit unique daily-index false mode, the configured finite scan
limit, sufficient bounded native rank and whole-day retention, and a positive
status-query timeout within the operation budget. It rejects ambiguous flags,
environment templates and enabled environment flag loading. The lease's
one-hop transport must preserve TLS/endpoint binding and request cancellation,
without ambient proxy or redirect behavior. A source replacement requires a new
verified generation; queries acquire fresh leases to recover.

The module retains fixed tenant/Workspace selectors from current server
authority, bounded decoding, post-I/O authority checks and committed request
audit. It discards results on source loss or release failure. Adapter Close
cancels query authority; lease Close releases a query only. Neither stops the
native store. The startup owner closes the adapter on module construction
failure and through its shutdown chain. A configured policy without this
concrete adapter fails construction; an omitted policy stays disabled.

## Alternatives

A URL or caller boolean cannot prove native identity/profile. Accepting fixture
admission as product authority confuses two scopes. A second process manager,
source registry or proof ledger in monitoring duplicates the runtime owner's
responsibility. The typed lease reuses that owner without a new database,
service, dependency or default production switch.

## Consequences and required evidence

This prepares a callable module boundary. It does not implement the concrete
installed runtime owner, shared startup/config/IAM route changes, public API,
Console flow or hard ingest budgets. Query-time profile checking does not write
metrics or logs into the control-plane database and adds no native data copy.
Existing fleet/storage/latency measurements retain their exact source scope.
The new source must separately pass targeted identity/profile/cancellation and
cleanup tests, integration-tag compilation, then coordinated current-main native
and installed acceptance plus the repository gates. Fake owners prove protocol
behavior only. See the [research and upstream sources](../research/series-cardinality-scope.md#current-main-integration-and-producer-boundary).
