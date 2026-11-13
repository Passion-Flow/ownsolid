# Preserve audit-read default response accessors

Status: accepted technical migration decision. Complete SDK generation and Go
protocol migration are qualified for e4bd3138 with ee7a214c fixtures. Release and
installed-server compatibility remain unqualified. Reviewed on 2026-11-14 for DEV-204.

## Context

Comparing 9e32b023 with 2e64ab4c adds an explicit Problem response for HTTP 409
on Admin GET /api/v1/admin/audit-events, listAuditEvents. The default already
declares that same response. The selected server transaction, identity and HTTP
sources are unchanged: an unconfirmed audit-read COMMIT produces
STORAGE_COMMIT_OUTCOME_UNKNOWN, releases no page and has no Retry-After.
Authentication and installation, tenant and workspace authority remain enforced
by the server. Descriptions clarify this behavior without changing input schemas.

This corrects a missing explicit declaration of existing behavior. The source
audit is nevertheless correct: the pinned Go generator moves 409 data from
ApplicationproblemJSONDefault/HeadersDefault into new 409 fields. Existing callers
can still compile and then observe nil. Python retains its Problem return model.
The exact source and wire boundaries are in the
[versioning research](../research/public-api-versioning.md#formal-audit-read-409-migration-decision).

## Decision

Keep the v1 route, operation identity, explicit 409 and current contract metadata
version. Preserve the old Go Default Problem and header fields for 409 as aliases
alongside the dedicated 409 fields. New callers should use the dedicated accessor;
existing callers can keep the old accessor while migrating. An old already
compiled SDK continues using its default decoder; actual old-client/new-server
qualification is still required before making a release compatibility claim.

Implement the adaptation in the owned generator, never by editing generated
files. Limit it to the reviewed audit parser and identical shared Problem
references for 409 and default. Fail generation on an unexpected parser shape or
changed response references. Leave other statuses, methods, transports and the
raw compatibility audit unchanged. Do not assign a fictitious SDK release version.
Retain the old accessor behavior throughout v1; any removal requires a separately
announced version transition and support window.

Neither 409 nor a lost response authorizes automatic retry. Preserve the earlier
request ID when available. A deliberate new call with the same selection is a new
audited access and cannot establish whether the first access audit committed.
Errors release no page or assistant identity. Rollback keeps the same server wire
behavior: callers using only the new accessor must retain their qualified SDK or
use status plus raw Problem decoding if rolling back to an older SDK source.

## Acceptance and consequences

Keep the one explicit named-response difference visible with audit exit 1.
This ADR accepts its technical migration disposition; it is not a tool waiver,
release approval, runtime run or completed DEV-204 capability.

Pure Python checks cover the exact adaptation boundary, old/new response binding,
idempotence and fail-closed changes. Fresh generation must then type-check and run
native assertions for both old and new accessors, headers and both audit
representations. Retain old-client/new-server and new-client/previous-server
upgrade/rollback checks, actual COMMIT-fault/authentication/tenant tests, offline
operation and complete make check, make build and related tests. Do not publish
or mark release compatibility complete while those checks remain unrun.

Requiring a caller-only rewrite leaves an avoidable v1 regression. Removing 409
misstates the server; suppressing the finding or changing the baseline conceals
the review. Those alternatives are rejected. No dependency or runtime schema,
permission, tenant, storage or retry policy changes with this decision.

## Qualified generated-client scope

An actual tagged v2.8.2 tool and Python generator 0.29.1 generated all 28 domains
offline from e4bd3138's 36 inputs. Both language outputs account for all 430
operations. The original 2e64ab4c generation produces the same 1,675-file set:
1,674 files are byte-identical and only the intended Go identity parser gains
the Default adaptation. The original default-only 9e32b023 identity SDK was
generated separately without altering generated code.

The same legacy caller compiled and ran against both old and new SDKs; the
explicit-409 caller passed against the new SDK, and the legacy caller passed
again for client rollback. All four groups exited 0 under the race detector,
passing 23 root cases and 20 subcases with no skips. This includes old public
field assignment, both representations, unknown status, lost replies, deliberate
retry, TLS/cookies, timeout, redirect refusal and concurrency checks.

These are generated SDK and real loopback HTTP protocol results, not a new
Control/database, UI, supported-release or complete platform run. The raw
430-operation audit remains declaration exit 0 and baseline review exit 1 with
the sole explicit 409 finding. The
[source and output evidence](../research/sdk.md#qualified-tool-generation-and-go-client-migration)
retains the exact qualification and release boundaries.
