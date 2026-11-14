# Product assistant audit attribution

AGT-107 records the signed-in user as the subject of an assistant action and
the product assistant as the actor. The existing user permissions remain the
authority for execution. Actor attribution gives audit evidence of delegation
and does not grant access to an operation.

## References and dependencies

- [RFC 8693 actor claims](https://www.rfc-editor.org/rfc/rfc8693.html#section-4.1)
  distinguish an actor from the subject on whose behalf the actor acts. Use
  that distinction in audit without introducing token exchange or service
  account credentials.
- [Kubernetes audit events](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/)
  carry request correlation, authenticated identity, resource and outcome.
  [Kubernetes audit levels](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/)
  distinguish request metadata from bodies.
- [OWASP application logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
  describes interaction identifiers and data that must stay out of event logs.

The existing Go standard library uses a [BSD-style license](https://go.dev/LICENSE)
and PostgreSQL uses the [PostgreSQL License](https://www.postgresql.org/about/licence/).
Both permit commercial use, redistribution with notices and offline operation.
The proposed connection needs no new package, external service, model or dataset.

## Available writer and remaining boundary

The legacy [audit writer](../../services/control/internal/modules/audit/repository.go)
accepts an actor user and a subject user. In the
[identity schema](../../services/control/migrations/postgresql/000001_identity.up.sql)
both fields reference real users. A null actor cannot distinguish the assistant
from other system activity. The writer also uses the installation's default
workspace instead of the authorized operation's actual workspace and has no
typed actor metadata parameter. The
[legacy audit list representation](../../services/control/internal/modules/identity/lists.go)
exposes human actor fields. The identity owner added a verified opt-in
representation in the [Admin audit contract](../../api/openapi/v1/identity/openapi.json).

SCI added [WriteProductAssistantWithin](../../services/control/internal/modules/audit/product_assistant.go)
for PostgreSQL. It borrows the operation transaction, stores the assistant
actor in fixed metadata, and keeps the authenticated user in the subject
field. It checks the supplied installation tenant, workspace and subject.
The chain binds the metadata digest. The safe workspace list and opt-in
Admin list verify that commitment. Export disclosure needs a separately
verifiable binding. See the
[writer contract](product-assistant-audit-writer.md).

Using the signed-in user's ID as both actor and subject would erase delegation.
Creating an assistant user would confuse an executor with an authorized person.
Encoding actor identity in an action prefix or free-text reason would leave
identity inference to readers. Direct SQL from productagent could use existing
metadata but would bypass the audit owner's typed persistence and read contract.
These paths do not satisfy AGT-107.

## Smallest handoff

1. Use the scoped append for authorized operations that already resolve a
   workspace. Pass the verified user subject, resolved tenant and workspace,
   audience, server request ID, registered tool ID and bounded outcome. The
   writer supplies the actor. It requires a workspace, so workspace-free
   navigation needs a shared global-scope policy before it can use this path.
2. The Admin list exposes the actor with `X-Ownsolid-Audit-Actor: true`.
   Keep the default representation compatible. The existing export carries
   a metadata digest. An actor displayed
   beside that digest needs an independently verifiable binding. Prefer
   including the typed actor fields in the next hash projection so the export
   can keep other metadata redacted. Introduce a format boundary as in
   [audit chain v2](../../services/control/migrations/postgresql/000054_audit_chain_scope.up.sql)
   and update the [offline verifier](../../tools/audit-verify/verify.go).
   Preserve historical records and checkpoints.
3. The [identity HTTP owner](../../services/control/internal/modules/identity/http/handler.go)
   passes the authenticated identity and the request ID generated in
   `serveHTTP` to
   [RunForRequest](../../services/control/internal/modules/productagent/service.go)
   when adding the assistant route. This correlates the event with the
   response's `X-Request-ID`. Navigation already appends in its session and
   scope transaction. Application and conversation reads use
   [the application owner transaction](../../services/control/internal/modules/applications/repository.go)
   after authorization and projection. The ordinary application routes
   continue to use the existing projection methods.
4. Each later confirmed write appends the event inside the owning mutation
   transaction with the exact action and idempotency record. A failed append
   cannot return a successful action or disclose a read result. After rollback,
   an authenticated failure can be recorded with a bounded error code and
   the caller's verified scope. Refused or unknown commands must not leak
   another tenant's identifiers or record the submitted command text.

Record only stable identifiers and outcomes. Keep prompts, aliases as typed,
cookies, tokens, confirmation data, cursors and result bodies out of audit.
An audited navigation result records the server's response to the command.
Whether the browser opened the page is a separate event.

## Scoped command integration

Keep the in-process command call compatible and generate its request ID on the
server. Add a correlated command entry for the HTTP owner to pass the ID it
already assigns before dispatch. Both entries use the same audit path. An
invalid supplied request ID fails before dispatch. The authenticated workspace
is a selector and the transaction resolves its tenant and membership again.

Borrow the real command transaction. Navigation uses the identity and
workspace fences before appending. The application owner uses dedicated
assistant reads that share the existing query, paging and membership path.
Append only after the page has been read and its rows closed. Ordinary
application and conversation reads keep their existing contract.

The available writer requires an authorized workspace. Workspace-free command
execution returns a scope error instead of a route or data, and its command
directory is empty. A future shared global-scope contract can restore that
navigation case. Rejecting scoped output on failed audit is preferable to a
separate post-response transaction, an optional write or default-workspace
attribution.

## Authenticated HTTP handoff

Stage an exact dispatcher-relative `POST /api/v1/product-assistant/commands`
route for the existing deterministic navigation and read commands. The JSON
object accepts `command` and an optional `cursor`. It carries no audience,
workspace, subject or request identifier. An invocation appends an audit
event, so use POST under the existing browser write-method guard. See
[RFC 9110 safe methods](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.1)
and the [OWASP CSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html).
Do not put command text in the URL or echo it in a problem response.

Construct `productagent.New` with the same application service used by the
ordinary portal routes in `identity/http.New`, join its error into startup
initialization, and store the resulting adapter on `identity/http.Handler`.
In `identity/http.Handler.api`, dispatch `productagenthttp.Owns(path)` beside
the existing application branch after the shared origin, CSRF and license guards.
Call `Authenticate(ctx, session, audience)` and pass its result and the
existing `requestID` to the adapter. Preserve the standard success and problem
envelopes. The provisional path is global in the HTTP router and the command
still resolves and verifies the session's workspace. The API owner decides
the public route and schema. A cell-qualified route also needs a matching
family in `platform/cellroute`.

Do not add a second login or CSRF path. Use Go's build overlay for the test
until the HTTP owner registers the route. Exercise the real dispatcher with
the shared private-CA TLS fixture, synthetic license fixture and PostgreSQL.
Verify that a forged request ID cannot change audit correlation, both
entrances enforce session audience, scope selectors are refused, a cursor
from another workspace is unusable, and an audit append error releases no
result. Read actor attribution through `ListWorkspaceAuditForActor` and the
opt-in Admin audit HTTP list. Check that the legacy HTTP representation omits
the actor fields, refused requests leave no event and the stored metadata
contains no submitted text.
This route exposes no write command or plan confirmation. No runtime or test
dependency is added. The overlay generator uses the repository's existing
[Python toolchain](https://docs.python.org/3/license.html) under the PSF License,
which permits commercial use, redistribution with notices and offline use.

[PostgreSQL transaction semantics](https://www.postgresql.org/docs/18/tutorial-transactions.html)
support returning the result only after the shared commit. Use a
[deferred constraint trigger](https://www.postgresql.org/docs/18/sql-createtrigger.html)
in the disposable fixture to force a commit failure after the real audit
append and chain update. Verify that the event and chain head both roll back.
Use an immediate refusal as well to exercise the append error itself.
This adds no new dependency or schema migration.

## Acceptance

Extend the existing disconnected PostgreSQL command fixture to invoke actual
navigation and scoped application or conversation reads using real sessions.
Include a non-default workspace. Read the resulting events through the shared
audit projection and verify the assistant actor, distinct user subject, actual
workspace, request correlation and absence of command text and result data.
Exercise a revoked session, a foreign workspace, audit insertion failure and
uncertain commit. Add a confirmed mutation case when a production write
adapter exists and verify the effect and audit event commit together. Verify
new and historical events with the offline verifier.

The disconnected command fixture verifies the stored actor metadata and
distinct subject, request ID, non-default workspace, cross-tenant denial,
rollback and the original portal reads.

`TestAuthenticatedAssistantHTTP` passed in a network-isolated Linux arm64
container with PostgreSQL 18.6 and pgvector 0.8.7. A build-only overlay mounted
the adapter in the real dispatcher. The fixture verified both entrances,
server correlation, the scoped audit reader, fixed metadata, cross-workspace
cursor denial and retry after an audit append refusal. Its Admin HTTP checks
also passed for the default and opt-in audit representations, with no event
visible through the same reader for refused requests. AGT-107 stays open for
production HTTP registration, workspace-free navigation, confirmed writes
and export acceptance.

## AGT-112 audit fixture handoff

The installation policy candidate changes the internal constructor to
`productagent.New(apps *applications.Service, policy productagent.ExecutionPolicy)`.
The policy reads the current local decision from `context.Context`. It returns
`true, nil` only for an explicitly enabled installation. A nil reader or a read
error rejects execution with `SERVICE_NOT_READY`. Explicit `false, nil` rejects
with `ACCESS_DENIED`.

SCI owns the constructor call in
[`runProductAssistantAuditCommands`](../../services/control/internal/modules/audit/assistant_disclosure_integration_test.go#L29).
When integrating the candidate, pass
`func(context.Context) (bool, error) { return true, nil }` as the second argument
in that synthetic fixture. The test already imports `context`. Keep the
fixture and constructor updates in the same integration change. The identity
dispatcher binds the real trusted configuration when it mounts the route.

The [Go build overlay](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies)
replaces only the fixture's legacy constructor line in a temporary file. It
checks that the original line occurs exactly once. No dispatcher, production
source or audit writer changes in this rehearsal. Build the real audit test
and standalone verifier with dependency downloads disabled. Run both with
PostgreSQL 18.6 and pgvector 0.8.7 on a disposable
[Docker none network](https://docs.docker.com/engine/network/drivers/none/).
The container has loopback only and exposes no ports. The verified fixture
exports the mixed V1/V2 chain, discloses the assistant subject and actor,
and rejects tampered records using the standalone verifier and held checkpoint.

The runner adds no product dependency, model or dataset. It uses the existing
[Python standard library](https://docs.python.org/3.12/license.html) under the
PSF license, the [Go toolchain](https://go.dev/LICENSE) under BSD-style terms,
and the existing [Docker Engine](https://github.com/moby/moby/blob/master/LICENSE)
under Apache 2.0. The pinned fixture uses
[PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE) under the
PostgreSQL License. These terms allow commercial use, offline use and
redistribution with the applicable notices. The test uses synthetic rows.

Run the replay with the AGT-112 candidate checked out. The script may be
copied from the APP checkout if the candidate does not yet contain it. Use
the local image and the warmed Go module cache described in the development
environment guide.

```sh
python3 /path/to/replay_audit_fixture.py /path/to/agt-112-candidate-checkout
```

The [replay script](../../services/control/internal/modules/productagent/testdata/replay_audit_fixture.py)
creates and removes its own container and scratch directory. After SCI has
updated the tracked fixture, the regular `TestProductAssistantAuditWithin`
test with the same protected DSN and verifier inputs proves the integrated
tree directly.

The replay passed on the installation policy candidate with PostgreSQL 18.6,
pgvector 0.8.7 and the Linux arm64 verifier. It exercised both real assistant
commands and all ten scope and export tamper subtests with no external route.
