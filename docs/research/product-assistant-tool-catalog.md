# Reviewed product assistant tool catalog

AGT-103 needs an inventory derived from OpenAPI that grants only the operations
reviewed for assistant use. A new platform API must not become an assistant tool
merely because a contract describes it.

[OpenAPI 3.1.1 operation IDs](https://spec.openapis.org/oas/v3.1.1.html#operation-object)
identify operations within a description. [Go generation](https://go.dev/blog/generate)
lets a package commit reproducible output from build-time inputs. [Kubernetes
server authorization](https://kubernetes.io/docs/reference/access-authn-authz/authorization/)
separates operation discovery from the request-specific authority decision.

Use a reviewed overlay keyed by local tool ID, contract operation ID and exact
path. Select only the two typed portal reads already dispatched by the service.
Verify the method, path, audience and declared session cookie and include the
reviewed read risk and workspace-member permission in the result. Changing a
path, security requirement or credential requires review. An automated
comparison rejects stale output. At runtime, use the generated admission data
before calling the resource owner. The existing projection transaction still
rechecks the authenticated user, current membership and private ownership.

Both application operations now reference `PortalSession` directly after
`9714d52d`. The [OpenAPI security requirement](https://spec.openapis.org/oas/v3.1.1.html#security-requirement-object)
requires the reference to name a declared scheme. The only caller of this
generator reads the contract in the same checkout. Older revisions retain
their own generator. Require the canonical reference and exact Portal cookie
declaration. Reject an absent or misplaced reference, anonymous alternative,
nonempty role array, changed scheme type or other cookie. The adapters still
call the application services in process using authenticated server context.

`9cb3d16a` adds the Portal history search parameter to the conversations
contract. Catalog generation projects the reviewed operation ID, route,
method, audience, risk and permission. It carries no cookies, security scheme
objects, credentials or request arguments. The assistant conversation read
continues to accept only its existing cursor.

[RFC 8259 object member uniqueness](https://www.rfc-editor.org/rfc/rfc8259#section-4)
and [Go JSON v2 decoding](https://pkg.go.dev/encoding/json/v2) support rejecting
duplicate names and invalid UTF-8. Use the same standard-library decoder
already used in control-plane dataset imports. Reject unknown overlay fields
and accept unrelated OpenAPI fields. Reject ambiguous or misspelled policy
keys before their content can affect admission.

A generic OpenAPI-to-HTTP proxy would let tool parameters or advertised URLs
influence transport and authorization. Generating every operation by default
would publish writes and sensitive reads before their confirmation and audit paths
exist. Keeping all HTTP metadata by hand would allow contract and catalog to drift.

The generator and runtime catalog use the [Go standard library](https://go.dev/LICENSE)
under its BSD-style license, which allows commercial redistribution with notices
and offline use. The tool inventory is ownsolid data. The slice adopts no external
SDK, model or dataset and needs no new runtime network connection.

Verify byte-for-byte regeneration from the latest contract and both reviewed
output entries. For each real operation, remove or change its session reference
and expect generation to fail. Exercise path and method drift, duplicate JSON
members and operation IDs, unknown overlay keys, broadened audiences and changed
cookie authentication.

Regeneration against the corrected applications contract was byte-for-byte
identical. The two exact output entries passed strict decoding, including
their Portal audience and reviewed risk and permission. Both real operations
were refused independently when their session reference was removed, moved to
the path item or replaced with the undeclared legacy reference.
The release item also needs tools wired through the authenticated assistant
entrance and reviewed coverage of the complete enabled inventory. The current
catalog contains only the two implemented read adapters.
