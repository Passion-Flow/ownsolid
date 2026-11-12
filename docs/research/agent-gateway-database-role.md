# Agent gateway database role

Research for ARC-111. Reviewed against the current PostgreSQL 18 documentation
on 2026-11-13. This note defines the acceptance boundary before implementation;
it does not activate a new role, grant, credential or serving mode.

## Upstream approach

[PostgreSQL CREATE ROLE](https://www.postgresql.org/docs/18/sql-createrole.html)
provides a login identity and connection limit independently of any table grant.
The gateway needs its own connection budget so exhausting the session tier cannot
consume control's role limit. A pool limit alone cannot enforce the aggregate
limit across several processes using the same login.

[Role membership](https://www.postgresql.org/docs/18/role-membership.html)
can confer both inherited privileges and permission to switch roles. Merely
setting NOINHERIT does not establish isolation: the current preparation policy
also refuses membership in either direction, ownership, superuser, database/role
creation, replication and bypass-RLS authority. Preserve those restrictions.

[GRANT](https://www.postgresql.org/docs/18/sql-grant.html) supports separate
read and write rights on individual objects. Effective authority includes PUBLIC
and memberships; checking only explicit ACL entries misses inherited grants.
[Security-definer functions](https://www.postgresql.org/docs/18/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY)
need a trusted search path and selective execution privileges. Keep the existing
private audit-chain owner private; a gateway must not read or mutate its head.
Audit-trigger execution is not permission to grant the caller direct head access.

Comparable infrastructure separates component identities from administration:
[Kubernetes etcd operation](https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/)
uses authenticated clients, while [etcd RBAC](https://etcd.io/docs/v3.6/op-guide/authentication/rbac/)
assigns read/write rights to explicit key ranges. These support the principle of
a process-specific principal; ownsolid keeps PostgreSQL table/function grants
rather than introducing another datastore or copying etcd's permission model.

## Existing boundary

[ADR 0048](../decisions/0048-agent-gateways-beside-control.md) serves only the
agent session stream and health routes from the dedicated process. Enrollment,
results, renewals, event batches and file transfers remain on control's machine
entrance. Do not grant their complete write surface merely because the shared
constructor also creates their handlers.

The session backend reaches heartbeat and held claim. Their transitive owners
include node credentials/cursors/tasks, component and inference observations,
training and preparation claims, model/dataset validation, current site/cell and
Workspace status, audit, liveness flush, task wakes, node-view invalidation and
shed policy. Inventory these actual statements, triggers and worker paths before
activating an allowlist. A read-only-node trace alone cannot prove permissions
for delegated claims or background flushes.

`runAgentGateway` currently opens the normal configured serving pool and calls
`database.CheckRuntimeWithin`. That predicate intentionally requires full
business DML. `installationFence.check` calls `CheckRuntimeReady` again after a
new installation-lock holder appears. Changing startup alone would make the
first control restart reject a correctly restricted gateway. Both admission
paths need a dedicated predicate while preserving exact migration/catalog checks
and the current transient-storage versus incompatible-schema distinction.

The runtime-access operator owns protected credentials, installation-lock and
exact target binding, transactional PostgreSQL grants and principal readback.
It must remain the owner of preparing the new role. No gateway process may create
or alter its own login, grants or password. Existing control principal grants,
OID and SCRAM verifier remain unchanged across introduction and retry.

## Chosen approach and rejected alternatives

Use an explicit PostgreSQL gateway principal and separate pool/aggregate limits,
with protected password-file input and the same configured database endpoint,
TLS policy, schema and installed identity. Admit only the closed catalog of
permissions needed by every active session/worker path. Enumerate the required
privileges and reject excess effective rights, unrelated objects, ownership,
membership, DDL, trigger control, delegation and journal writes. Preserve
SELECT-only migration-ledger access and the private audit-head exclusion.

Prototype the permission closure outside product code against the current real
PostgreSQL catalog first. Exercise real heartbeat, claim, observation flush,
reconnect and delegated paths as the restricted principal; use SQLSTATE 42501 as
evidence to review a missing right rather than granting an entire schema. Record
each required table/operation with the owning runtime path and negative tests.
Only enable product composition when that closure holds.

A second login with the same business DML was rejected: it isolates the connection
budget but fails the least-privilege requirement. Membership in the control role
was rejected for the same reason. Search-path substitution, query rewriting and
catch-and-retry with privileged credentials were rejected because they bypass
real authorization. A separate gateway database was rejected for this item;
per-cell metadata topology belongs to ARC-201.

No agent wire change is required. Old v1 agents retain their current stream,
fallback, lease, cursor and retry behavior. Existing gateway configurations
retain the control principal when the new role is omitted. The new principal must
coexist; never revoke the control principal while any gateway or control still
depends on it. A new authority-lock function requires a formally admitted catalog
upgrade: current exact-schema fences do not permit old gateway binaries to ignore
that migration. Upgrade serving binaries/catalog through the existing controlled
procedure before rolling the compatible gateways onto the new principal. Old v1
agents can still coexist; agent wire compatibility is not a promise of arbitrary
serving-binary/database version skew. Installer provisioning and final switch-over
need separate qualified delivery changes, without weakening either role's admission.

## Licenses and offline behavior

No new dependency, model or dataset is proposed. The existing Go toolchain uses
[BSD-3-Clause](https://go.dev/LICENSE), pgx uses
[MIT](https://github.com/jackc/pgx/blob/master/LICENSE), and PostgreSQL uses the
[PostgreSQL License](https://www.postgresql.org/about/licence/). Each permits
commercial and offline use and redistribution with the applicable notices.
Kubernetes/etcd documentation is design evidence; their code is not imported and
no additional etcd process or network service is introduced. Synthetic fixture
identities, credentials and rows must remain outside Git. Runtime preparation,
login, serving and recovery must work without a network route off the site.

## Failure coverage and completion

- Missing/wrong credential, invalid TLS, foreign database/schema or lost
  installation lock refuses preparation without changing grants or either role.
- Repeated preparation preserves both principal identities and credential
  verifiers. Interrupted work is read back before any deliberate retry.
- A gateway can heartbeat, claim, flush observations and recover after a control
  restart; schema mismatch and an unavailable database retain distinct verdicts.
- Every current delegated profile works through its real owner. Cross-node,
  cross-Workspace, disabled-node and stale-lease requests remain refused.
- Gateway sessions cannot change human identity/membership, licensing or unrelated
  application state, mutate the schema journal, truncate, disable triggers,
  inspect the audit head or switch to the control/operator role.
- Exhaust the gateway's real aggregate connection limit while control continues to
  obtain a connection; each pool and worker releases its owned connections on
  failure and shutdown. Revoke a required grant and add an excess grant separately;
  startup and post-restart readiness must both refuse.
- Finish with the installed two-gateway simulator flow, actual privilege readback,
  disconnected operation and the complete repository gate. Any capacity statement
  additionally needs the corresponding measured simulator workload.

ARC-111 stays unticked until the complete permission closure, operator preparation,
serving/readiness, delivery path and installed flow have all passed. No reduced
role or successful installed result is claimed by this research note.

## Row-lock privilege boundary

The existing PostgreSQL node credential path takes FOR SHARE on both
installation_state and tenants before locking its node. A proposed SELECT-only
grant on these authority tables therefore cannot run that path. PostgreSQL's
[SELECT privilege rules](https://www.postgresql.org/docs/18/sql-select.html)
require UPDATE on at least one column of each relation explicitly row-locked,
including FOR SHARE and FOR KEY SHARE. This is separate from ordinary reads.

Granting UPDATE on an arbitrary authority column solely to obtain the lock can
also grant real mutation authority. Do not select an apparently harmless column
without proving the invariant, and do not remove the existing lock to fit a role.
The [row-security documentation](https://www.postgresql.org/docs/18/ddl-rowsecurity.html)
describes a security-definer function as one way to acquire protected row locks
without granting callers direct UPDATE rights. Prototype that narrow alternative
outside product code first: fixed trusted table references/search path, closed
execution grants, original lock lifetime, foreign-scope refusal, writer blocking
and transaction rollback. Do not activate a schema function or new grant catalog
until the complete session/worker closure and its current migration admission are
proven. This discovery does not change current runtime rights or claim ARC-111 done.

## Native row-lock prototype

A separate synthetic PostgreSQL 18.6 Linux amd64 fixture passed 14 checks with no
external network or published host port. The owned container was limited to two
CPUs and 512 MiB, used the retained pgvector image
sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47,
and was removed with its anonymous volume and protected password file afterward.
Admission retained both regular-file resource locks and the unchanged thresholds;
MemFree was 4471828 KiB, MemAvailable 19971884 KiB and full I/O PSI avg10 was 0.
The prototype source SHA-256 is
0d5e474757acd9ab5332bb8ccc66b8815b0a6d803bf8cfd12b22a1920a5c952f.

The real SQL results establish the following narrower mechanism:

- A role with SELECT alone receives 42501 on the existing two-table FOR SHARE shape.
- UPDATE(id) permits that lock but also permits changing an otherwise unreferenced
  tenant identity. Refusing status updates does not make the identity grant harmless.
- After revoking that column grant, a SQL security-definer function with a parsed
  atomic body and trusted search path obtains the original shared locks. Direct
  table locking still receives 42501 and neither authority table has any remaining
  direct UPDATE right for the caller.
- Function execution is revoked from PUBLIC and granted only to the fixture gateway
  role. A second role receives 42501. An unmatched tenant returns false; temporary
  tables named tenants and installation_state do not shadow the bound references.
- In a separate session, writes to both protected rows time out with 55P03 at a
  150ms lock timeout while the caller's transaction remains open. Rolling back the
  caller releases those locks. A subsequent tenant disable succeeds and the next
  function call refuses that inactive installation.

The SELECT-only refusal log SHA-256 is
71ba07118a5d784aa881a0d002d8299ae73d6a99a181d2c56453861e08188f9f.
The tenant and installation writer-fence logs are
a70e3519c9f1acde240a31b764704d01ccdf26e16f0f1210bb523e54bec6c5ab
and 4ba0e7b5f2fc683d06458806de9eeb171fb16c1d7ef932639803f96b113367d7.
The temporary-shadow and no-direct-UPDATE logs are
2444da6458750a371aa6325b06c65c9ecf5311e5594627791b7124604ecb7fbb
and 016b329ada15887b9ad95aa2da3167ba0988a6b8fc8e11d40204b499f7d06bec.

This is an actual PostgreSQL permission/concurrency prototype using SET ROLE
inside a disposable local administrator session. It does not establish separate
SCRAM/TLS login, the product catalog, the complete profile permission closure,
control/gateway startup or installed delivery. No function or role was activated
in ownsolid and no production migration number was taken. Preserve the current
runtime path until all of those acceptance gates pass. ARC-111 remains open.

## Product-schema prototype boundary

The product-schema slice exercises the actual read-only Node heartbeat and claim
against a disposable current product catalog. Keep the candidate SQL function and Go source overlay
outside product files. Reuse the exact unmodified node credential, Workspace,
lease, cursor and audit logic; substitute only the protected installation/tenant
shared-lock query proved above. Connect a new LOGIN principal through real local
TCP/SCRAM, with a protected generated password and a separate bounded pool.
Require the selected store and identity service to use that same restricted pool.

Begin with explicit per-table read/write grants reviewed from the existing
read-only path. A missing permission fails the prototype with its SQLSTATE and
object name; do not automatically grant an entire schema or retry as an operator.
Exercise current heartbeat persistence, idle/issued claims, foreign credentials,
acknowledged cursor and audit behavior. Independently deny direct authority
mutation, human identity/membership access, audit-head reads and migration-ledger
writes. Revoke function execution and require the current authority path to fail.
Do not expose the candidate function before closing PUBLIC execution.

This slice cannot qualify component/inference observations, delegated training or
preparation, gateway background workers, normal server readiness or installer
provisioning. An extra candidate function deliberately changes the fixture catalog;
it must not be mistaken for an activated, normally admitted product catalog.
No production SQL, grant, protocol, migration number or default is changed by the
prototype. Finish the complete permission closure before implementing ARC-111.

## Actual product Node-path prototype

The external overlay now passed the real Node service against PostgreSQL 18.6 on
Linux amd64. The verified baseline was
149a95013db6c6d1ca9dbadd09b05fff9b4c98de; its Node package, migrations,
identity fixture, database platform and agent contract match the current product
inputs. Only the installation/tenant shared-lock CTE was replaced by the narrow
prototype function. Credential checks, node locking, Workspace status, task
leases, acknowledged cursors and audit execution remained the actual product
code. This is a source overlay outside the repository, not a serving change.

The disposable database migrated to schema 60. The raw pg_tables count was 140,
including 20 partition children; the catalog has 120 logical table roots. The
fixture explicitly installed the already approved public vector extension 0.8.7
before migration. An earlier attempt omitted that installer precondition and
failed with KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED before any business
migration or restricted-role flow. Its exit was 1 and its test-log SHA-256 is
402ca2123d05e401f45dc058f435fb0a07dd20365e6004eb0f28b936598925fa.
It is not a successful permission test. The corrected attempt changed only that
fixture precondition; its overlay, grants and assertions were unchanged.

The successful fixture created a separate LOGIN principal with NOINHERIT,
NOSUPERUSER, NOCREATEDB, NOCREATEROLE, NOREPLICATION, NOBYPASSRLS and connection
limit 2. Host authentication required SCRAM-SHA-256. An independent TCP connection
verified both current_user and session_user as that principal, with a SCRAM
verifier read back as a boolean. Generated credentials remained in memory or
protected files and were never included in logs or command-line values. The
Node, identity and Workspace services all used the same restricted pool; there
was no privileged fallback. The reviewed grants for this read-only-node slice were:

| Object | Right | Actual path |
| --- | --- | --- |
| installation_state, tenants, workspaces, sites, cells, agent_pacing, schema_migrations | SELECT | Installed identity, active scope, pacing and ledger reads |
| managed_nodes, node_tasks | SELECT, UPDATE | Credential/heartbeat state, claims and acknowledgement |
| node_task_history | SELECT, INSERT | Task state/history trigger path |
| audit_events | INSERT | Existing private-owner audit trigger |
| Owned fixture schema | USAGE | Qualified runtime objects |
| Prototype authority-lock function | EXECUTE | Original installation/tenant shared-lock fence |

This list is observed prototype input, not the complete gateway grant catalog.
The function body bound qualified relations at creation, fixed its search path,
and closed PUBLIC execution transactionally before exposing the caller grant.
The restricted real service then:

- Committed a heartbeat whose persisted timestamp matched the returned server time.
- Refused a different enrolled node's credential with AGENT_UNAUTHORIZED.
- Claimed an actual collect_facts.v1 task on attempt one and acknowledged its exact
  sequence; persisted attempt, acknowledged attempt and node cursor matched.
- Received SQLSTATE 42501 for direct installation and tenant mutation, human
  identity and membership reads, private audit-head reads and journal writes.
- Failed the next real claim closed, without a lease, after EXECUTE was revoked;
  the traced seventh permission refusal was 42501.

TestRestrictedReadOnlyGatewayAuthorityPrototype passed in 1.79 seconds with no
skips. The owned network-none container published no host port and used two CPUs,
1 GiB and the same pinned image as the synthetic prototype. Both resource locks
were held; MemFree was 4102932 KiB, MemAvailable 20105108 KiB and full I/O PSI
avg10 was 0.03. The container, anonymous volume, database input and password file
were removed. The external input archive was 5271 bytes with SHA-256
1201d518a9cecc3ff40fa5acd2bb506a6c9248f9a25a856a540099a7af0318dd;
the actual test-log SHA-256 is
5e3a67f7b9d82920d31e7133db8a547901536add97f3aa9e114e0f61fbbccf40.

This proves independent local SCRAM login and the actual read-only Node SQL path.
It does not prove TLS, aggregate connection exhaustion, component/inference or
delegated profiles, background workers, normal catalog/readiness admission or an
installed gateway. The candidate function intentionally changes the disposable
catalog; no migration number, runtime grant, startup behavior or delivery default
was activated. ARC-111 remains unticked while those acceptance gates remain open.

## Observation and worker prototype acceptance

Extend the same external fixture through the production NewAgentGateway
composition. Use its actual component and deployment observers, restricted pool
and read-only outbox reader, without changing those owners. Review the additional
managed_components and inference_nodes SELECT/INSERT/UPDATE operations from their
actual PostgreSQL upserts and batched sighting updates; control_outbox needs SELECT
for RunWakes, not INSERT or DELETE. Shed refresh only reads agent_pacing in this
process because no host operator socket is installed here.

For each observation family, require the missing table privilege to abort the
whole heartbeat, preserving the node timestamp and observation absence. After an
explicit reviewed fixture grant, verify first/update reports, exact persisted
scope and timestamp, foreign-credential refusal and unchanged state after denial.
Enable liveness, repeat reports and flush through the same restricted pool; verify
the node and owner batch timestamps. Run and join the real outbox worker, and
verify a task committed by the control-side fixture wakes a held restricted claim.
These are permission/transaction checks, not a load claim or hardware execution.

Independently occupy both connections permitted to the LOGIN role, require a third
real connection to fail with SQLSTATE 53300, and require the control-side fixture
pool to remain usable. Release every acquired connection before closing either
pool. Keep the role's configured connection limit at two; do not change the limit
in response to a failure. Deployment-bearing observations and delegated task
profiles remain separate acceptance cases even if hardware-only reports pass.

## Qualified composed observations and workers

The extended external prototype used the actual NewAgentGateway composition;
its component/deployment observers, identity and Workspace services shared the
restricted LOGIN pool. Its unchanged product sources matched the same verified
149a95013db6c6d1ca9dbadd09b05fff9b4c98de baseline. The only product-source
overlay remained the protected installation/tenant lock CTE. No test supplied a
replacement observer, database implementation or privileged retry.

TestRestrictedComposedGatewayAuthorityPrototype passed on native Linux amd64 in
1.95 seconds, without skips. The actual results were:

- With two gateway-role connections occupied, a third independent TCP connection
  failed with 53300. The control-side fixture pool successfully pinged while both
  gateway connections were still occupied. The same role limit remained two.
- A systemd_components report without managed_components rights failed with 42501;
  a docker_inference hardware report without inference_nodes rights did likewise.
  Both failures rolled back the node timestamp and persisted no owner row.
- Explicit SELECT/INSERT/UPDATE grants on those two owner tables admitted first and
  updated reports through their real owners. Readback checked complete report JSON,
  tenant/Workspace/site/cell equality and the returned committed heartbeat time.
  Foreign-node credentials were refused and left the stored node time unchanged.
- EnableLiveness, repeated unchanged heartbeats and FlushLiveness ran on that same
  pool. Both repeat heartbeats left the synchronous node timestamp untouched before
  flushing; the subsequent actual node and owner batch timestamps matched each
  repeat's returned server time.
- SELECT on control_outbox admitted the actual RunWakes reader. INSERT and DELETE
  each failed with 42501. A held restricted claim acquired no database connection
  while waiting, received a task committed by the control-side service and then
  acknowledged it. The outbox worker reported zero read failures and was joined.
- The earlier authority mutation, private/human reads, migration-journal and
  revoked-function cases remained enforced. In total the trace recorded eleven
  expected query-level 42501 refusals, in addition to the independent 53300 login.

This fixture used schema 60 / 120 logical roots, PostgreSQL 18.6 and vector 0.8.7,
the retained pinned image, network=none, no published host port, two CPUs and
1 GiB. Two admission attempts returned 75 before compilation because free memory
was below the unchanged 2 GiB threshold. The admitted attempt held both resource
locks with MemFree 3868256 KiB, MemAvailable 20906912 KiB and full I/O PSI avg10
0.11. The owned container and protected database/password inputs were confirmed
absent after cleanup; logs and the external overlay remain retained for review.
The 7805-byte input archive SHA-256 is
eac225cd29a957cf8afcb525b3255476ead4316d16b190575568a8781f8b2d87;
the actual test-log SHA-256 is
9c585726809071f3a459616a966cd0c679e8288678857a97ea573f6d3aa7defc.

Hardware-only inference observation does not exercise a deployment-bearing report.
Likewise, composition constructs the training/preparation owners but this test
claims only read-only facts tasks. It does not qualify those delegated paths,
replicated liveness, TLS, shed refresh, normal startup/catalog admission or
installer provisioning. The independent connection-limit proof applies to this
restricted fixture role, not to a deployed control/gateway resource budget. No
production function, grant, credential or migration number was activated.

## Verified TLS principal prototype acceptance

The next external role fixture retains the composed observation, liveness, wake
and connection-limit assertions and changes only the protected database transport.
[PostgreSQL server SSL](https://www.postgresql.org/docs/18/ssl-tcp.html) requires
an actual certificate/key pair and protected key permissions. Merely enabling SSL
still allows plaintext unless the authentication policy refuses it. Use a
fixture-owned hostssl/SCRAM rule and an explicit hostnossl rejection, in a
network-none container with no published port.

[Client SSL verification](https://www.postgresql.org/docs/18/libpq-ssl.html)
distinguishes encryption, chain trust and endpoint-name validation. Select an
explicit verify-full DSN with the protected fixture CA, retaining pgx's actual
TLS verifier. Require no transport fallback and no InsecureSkipVerify; observe
TLS on the independently logged-in role's own pg_stat_ssl row. Use generated
synthetic local certificate material, never a production CA or signing key.

Independently require an untrusted CA and a wrong certificate reference name to
fail normal certificate verification, the wrong password to fail with 28P01,
and plaintext to fail with 28000. Correct credentials must still execute the
unchanged product Node/observer/worker slice over verified TLS. Keep all existing
42501 cases and the aggregate 53300 overflow. No automatic retry with operator
credentials, weaker SSL mode or broader grants is permitted. Remove the generated
private key, password and protected database input with the owned container.
This is database transport/role evidence only; delegated profiles and product
startup/delivery remain separate open gates.


## TLS certificate algorithm and external runner boundaries

The first actual TLS prototype reached PostgreSQL 18.6 with its generated
Ed25519 certificate and failed before fixture bootstrap with SQLSTATE 28000,
SCRAM channel binding negotiation error. Its material-generation test passed;
the product SQL/role assertions did not run. The retained failed input is
9307 bytes, SHA-256
d59611dca09c6240092bcbcb43aa01ce40e7b7edc5471c9beb89c332de43f95f.
The generated private key, password, database input and container were removed.
This failure is not qualification of the TLS path or an Ed25519 support claim.

The pinned [pgx v5.11.0 SCRAM implementation](https://github.com/jackc/pgx/blob/v5.11.0/pgconn/auth_scram.go)
uses the certificate signature hash required by
[RFC 5929 section 4.1](https://www.rfc-editor.org/rfc/rfc5929.html#section-4.1).
Its hash switch implements ECDSA/SHA-256, SHA-384 and SHA-512 and the listed RSA
variants, but not Ed25519. In the default prefer mode the missing binding data
falls back to a SCRAM client header that
[PostgreSQL rejects when TLS channel binding is available](https://doxygen.postgresql.org/auth-scram_8c_source.html).
Choose a newly generated ECDSA P-256/SHA-256 certificate for the next external
fixture and require channel_binding=require as well as verify-full. Verify the
actual certificate signature and parsed connection policy in the test. Do not
disable channel binding, TLS verification or server plaintext refusal. Preserve
all four independent TLS/authentication refusals, eleven query permission
refusals and the role connection overflow. Ed25519 database-certificate
compatibility remains a known open boundary rather than being relabeled.

The external Python admission driver also exposed a test-harness namespace
failure: a sibling queue.py could shadow the standard-library queue import and
recursively start the runner. All identified owned runner processes were stopped
by exact path, PID start time and command digest; foreign processes were left
untouched. The later cleanup readback contained zero matching owned processes.
Retain the failed logs. Use a responsibility-specific driver filename and
[Python isolated mode](https://docs.python.org/3.12/using/cmdline.html#cmdoption-I),
which excludes the script directory from module lookup, for admission and job
processes. This change is outside product code and grants no product acceptance.
Neither the resource thresholds nor the shared lock identities may change to
admit a retry. No new dependency, model or dataset is introduced.


The ECDSA retry compiled and generated the expected certificate, but its external
readiness probe reached the image's temporary Unix-socket bootstrap server before
the target database was created. Extension setup failed before product tests ran.
[pg_isready](https://www.postgresql.org/docs/18/app-pg-isready.html) reports server
availability without proving authentication or database existence; it is not the
acceptance predicate. Require an actual TCP SELECT current_database() returning
the exact owned database, with verify-full and required channel binding, before
extension preparation. Use a mode-0600
[password file](https://www.postgresql.org/docs/18/libpq-pgpass.html), never a
password in a command or environment, and remove it in unconditional cleanup.
Bound only this startup availability polling; do not retry a failed product test
or weaken its SQL, authentication or certificate assertions.
