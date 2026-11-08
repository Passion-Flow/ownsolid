# Portal application projections

This module presents published workflows as portal applications and lists the caller's own
conversations with them. It owns no execution engine and no application table: publication,
execution, permissions and conversation state stay with [workflows](../workflows/README.md).

- [service.go](service.go): `List` and `Conversations`, the two portal reads behind
  `GET /api/v1/applications` and `GET /api/v1/application-conversations` (contract:
  [api/openapi/v1/applications](../../../../../api/openapi/v1/applications/openapi.json)).
- [repository.go](repository.go): borrows the selected database transaction and maps failures to
  public codes, keeping private diagnostics out of responses.
- [projection.go](projection.go): the page queries for PostgreSQL, MySQL and Dameng.
- [http/handler.go](http/handler.go): admits only `GET` from a portal session with exactly one
  `workspace_id`.

## Scope and authority

Both reads require a portal session. The workspace comes from the query string, so it is never
trusted: one transaction rechecks the session, locks the workspace and reads the caller's
membership before anything is projected. A workspace the caller is not a member of answers
`RESOURCE_NOT_FOUND`, exactly like one that does not exist, and a failed transaction returns an
empty page. Conversations are always the caller's own.

Pagination defaults come from the UI policy. A cursor binds the caller, the audience and the
workspace, so it cannot be replayed by another user or against another workspace. Execution
availability and the question limit come from the workflow owner's configuration in the same
transaction.

`ListForAssistant` and `ConversationsForAssistant` share those projections.
They append the scoped assistant event after closing the query rows and before
commit. The authenticated workspace supplies the selector and the owner
transaction resolves the scope. The ordinary portal routes call `List` and
`Conversations`.

## Tests

- `go -C services/control test ./internal/modules/applications/...` runs the unit tests.
- The repository tests run with `-tags repository_integration` against real PostgreSQL
  ([environment](../../../../../docs/development/environment.md)). They cover visibility, keyset
  pages, revoked membership and sessions, another workspace, cancellation and unknown commit
  outcomes. `TestActualPairedPortalProcessorProjection` also needs the offline data-processing
  encoder inputs.
- MySQL and Dameng statements are covered by statement tests only ([dameng_test.go](dameng_test.go)).
  Dameng's tables come from the
  [applications schema contribution](../../platform/database/dameng/migrate/contribution_applications.go),
  which the migration owner registers explicitly.
