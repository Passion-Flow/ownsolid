# Lane D shared contracts

## GPU observations and event delivery

The device collectors produce physical GPU readings and same-boot Xid
observations. End-to-end reporting needs an architecture-owned protocol
and cursor delivery, and an authorized control-plane consumer. An
additive, bounded report operation is preferable to overloading heartbeat
or inference preflight fields. Keep the observations local until that
integration is owned and the delivery and quarantine properties can be
tested across the real allocation paths.

## Duplicate and unnamed public operations

The [compatibility projection](public-api-inventory.md#node-exporter-operation-aliases)
gives the four node-exporter operations one canonical Components source.
Nodes retains its SDK methods, local response schemas and help bookmarks.
The [cloud inventory identities](public-api-inventory.md#cloud-inventory-operation-identities)
are explicit and preserve the existing clients and reference links.

## ARC-104 generated Web consumer

The Nodes contract needs the Kubernetes execution profile in the token
request, token reads and node response, as well as the matching agent
handshake and enrollment alternatives. The generated Web types carry the
new value. The Console's exhaustive `profiles` map in
`web/apps/console/src/node-state.ts` then fails TypeScript with TS2741.
The Web owner can add the Kubernetes label and its English and Simplified
Chinese messages in the same merge, or land a map that accepts both contract
revisions before the generated types move. The operator flow and the
candidate, node address and task response projections remain separate.
The contract change is prepared in the lane until that consumer check passes.

## AUD-101 assistant actor HTTP merge point

The public installation-wide Admin list on main retains the original actor
and subject fields. The scoped `ListWorkspaceAuditForActor` repository
method from `fbf038f0` verifies assistant attribution without serializing
it in the legacy response. That method keeps its explicit workspace and
scope-bound cursor.

IAM's staged `9a6d1b6c` handler uses the existing installation-wide route.
Exactly one `X-Ownsolid-Audit-Actor: true` header selects the enriched
representation, without changing authorization, filters or pagination.
Only the verified actor pair is exposed, separately from the user subject.
The [joint audit contract](audit-actor-contract.md) records the matching
OpenAPI, the validation and the landing order. In the joint fast-forward,
the IAM handler precedes the DEV contract and generated-client commit.
Availability follows the joined revision. The Nodes candidate remains an
independent handoff.

## Product assistant HTTP registration

The identity HTTP dispatcher currently registers the two Portal application
reads in `applications/http`. The product assistant catalog consumes those
existing operation IDs, and the service exposes `RunForRequest` internally.
The dispatcher has no product assistant command route. Keep the OpenAPI
surface tied to registered operations. The adapter handoff must demonstrate
the audience and session checks, propagate the server request ID to
`RunForRequest`, and exercise the route's real success and failure responses
before publishing its operation. Both existing reads now refer to the
declared PortalSession scheme. See the [contract verification](public-api-inventory.md#portal-application-session-references).

## Gateway usage CSV attachment

The production dispatcher serves both CSV downloads. The published
operations and SDKs follow the JSON history cursor and the existing
attachment semantics. See the [Web handoff and verification](gateway-usage-csv-contract.md#web-handoff).
The direct continuation header needs the shared HTTP response writer.

## Node exporter reference inventory test

The existing Web Help contract comparison reads the raw Nodes path items
as JSON objects. The three Admin and one Portal exporter path aliases use
OpenAPI references to their canonical Components operations. The
generated Help index retains their legacy entries and bookmarks.
The comparison therefore reports only these four missing expectations.
An isolated comparison using the existing readContract path resolver
produced exact sets for both entrances. The Web owner can make the
comparison resolve Path Item Objects before enumerating methods. Retain
the independent check of the audience and operation set.
