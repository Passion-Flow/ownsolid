# Installation release records

`releases` reads one explicitly configured, immutable-at-publication projection from
the offline operator. `configuration.go` retains protected file IO and delegates the strict JSON policy to
`contracts/release/view.Configuration`;
platform/config owns only its optional reference and original explicit env-file mapping.
`snapshot.go` verifies the canonical fixed directory, publisher UID distinct from actual
nonroot runtime EUID,0444 single-link files, bounded bytes, original scope/manifest/configuration
bindings, before/after dirty fences and descriptor/named-file identity. No request can
select a file, scope or tenant. The internal UID observation seam is only for deterministic
rules testing; production always uses os.Geteuid and has no override switch.

`service.go` rechecks ordinary current Admin authority on the selected store before and
after reads, confirms the installed tenant, and commits its read audit before releasing
any DTO. A32-hex scope nonce is not written to UUID audit target_id. Snapshot cursor
binding prevents mixing pages from different journal heads. Unknown commit/failure
returns no data. `http` is mounted behind existing authentication and Origin transport
policy; it provides exactly two readonly GET endpoints and no operator mutation.

The publisher retains the original complete journal. The versioned wire projection has
a safety bound of256 latest events and explicit first/last sequence. It cannot prove
live process health, cold installation or target compatibility. Current SQL applied
schema number and the actual admitted locale registry are independently labeled.
No new migration, database pool, worker or raw filesystem authority is introduced.

See [contract](../../../../../docs/milestones/m17-offline-compose/admin-release-view.md),
[API](../../../../../contracts/http/v1/releases/openapi.json) and
[configuration](../../../../../config/examples/release/admin-view.schema.json).
Focused controls: `go test ./internal/modules/releases/...`; actual paired SQL test:
`go test -tags repository_integration ./internal/modules/releases -run
'^TestActualReleaseViewReadAuditAndCurrentAdminRevocation$' -count=1` with the existing
repository fixture and protected PROJECT_ONE_RELEASE_VIEW_TEST_CONFIG. The latter
requires a genuinely separate existing publisher UID, never a new macOS account or a
same-user bypass. Real runtime UID/mount/browser/Compose qualification stays separate.

## Focused HTTP and PostgreSQL verification

The [fixed quality owner](../../../../../tools/quality/releases/README.md) selects
`TestActualReleaseHTTPAuthorityAuditAndFreshness` from
`http_repository_integration_test.go`, using existing build/native/readback tools.
It exercises actual private-CA TLS through the Identity dispatcher, Admin/Portal
login, current PostgreSQL authority, read audits, pagination/head fences and an
actual audit-insert refusal. Only snapshot contents are a scripted test double.
`reader_repository_integration_test.go` exports its factory solely in the tagged
test archive; production `New` unconditionally binds private `readSnapshot` and
refuses a missing reader. There is no deployment/UID/configuration override.

Public statuses follow the original dispatcher and mapper: Portal 404, absent or
currently ineligible Admin session 401, malformed query/body 400, changed head 409
and unavailable projection/audit 503. The internal ACCESS_DENIED guard does not
expose a separate Portal 403 route. Select only the fixed HTTP leaf for this gate;
the original real-file/cross-UID test above remains a separate prerequisite, and
neither compilation nor the scripted snapshot qualifies protected mounts or
Compose/browser delivery.


## Cumulative M17 source boundary — 2026-10-27

The explicit cumulative `releases` selection now composes this owner through `serveAccount` and the existing Identity dispatcher. Its protected policy is required; other bounded profiles continue to refuse that reference. Actual cross-UID mount, SQL/HTTP/browser and installer evidence remain separate from source/profile controls.

`VerifyProjection` exposes the existing production filesystem reader to the
readonly native startup verifier. It uses actual EUID and performs no SQL, session,
worker or publisher construction. The additive Kubernetes frame keeps the same
configured scope/manifest/configuration, dirty-file and changed-head fences.
