# Portal application projections

This module presents published workflows as Portal applications and lists the authenticated caller's conversations. It does not own a second execution engine or an independent application table. Workflow publication, execution, permissions and conversation state remain with [workflows](../workflows/README.md).

[service.go](service.go) owns List and Conversations; [repository.go](repository.go) borrows the selected database transaction. Each projection requires the Portal audience and current Workspace scope. Application rows bind a published version and graph digest; execution availability and question limits come from the workflow owner's current configuration. Cursor scope includes caller and Workspace, so a cursor is not reusable authority for another user or Workspace.

Pagination defaults come from the control UI policy. The historical domain
migration13 defines workflows; the current account19–21 cumulative chain retains
those real rows and immutable versions. Explicit `portal` composes these
projections at account21/86 tables, with no separate application table or
publication. Unavailable workflow configuration or failed current authority
must not produce a usable cached application view. See the
[cumulative operation](../../../../../docs/milestones/m15-portal/cumulative-operation.md)
and [current queue](../../../../../tools/quality/portal/README.md).

When changing the projection, synchronize [HTTP contracts](../../../../../contracts/http), [applications frontend](../../../../../web/README.md), workflow publication/conversation repositories and [M15 Portal contracts](../../../../../docs/milestones/m15-portal). Preserve both database dialects and caller-private history.

Focused native check: `go -C services/control test ./internal/modules/applications`. The local repository/Portal integration tests require their declared selected SQL fixture and build tags; package compilation alone does not verify current visibility or actual execution.

Portal recovery uses the existing bounded transaction outcome classifier before
ordinary cancellation. Unknown/rolled-back commit outcomes keep their public
code; a mixed private failure cannot be treated as cancellation. Read projections
remain empty on a failed transaction. The repository fixture now uses only the
active migration catalog, avoiding duplicate historical candidate DDL on MySQL.
See [the commit recovery contract](../../../../../docs/milestones/m15-portal/commit-recovery.md)
and the fixed paired controls; execution is pending until retained evidence exists.

[projection.go](projection.go) owns explicit PostgreSQL, MySQL and native Dameng
page queries. The Dameng branch uses numeric enabled predicates, caller-private
conversation scope and a timestamp/ID seek condition with positional bindings.
Only an already-bounded integer LIMIT is rendered into SQL. UTC cursor instants
outside years 1970–9999 or with sub-microsecond precision return INVALID_CURSOR;
they are never rounded into a different page boundary. Unknown dialects fail
closed. The original identity/Workspace locks and current workflow configuration
checks still precede every projection, and transaction failure clears the DTO.

The native branch requires the corresponding identity, Workspace and workflow
repositories plus their actual schema. This module does not create substitute
application tables or bypass a missing dependency. Focused controls in
[dameng_test.go](dameng_test.go) cover bindings and representability; real native
revocation, pagination and commit-acknowledgement tests remain necessary.

The native [applications schema contribution](../../platform/database/dameng/migrate/contribution_applications.go)
defines the actual workflow/version/conversation/run/trace/intent tables and
terminal notices. Signed plugin versions, grants, intents and publication
references are included to retain their real composite FK targets. The
current-version FK is added after both sides exist, without removing the scope
constraint. The fragment is `applications_v2`, depends only on `foundation_v1`,
and requires explicit registration by the migration owner; source authoring
does not activate it. Knowledge/model execution adapters are separate owners.
