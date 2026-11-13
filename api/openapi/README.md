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

## Audit-read 409 migration in v1

The comparison from 9e32b023 to 2e64ab4c explicitly declares HTTP 409
STORAGE_COMMIT_OUTCOME_UNKNOWN for Admin GET /api/v1/admin/audit-events.
The selected server sources already produce this failure; the previous default
declares the same Problem body. Method, path, operation ID, Admin authority,
tenant/workspace enforcement, request inputs and successful page are unchanged.
Keep v1 and the explicit response. This is an accepted technical contract
correction with a generated Go response migration; it is not an auditor error.
See [ADR 0085](../../docs/decisions/0085-preserve-audit-read-default-response-accessors.md)
for the scope and remaining release qualification.

| Caller | Required handling |
| --- | --- |
| Existing Go SDK with default-only 409 decoding | Handle status 409 through GetApplicationproblemJSONDefault() and HeadersDefault. Preserve its code and request ID. Qualify its configured transport against lost replies; source compatibility does not guarantee absence of retries. |
| Newly generated Go SDK | Prefer GetApplicationproblemJSON409() and Headers409. The owned generator also retains the legacy Default fields for 409 throughout v1. Full generation and Go protocol checks passed for e4bd3138 with ee7a214c fixtures; installed-server and release qualification remain separate. |
| Python sync and async SDK | Continue checking the detailed response status and Problem.code; both default and explicit branches use Problem. Disable caller/transport retry wrappers. |
| CLI audit list | Existing error decoding retains HTTP status, stable code and request ID and does not render a successful page. Do not wrap this command in a retry loop. |
| Raw HTTP consumer | Decode application/problem+json, retain the server request ID and treat 409 as uncertain access accounting. Never infer a returned page or a rolled-back audit from this failure. |

Upgrade clients by regenerating from the reviewed contract and generator as one
source set, then checking both the old Default and new 409 fields. Never copy-edit
generated files. A lost reply or this 409 has no automatic recovery request:
the access audit might have committed. A deliberate retry with the same filters,
cursor and representation records a new access with a new server request ID.
Keep the earlier ID when received; no idempotency key can replay this GET's audit.
Neither an error body nor a retry may supply tenant/workspace authority.

Before rollout, run old-client/new-server and regenerated-client/previous-server
checks offline, with both standard and assistant representations. Inject a COMMIT
that is not forwarded and a successful server COMMIT with its acknowledgement
suppressed. Require 409 with the stable code, matching body/header request ID,
no page, no Retry-After and no automatic second request; a deliberate read must
have its own request ID. Recheck revoked sessions, forged entrance and denied
workspace access. For client rollback, retain the previously qualified SDK and
its transport configuration. Code using a newly introduced 409 accessor must
either keep that SDK or use status and raw Problem decoding when compiled with
the older SDK. Server rollback does not change the reviewed wire response.

Keep the exact comparison baseline and raw 430-operation report: the explicit
409 named-response finding remains present with exit 1. The migration decision
does not turn that result into exit 0. A supported-release baseline still needs
selection. Complete 28-domain generation and the four Go caller migration groups
have passed for the exact e4bd3138/ee7a214c source pair; see the
[SDK qualification](../../docs/research/sdk.md#qualified-tool-generation-and-go-client-migration).
Actual installed-server/database upgrade and rollback, remaining SDK runtime
checks, make check, make build and related tests still must pass on the final
source before release compatibility is declared. No release version or completion
tick is created by these SDK protocol checks.

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
