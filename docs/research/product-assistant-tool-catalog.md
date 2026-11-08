# Reviewed product assistant tool catalog

AGT-103 needs an inventory derived from OpenAPI that grants only the operations
reviewed for assistant use. A new platform API must not become an assistant tool
merely because a contract describes it.

[OpenAPI 3.1.1 operation IDs](https://spec.openapis.org/oas/v3.1.1.html#operation-object)
identify operations within a description. [Go generation](https://go.dev/blog/generate)
lets a package commit reproducible output from build-time inputs. [Kubernetes
server authorization](https://kubernetes.io/docs/reference/access-authn-authz/authorization/)
separates operation discovery from the request-specific authority decision.

Use a reviewed overlay keyed by local tool ID and contract operation ID. Select
only the two typed portal reads already dispatched by the service. Generate the
method, path and audience from the source contract and include the reviewed read
risk and workspace-member permission in the result. Require a portal cookie
contract and a read method for each approved operation. Reject absent, duplicate
or changed operations, authentication or audience at generation. An automated
comparison rejects stale output. At runtime, use the generated admission data
before calling the resource owner. The existing projection transaction still
rechecks the authenticated user, current membership and private ownership.

A generic OpenAPI-to-HTTP proxy would let tool parameters or advertised URLs
influence transport and authorization. Generating every operation by default
would publish writes and sensitive reads before their confirmation and audit paths
exist. Keeping all HTTP metadata by hand would allow contract and catalog to drift.

The generator and runtime catalog use the [Go standard library](https://go.dev/LICENSE)
under its BSD-style license, which allows commercial redistribution with notices
and offline use. The tool inventory is ownsolid data. The slice adopts no external
SDK, model or dataset and needs no new runtime network connection.

Verify byte-for-byte regeneration, reject an operation moved to a write method,
removed or duplicated IDs, broadened audience, and missing cookie authentication.
The release item also needs tools wired through the authenticated assistant
entrance and reviewed coverage of the complete enabled inventory. The current
catalog contains only the two implemented read adapters.
