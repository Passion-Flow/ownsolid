# Public API compatibility

The public API consists of operations declared for the Admin, Portal and model
gateway audiences. Keep their released methods, paths, scopes and operation IDs
stable within the same API major. Operation IDs and domains determine generated
SDK symbols, so moving an operation between domain packages requires source
compatibility review even if its URL stays available.

## Review a change

Compare with the contract directory of the oldest supported release in the
same major. Obtain that directory from the release source or a local Git
checkout. Run the audit from the repository root:

```sh
python3 tools/codegen/sdk/audit.py --baseline "$RELEASE_SOURCE/api/openapi/v1"
```

The report separates current declaration findings from compatibility findings.
Named response model changes are review findings: regenerated clients can
change return types even when old clients still decode the wire reply.
Review request and response schemas, status and error codes, security
requirements, pagination, idempotency, defaults and semantics separately.
Exercise old SDKs against the new server and confirm upgrade and rollback
on the offline installation before declaring the release compatible.

## Deprecate an operation

1. Provide a usable replacement and test the existing and replacement paths
   with their intended audiences.
2. Mark the old operation deprecated in OpenAPI. Document the replacement,
   first affected release and migration steps in the offline documentation.
3. When the endpoint has deprecation signaling, use the
   [Deprecation response header](https://www.rfc-editor.org/rfc/rfc9745) and a
   local migration link. Use [Sunset](https://www.rfc-editor.org/rfc/rfc8594)
   only once a retirement date is established.
4. Keep serving the previous major during the announced migration window.
   Remove the operation only through the planned version transition and
   after qualification of the upgrade and rollback paths.

See [versioning research](../../docs/research/public-api-versioning.md) for
the comparison rules and acceptance boundary.
