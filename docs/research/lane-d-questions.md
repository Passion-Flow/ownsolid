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

The contract audit found four node-exporter operations in both the nodes
and components documents. Removing one declaration can remove an existing
SDK symbol. Adding explicit IDs to six cloud inventory operations can
rename generated client methods and the UI's reference entries. Resolve
the source of truth with the relevant API and UI owners, preserving or
migrating any released client symbols. Retain the current declarations
and report the findings until that coordinated change is available.

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
before publishing its operation. The existing application cookie scheme
reference mismatch is tracked in the [APP handoff](app-lane-questions.md#assistant-catalog-handoff-for-agt-103).
