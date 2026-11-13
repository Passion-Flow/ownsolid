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


## Qualified verified TLS composition

TestRestrictedTLSComposedGatewayAuthorityPrototype passed on native Linux amd64
in 2.11 seconds, without skips. The production sources and narrow external Node
CTE overlay remained those of the earlier verified 149a9501 composed prototype.
Only fixture database transport and its generated certificate material changed;
no Node, observer, Workspace, identity or worker implementation was substituted.

The independent restricted LOGIN negotiated TLSv1.3. Its parsed policy retained
verify-full, a real trust pool, required SCRAM channel binding, no fallback and no
InsecureSkipVerify. The same connection's pg_stat_ssl row confirmed encryption.
The actual generated certificate was ECDSA P-256/SHA-256. Independent connections
with an untrusted CA and wrong reference name failed normal x509 chain/name
verification; wrong-password and plaintext attempts failed with 28P01 and 28000.

The same root retained and passed all eleven expected SQLSTATE 42501 query
refusals, actual two-connection occupation and third-login 53300 refusal while
control remained usable. It then passed real first/update component and hardware
inference reports, missing-grant rollback, foreign-credential refusal, two
liveness batches, zero-connection held claim, read-only outbox wake and exact
acknowledgement/cursor persistence. No assertion or declared resource bound was
relaxed. This remains a limited actual product path with synthetic facts, not
hardware, deployment-bearing observation or complete delegated-profile proof.

The owned PostgreSQL 18.6/vector 0.8.7 container used the same pinned image,
network=none, no published port, two CPUs and 1 GiB. The admitted run held both
resource locks; MemFree was 21389960 KiB, MemAvailable 22403392 KiB and full I/O
PSI avg10 was 3.02. Actual target-database readiness used a verified TCP SQL query
and protected password file before extension preparation. The container, volume,
private key, password, DSN and readiness password file were confirmed removed.

The 9328-byte overlay input SHA-256 is
156bb548141e631558f4ab6bb7342c87c1a9c31f3fa241aaadd448c7a39b7fa5;
the test source SHA-256 is
d59722f9cfa0f5526ac806b5c6e4549ea8117d29fb13d2fc085bd2c0699f8d45.
The corrected external runner SHA-256 is
9f3337c9b9a6eeed3a108f2a9ab49fa8010e3b7f6fd9b9db7e501a8aa9037a7d;
the actual SQL test-log SHA-256 is
02a495e21982421c0bcb77d9d0766429f33a3f42812c3f29cff038ad97644125.

This qualifies the ECDSA verified-TLS/SCRAM transport of the existing composed
prototype. It does not cure the retained Ed25519 compatibility failure or activate
production grants, a role, catalog functions, startup admission or installer
provisioning. ARC-111 remains unticked; the next acceptance slice is delegated
training/preparation authority with closed column privileges, before product-role
catalog and operator preparation work.


## Delegated session claims and column privilege acceptance

The next role slice must follow the actual session graph rather than grant every
machine HTTP mutation to a gateway. Agent protocol v1 carries heartbeats, claims
and acknowledgements through the session; training/preparation uploads and result
HTTP traffic still use their existing control-owned HTTPS paths. Do not broaden
this role to their whole serving role merely because the constructor builds those
owners. Renewals can be separately verified as a reusable owner path, without
claiming that this tier currently routes their HTTP requests.

Current training and preparation delegated loaders read users(id, auth_epoch,
status, is_platform_admin) and workspace_memberships(tenant_id, workspace_id,
user_id, role, authority_id). On PostgreSQL those human authority reads retain
the existing node-mediated revocation fence without locking shared human rows.
The [column privilege contract](https://www.postgresql.org/docs/18/sql-grant.html)
permits these reads without granting passwords, email, identity writes or whole
membership tables. SELECT 1 is not a sufficient negative test after column grants:
explicitly read an ungranted human field and separately verify forbidden writes.

Prototype the reviewed grants against current verified source, using actual
control-side dataset preparation/job dispatch and NewAgentGateway over an
independent restricted LOGIN pool. Keep dataset file verification outside business
locks and retain both delegated authority checks. For a real training claim,
verify the exact attempt, task sequence, stop-token binding, acknowledgement and
held GPU ownership; a lease response alone is insufficient. Missing column or
attempt mutation privileges must fail without a delivered lease or partial task
mutation. Then grant only the reviewed fields/operations and rerun a fresh owned
scenario. Never add privileges dynamically after catching a product SQL error.

Independently revoke the requester's epoch and membership authority, disable the
Workspace/node and alter the frozen command. Each case must preserve physical
ownership while refusing the next claim, with no operator-pool retry. An approved
repair may admit a fresh actual retry where the existing business contract allows
it; it must not change the same attempt after acknowledgement. Preparation claims
need their own persisted authority and exact-attempt checks rather than inheriting
training's acceptance. Keep existing control workers, source artifact ownership,
scope fences and read-only/observation role cases unchanged. These are acceptance
requirements; no delegated-profile role run is claimed yet.


## Delegated claim artifact-store precondition

Before measuring grants, isolate a current constructor precondition with the
normal control-side principal. The current NewAgentGateway graph constructs a
dataset Service without a Store, and runAgentGateway does not open it afterward.
Training ClaimTraining calls VerifyTrainingInputSnapshot between its two
transactions; that method explicitly refuses a nil Store. This is a static gap,
not yet a recorded runtime reproduction. Broader SQL privileges cannot supply a
missing filesystem owner. Do not patch the identity/dataset owners from this lane
or insert a synthetic successful file verifier into the role prototype.

The next external diagnostic must prepare a real synthetic JSONL dataset through
the existing CPython processor, dispatch a real control-side training attempt,
then call the unchanged NewAgentGateway/Node claim graph against that same actual
database. Observe the exact error and complete task/attempt/physical-lease state
before and after. Independently require control's normal file-owning path to claim
and acknowledge that same attempt successfully, retaining the exact task sequence
and physical ownership. A diagnostic that confirms an undesired failure is not
acceptance of delegated gateway support. Keep this composition question separate
from the restricted role's still-open permission closure.

The available Linux CPython 3.12.3 satisfies the data processor's >=3.12,<3.13
contract; the JSONL path has no third-party Python dependencies and uses generated
synthetic records. Python's [PSF-2.0 terms](https://docs.python.org/3.12/license.html)
permit commercial/offline use and redistribution with notices. No model weights
or external dataset are needed. To keep real processing available while denying
network egress, run the test in the owned PostgreSQL container's network namespace
while retaining the host filesystem and interpreter. The upstream
[nsenter contract](https://man7.org/linux/man-pages/man1/nsenter.1.html) separates
network namespace entry from mount namespace entry. Validate the owned container
identity and network=none before entry; never enter a foreign namespace. util-linux
[nsenter 2.39.3 source](https://github.com/util-linux/util-linux/blob/v2.39.3/sys-utils/nsenter.c)
is GPL-2.0-only; it is an existing external test-host tool, not linked into or
redistributed with the product by this diagnostic. Retain all prior dependency
licence obligations. Use the same pinned PostgreSQL image, generated ECDSA trust,
verify-full, required SCRAM binding, bounded admission and unconditional cleanup.


The first artifact-owner diagnostic did not reach the claim: the dataset processor
correctly rejected the retained Git-archive carrier's group-writable process.py
(0664) and immediate directory (0775). The CPython executable was canonical,
root-owned and 0755. Keep the trustedProgram guard and failed log; this is not a
runtime reproduction of the missing-store hypothesis. The retry must copy only
the verified source into its own disposable tree, preserving every byte and
executable flag while restricting regular files/directories to ordinary 0644/0755
checkout modes. Do not chmod the immutable retained baseline, weaken source trust
or place processing code inside writable dataset storage. The processor retains
its existing isolated -I/-B execution and all resource/file checks.


## Reproduced gateway training claim failure

The protected-source retry confirmed the constructor gap on current verified
274d771c product sources. TestObserveGatewayTrainingClaimArtifactOwnership
completed on native Linux in 2.25 seconds without skips. This diagnostic passed
by observing a known undesired failure; it does not qualify training through the
gateway or the restricted database role.

The existing real CPython processor inspected/published generated JSONL, and the
control-side fixture dispatched an actual training attempt and held its physical
GPU reservation. Unchanged NewAgentGateway constructed its training dataset
service with Store=nil despite configured artifact roots. The same control-side
fixture principal then received DATASET_STORAGE_UNAVAILABLE from that gateway's
Node claim graph, with no lease. Full checked task state/attempt/acknowledgement,
lease/stop-token presence, physical reservation and node acknowledgement cursor
were unchanged. Control's actual file-owning path immediately claimed and
acknowledged the same task/attempt on attempt one, retaining physical ownership
and the exact delivered sequence. No training image, model weights or GPU process
was run. Synthetic model metadata does not constitute model execution evidence.

The first nil-store refusal already occurs in PublishedSourceWithin, before its
metadata read; VerifyTrainingInputSnapshot independently requires the real Store
as well. runAgentGateway does not open either store after construction. Broader
SQL grants cannot repair this path. Training claims through a separate gateway
are not supported until the actual file-owner composition is fixed and verified.
Control-side training and the previously measured read-only gateway path are
separate capabilities. Do not add a privileged retry, fabricate file verification
or deliver the generic training lease without its delegated authority checks.

The diagnostic reverified all 5040 source paths and copied unchanged bytes into
its own 0644/0755 checkout before compilation. It entered only the owned
network-none PostgreSQL container's network namespace, checking its identity and
namespace in the test. CPython 3.12.3 and util-linux nsenter 2.39.3 remained on the
host filesystem; no host port or external service was exposed. Database access
retained generated ECDSA trust, verify-full and required SCRAM binding. The run
held both resource locks with MemFree 19931192 KiB, MemAvailable 22363188 KiB and
full I/O PSI avg10 1.2. The owned container/volume, private key, DSN, password and
readiness password were confirmed removed.

The 1791-byte external diagnostic archive SHA-256 is
07e0a60271977cc2412025d3a557f22b671ea076a226b44cd0ba4bb2275d0b0c;
the corrected runner SHA-256 is
554b2a5d7a07f5126464088b9e9d2195ff19e284381945c93c63fe0f1fb5f9c7.
The actual test-log SHA-256 is
69638e0915df065f9d8aee946340db86e1ce0456045089bc2be6a655e2b05235.

The responsible identity/dataset composition is outside this lane's allowed
business modules. Keep this boundary explicit and take the next owned item;
there is no request for the owner to answer and no product-role activation.
Any later permission-only experiment that borrows a real existing Store must
state that fixture dependency and must not claim the unchanged gateway constructor
passed. It may not replace the Store with a success callback.

The permission review also finds FOR UPDATE on the complete training job and on
published dataset/version rows. PostgreSQL requires UPDATE rights for those locks
as it does for the installation lock. Granting arbitrary id updates or dropping
the locks would repeat the already rejected authority-lock mistake. Their owners
must preserve retirement/current-attempt fencing through a closed protected
interface before claiming a fully narrow gateway grant catalog. Column SELECT on
human authority fields alone is therefore insufficient to close ARC-111.


## Concrete reader-constructor dependency and process ownership

The real construction owner is IAM's identity/http/agent_path.go; ARC owns
cmd/server/agent_gateway.go and runtime_lifecycle.go. TRN owns the two concrete
PublishedReader types and their namespace/file behavior. Its candidate and native
qualification must be explicitly admitted; an unpublished API or a source-only
unit result is not a ready runtime dependency. The formal family lineage has its
own [source/metadata qualification](model-family-schema-admission.md).

The following is an interface proposal for IAM, not a declaration that exists or
an interface ARC will guess at a call site. Keep the current two-argument
NewAgentGateway and Control writer construction compatible; an additive entry
can accept the already opened concrete readers:

```go
type AgentGatewayPublishedReaders struct {
    Datasets *datasets.PublishedReader
    Training *training.PublishedReader
}

func NewAgentGatewayWithPublishedReaders(
    s *identity.Service,
    ready func() error,
    readers AgentGatewayPublishedReaders,
) (*AgentGateway, error)
```

The original owner must bind these to the actual graph it constructs: the same
dataset service must be shared by training and preparation, and the training
reader must reach accepted checkpoint/adapter reads. All gateway Store fields
stay nil; Control keeps its existing writer owners. Missing configured readers
or a conflicting writer/reader must refuse, never silently select a side graph.
The constructor borrows these owners; it must not initialize directories, take a
writer lock, start a processor or hide a construction error. Zero/closed/foreign
readers must fail through the real admitted owner contract; if startup needs an
additional namespace-health operation, that concrete method remains TRN-owned.
Do not reflect into private reader state or type-assert Nodes.Training to alter
someone else's services. IAM supplies the final compiled signature, graph tests,
error/ownership contract and exact source before ARC consumes it.

ARC's caller will read the installed tenant from its authenticated selected
store, open only c.Datasets.ArtifactRoot and c.Training.ArtifactRoot through the
admitted owner APIs, and transfer no descriptor ownership to a request. A failed
second open or constructor must close the first open and retain any close error.
Configured namespaces must already exist; no new-writer/create fallback or
client-selected tenant/path is permitted. Register process-owned closers only
once and close in reverse order after admission stops, requests/streams and
workers have actually joined. Files returned to an admitted handler remain that
handler's responsibility until its own Close.

## Join the existing cancellation drain

The current runAgentGateway schedules Sessions.Drain with context.AfterFunc and
only defers its stop function. [Go1.27.3 AfterFunc](https://pkg.go.dev/context@go1.27.3#AfterFunc)
specifies that stopping the association does not wait for an already started
callback. Therefore the process lifecycle cannot treat that callback as joined.
This part is implementable in ARC without a reader API: replace the untracked
callback with the existing lifecycle's Loop waiting for cancellation and then
calling the same real Sessions.Drain. The existing worker wait then covers drain
before closers, including normal cancellation and early listener return. No new
public interface, generic wrapper, alternate gateway graph or test-only success
path is needed.

This only joins the drain operation. Sessions.Drain cancels sessions; it is not
itself a request/stream/file-owner join. The current aged listener joins its
connection workers, but a timed-out net/http Shutdown can close connections
without proving that an uncooperative handler returned. The [HTTP shutdown
contract](https://pkg.go.dev/net/http@go1.27.3#Server.Shutdown) requires preserving
that distinction. Reader integration must retain confirmed handler/stream joins
or return an unconfirmed shutdown without closing owners still in use; attaching
readers now would conceal that open boundary.

Reuse TestRuntimeNormalCancellationAndReverseOwnerJoin,
TestRuntimeTimeoutRetainsLeaseUntilActualJoin,
TestRuntimeUnconfirmedOwnerStopNeverClaimsJoin and the actual Gateway drain tests
for the affected lifecycle primitives, then the real composed server shutdown,
partial-start failure, retry/restart and native/offline profiles. Compilation,
race, real claims/SQL and installed delivery remain separate required evidence.
No dependency, model or dataset is added; Go remains BSD-3-Clause with its
existing commercial redistribution/offline terms and notices. No runtime
networking or protocol field changes are introduced by joining this callback.

The joined-drain increment is source preparation only: formatting, layout,
roadmap consistency and diff checks ran, but no compiler, unit/race test, native
SQL or full gate ran for it. Its focused existing-entry checks, when separately
allocated, are:

```sh
go -C services/control test -mod=readonly -p 1 -count=1 ./cmd/server -run '^TestRuntime(NormalCancellationAndReverseOwnerJoin|TimeoutRetainsLeaseUntilActualJoin|UnconfirmedOwnerStopNeverClaimsJoin)$'
go -C services/control test -mod=readonly -p 1 -count=1 ./internal/platform/agentgateway -run '^TestDrainEndsSessionsAndRefusesNewOnes$'
```

These primitive checks cannot replace real composed shutdown and file ownership
acceptance. The database-focused 1,200-second allocation does not also authorize
these extra profiles, the installed500-agent drill or a whole-repository gate.

## Original TRN input and complete request ownership

The integration tree merges original e639db9035b0b2c0229c90175493c605f328e295
with baa584c3. All 21 owning dataset/training module paths, including README/test
inputs, compare byte-for-byte to the original author; both research records are
preserved. The owner's reported native v3 qualification is two packages/two roots
and required independent-reader cases, zero skips/failures in 138.486532 s. That
is author evidence for e639db90, not a run of this combined schema/process tree.
No reader graph binding is inferred from importing its source.

The pinned gRPC-Go v1.85.0 source shows why HTTP-handler counting alone is
insufficient. Server.ServeHTTP starts RPC work through serveStreams; its handler
transport can return after request cancellation while the application RPC callback
is still unwinding. See [Server](https://github.com/grpc/grpc-go/blob/v1.85.0/server.go)
and [handler transport](https://github.com/grpc/grpc-go/blob/v1.85.0/internal/transport/handler_server.go).
Track the entire original Gateway.connect callback, from before peer/backend
admission until all its scoped defers finish. Drain seals registration under the
same mutex, and the last callback signals completion. Keep Drain nonblocking for
existing callers; add a context-bounded DrainAndWait for process ownership. No
wire/version change or old-agent upgrade is needed.

The actual dedicated HTTP handler additionally needs a zero-value usable request
owner. Admission and shutdown sealing use one mutex, so no request can register
after a successful join. A sealed handler returns the existing SERVICE_NOT_READY
without consulting a file or SQL owner. Register this join and the real RPC join
with the existing runtime lifecycle before serving, and seal/drain on its tracked
cancellation loop. Move the already opened database pool into that lifecycle as
its first closer, so it closes last and is retained on an unconfirmed request
join. Startup failures still close it through the same lifecycle. Reader closers
can later share this ordering when IAM supplies its real graph binding.

Use channels rather than a goroutine per waiting caller. A canceled join reports
its original context error and does not declare completion; a later explicit
join can observe actual completion. Cover active HTTP work, an RPC callback still
running after its HTTP transport ends, refusal after seal, concurrent drain/join,
panic/deferred release and startup/timeout owner retention. Keep real protocol
and file/SQL composition acceptance separate from isolated lifetime tests.
The source may implement these existing boundaries without constructing a second
reader graph, opening an unbound reader and returning success, or calling an IAM
interface that does not yet exist. Same-host reader paths remain the existing
Dataset/Training artifact_root values and the tenant from installedTenant on the
original selected store; initialization/Close must remain owner APIs.

No dependency is added. gRPC-Go remains Apache-2.0 and Go remains BSD-3-Clause;
commercial/offline redistribution retains their original notices. Runtime
filesystem and network scope do not grow. This preparation is not a native,
compiler or product qualification; all original checks and resource allocation
requirements remain in force.

The concrete source increment now registers HTTP and RPC joins in runAgentGateway,
retains the real pool as the last lifecycle closer and exposes DrainAndWait from
the existing Gateway. Prepared isolated tests use real HTTPS/file descriptors
for request-owner retention and the existing unit backend fixture for a callback
that remains active after cancellation/transport close. They exercise refusal,
concurrent joins and delayed actual file close; they establish no SQL or training
authorization evidence. Formatting/layout/roadmap/diff checks ran, but no compiler,
unit/race execution, native process or full gate ran for this increment.

The earlier handoff proposal above is superseded by the actual IAM API recorded
below. The private process source now calls that constructor with the tenant
from installedTenant(original selected store) and only the existing configured
artifact roots. It registers the real ClosePublishedReaders operation before
later startup work, retains its exact error and refuses startup-context expiry.
IAM owns partial-open/graph-failure cleanup; ARC owns all later startup and
request/RPC/worker joins. No unused reader helper or alternate unbound graph is
introduced. This is source wiring, not executed CLI/claim/mount qualification.

The same completion counter also owns each session receive loop. It is registered
while its admitted RPC callback is still counted; therefore a racing drain cannot
miss that descendant. It is released only when RecvMsg/channel handling exits,
after stream cancellation/RPC return, without waiting inside the callback and
deadlocking transport teardown. HTTP join and RPC/receive-loop join are both
required before a process closer. The unit source covers real retained file
descriptors, but its execution and the full composed reader contract remain
unverified. No original IAM constructor, TRN owner path or agent v1 field changed.

## Actual IAM constructor consumption

The original IAM input is review tree1af0ca5497fbbe83875eb6af4769fad4198135ac
on5bbc324d, archive e475f54c97137d11b7a9f40b687d809f69e4c65d0068822cd1d707858fd8feaf
and hash manifest346e20c56a8032fba2815d4b06522c98ed9d96d7c814d9711f7161061757f6f5.
All archive members, the four IAM owner paths and all21 original TRN paths were
verified locally before import. The actual API differs from the earlier proposal:
NewAgentGatewayWithPublishedReaders(s, ready, tenant) opens/binds the concrete
readers itself; ClosePublishedReaders closes only those owners. ARC must consume
this actual API, not retain its proposed option struct or duplicate reader opens.
The original default constructor and all four imported owner paths remain intact.
The original IAM source now has the file-only qualification recorded below.
This combined process graph remains unqualified.

At process startup, no configured dataset/training namespace keeps the original
constructor. If either artifact_root is configured, select the actual reader
constructor; its required pair makes a partial/missing namespace fail visibly
instead of returning an unbound graph. There is no fallback after a reader or
graph error. Resolve tenant through installedTenant(startup, original store)
only, after the existing bootstrap/schema admission, and pass only the original
Config to the constructor. No deployment knob or client value selects a tenant,
file path, writer or authorization mode. This is one complete reader-enabled
path plus compatibility for the original namespace-free path.

Register ClosePublishedReaders with the process lifecycle immediately after
successful construction, before installation-fence admission, workers or listen.
That gives every later startup error the same reverse close path. Retain the
owner's exact close error in lifecycle failure before the generic store-close
classification is added; its concrete errors are fixed storage codes, never
private paths or credential values. A successful construction that has exhausted
its startup context must also unwind and return that original context error.
During serving, both real HTTP and RPC/receive-loop joins and all worker joins
precede reader close, with the pool registered first and closed last. An
unconfirmed join must never invoke reader or pool closers. Returned files remain
owned and closed by their actual admitted handler, not by a replacement writer.

Validation must combine IAM's original no-database graph/partial-open/failure
tests, ARC's real HTTP/RPC/file lifetime tests, and the original real PostgreSQL
retained-training-claim profile through this constructor. Actual CLI later-start
failure, missing/foreign same-host namespace, shutdown/restart and read-only
mount/role/offline runs remain required. Source imports and wiring prove none
of those executions. No new dependency, model or dataset is introduced; the
existing Go BSD-3-Clause, pgx MIT and gRPC-Go Apache-2.0 notices/offline rights
remain unchanged.

The actual caller is now wired directly in runAgentGateway. Its imported IAM
README's research link is retained by importing the exact original9a0543b9 note,
without changing that owner's four review paths. No typed option wrapper or
alternate reader constructor was added. All four candidate paths remain the
exact reviewed tree bytes; the original corrected observer/graph test roots and
ARC's prepared lifetime tests remain required. Real process startup with the
selected runtime principal, post-construction listen/fence failure cleanup,
active training claim and installed read-only namespace behavior still need
actual allocated execution. The old native opportunity remains NOTRUN and has
not been reused for this source.

### Qualified original IAM graph and combined-source boundary

The original IAM source is now committed as
`b3c2a700fe55e50be61c35155f9d43de665ff5ae`, tree
`d5e83cf914f3866a8d9e219dff63470886ee134f`, parent
`9a0543b9a84fbe82f45e3e67da24cca67748aaf2`. All four owner paths are
byte-identical to the earlier reviewed input already consumed at `8e9a6c4a`.
The exact original patch remains
`8907bbf2f38ce3aec7effb424d33aa4f40bff795ac72fd638d14d1208e0fb53d`.
The verified original-author bundle has SHA-256
`7016466592a837b546850baaa5fda481e2f3e9746549337a3ea1ec92a84cf1d4`.
The independent ARC candidate preserves both original DAGs through merge
`6999a6e5b8b16a9eb741e87487eba368a3a1621c`; its complete tree is exactly
the `8e9a6c4a` tree, including all 21 TRN and 13 MODEL owner paths. This joins
the original source identity without replacing the reviewed process input.

The owner's actual bounded opportunity passed the three original roots
`TestAgentGatewayPublishedReaderGraph`,
`TestAgentGatewayPublishedReaderFailureCleanup` and
`TestAgentGatewayPublishedReaderRequiresTrustedOwner`, plus the three cleanup
subcases `foreign_tenant`, `missing_second_namespace` and `invalid_graph`.
The raw JSON log contains all six passes and no skip or failure. The opportunity
took 9.018893875 seconds, including compilation and cleanup; its result SHA-256
is `19556c297c46005cc0c2639d39ee9945f5aa137d87c6c56106aa2e34b4621b17`.
All four tested owner hashes and all 21 producer hashes match the combined
candidate. The real file-only graph retained held writers, exercised eight
concurrent idempotent closes and attempted no database connection. The owned
process groups and the unchanged carrier were independently accounted for by
the original opportunity. Earlier failed/refused attempts remain retained.
The current upstream evidence is recorded in the original
[IAM qualification commit](https://github.com/Passion-Flow/ownsolid/commit/e9e715e223f6879339a29fbd2c4b12473c0b026e).
The imported `9a0543b9` research is the frozen earlier source input.

This closes the original constructor's source and file-only test dependency.
It does not execute ARC's actual `runAgentGateway` branch or qualify combined
HTTP/RPC/receive/worker shutdown, later startup rollback, restricted SQL,
training claims, installed read-only mounts or fleet cost. The combined source's
11 explicit no-database test roots have now run as recorded below, across
`cmd/server`, `internal/platform/agentgateway` and
`internal/modules/identity/http`. Compile-only and test-list output remain
separate from execution: a run is acceptable only when its complete expected
root set appears with passes, with no empty selection, skip or failure.
The original native SQL opportunity remains NOTRUN on SSH banner timeout255;
it is neither reopened nor reassigned to this source. All product boxes stay
open, and no runtime role or new support claim is activated.

### Joint source local lifecycle qualification

Exact source `3513ab2ba5885bb5d782919f7611d96e35d28fc6`, tree
`c65b8e3fde9833369c253c8303b70c3f13cae5d8`, passed the prepared local
qualification with its original IAM four paths, TRN 21 paths and MODEL 13 paths
and their original ancestors unchanged. All 5,102 manifest entries were checked
before entry. This is the joint source, not a reuse of IAM's earlier isolated
overlay result.

| Package | Actual race roots passed | Cleanup subcases passed | Skips/failures |
| --- | --- | --- | --- |
| cmd/server | 5 | 0 | 0/0 |
| internal/platform/agentgateway | 3 | 0 | 0/0 |
| internal/modules/identity/http | 3 | 3 | 0/0 |

Each package compiled, its binary list exactly matched the declared roots, and
the real JSON execution included every required run/pass and the package pass.
The cmd/server roots exercise sealed HTTP admission, a real HTTPS handler/file
owner, reverse lifecycle join, timed-out join retention and an unconfirmed stop.
The gateway roots exercise actual TLS gRPC cancellation, a callback retaining
an open file after transport close, concurrent joins and empty/repeated drain.
The original IAM roots exercise the one shared graph, failure rollback and
trusted-owner validation. They use explicit file-only fixtures and establish
no SQL or actual CLI claim authority.

The single Mac opportunity took 66.690797834 seconds including 0.561827958
seconds of cleanup and three additional serial Linux amd64 test-binary builds.
It used Go 1.27.3, race-enabled local tests, Go-p1/GOMAXPROCS=2,
GOMEMLIMIT=512MiB and offline caches, with no dependency download. Fresh raw
free memory after admission was 6,273,236,992 bytes, disk free 164,483,067,904
bytes and pressure 1, with no competing heavy process. The new coordinator
epoch carrier matched its exact path/FD identity throughout one NB admission,
all children and cleanup. All 13 child PIDs and the wrapper PID were absent
at readback; no owned group remained, the carrier was unlocked/closed and its
one-shot private entry was closed. No database, Docker volume, browser or
installed service was started. The real loopback test listeners ended with
their tests and processes. The original 82 legacy entries remain closed.

The retained evidence directory is `lane-arc-diagnostics/arc111-mac-e1.shn25mqk`.
Its unchanged result SHA-256 is
`44490f1356166aa4efd066fd62bd1b6b57b158447f90e512b6afe98b4353f313`;
the independent local cleanup readback SHA-256 is
`6872648ab89ade526f1e5a63d82d5c3cbd0c46c5dc3db12d989b768af9b9a8f2`.
The compiled database, databaseaccess and repository-tagged MODEL Linux binaries
are bound to that same source. Compilation executes no native SQL root.

After the Mac carrier was released, the separately granted single Linux
preflight returned SSH exit255 after 8.616071125 seconds, with
`Connection to UNKNOWN port 65535 timed out`. It received no resource JSON,
opened no native carrier, uploaded no input and started no database, SQL,
container, volume or credential. No retry followed. The exact source's native
result is NOTRUN, retained in
`lane-arc-diagnostics/arc111-native-e1.7axxa0p5/preflight-result.json`, SHA-256
`167ce561f41006e262a06eb0de132a0f5c1fd0a2314f1c91f4b4c71988175b31`.
No actual schema61 catalog JSON/fingerprint, admitted source B or runtime grant
qualification exists. The original schema60 pins, CatalogSQL and all assertions
remain unchanged. Actual CLI startup rollback, real restricted-role training
claims, read-only mount delivery, persistence, offline installed execution and
fleet cost remain open, as do the complete check/build/test gates and ARC-111.

The separately reauthorized native opportunity also stopped before a remote
preflight was received. Its sole SSH child returned 255 after 19.858641375
seconds with the same raw `Connection to UNKNOWN port 65535 timed out` error.
The exact `3513ab2b` source archive/manifest, all 61 migration hashes, all 16
metadata inputs and the three retained native binary hashes were verified
locally first. There is no remote entry receipt, catalog JSON, SQL case, role
execution, new pin patch or source B from this opportunity. The result is
NOTRUN; it does not supersede the earlier 8.616071125-second refusal or the
passing Mac qualification. No Mac carrier was opened or compiler started; no retry or
queue followed, and all legacy entries remain closed.

The exact SSH PID 59504, its wrapper PID 59392 and its owned process group were
absent at readback. Its one-shot private local entry was closed. Remote
workload admission was never confirmed; no global foreign-process or FD
absence is inferred. The retained result is
`lane-arc-diagnostics/arc111-native-formal61.qtc543lk/outer-result.json`, SHA-256
`fed248a4ece4bc3ef9c8fc698d51a6d38088c8744f3f2bad16288d06ebd38d1a`;
local cleanup readback has SHA-256
`390aa5c1b49245ab8db81e9479699392c9eed4744be2288a1fe577bf7f716c7c`.
Actual catalog collection requires a new authorized native opportunity whose
single SSH/resource admission succeeds. Matching source/pin binaries and the
protected original fixture inputs remain prerequisites for subsequent SQL
acceptance. The previously observed successful diagnostic SSH connection
establishes only its own observation, not continued access for this entry.

### Offline refusal analysis and private launcher corrections

The two failed attempts' retained command sources and bytes were compared with
the original resource-inspector observation, not with a new connection. The
successful observation is the inspector's item
`exec-387761bd-9a23-4247-804b-000b1940a688`: its derived debug markers show
the expected userspace proxy/socket, banner, KEX, known-host match,
authentication and `true && uname -srm`, with SSH/remote exit 0. Its original
full debug stderr was not retained, so those markers cannot supply missing
debug bytes for the failed attempts.

| Existing observation | ConnectTimeout / outer wait | Stdin | Actual SSH / outer exit |
| --- | --- | --- | --- |
| First failure, 8.616071125 s | 10 s / 25 s | Inherited, no payload pipe | 255 / 0 |
| Renewed failure, 19.858641375 s | 10 s / 1,100 s | PIPE, 12,840,960-byte payload | 255 / 0 |
| Successful diagnostic, 9.165 s | 8 s / 15 s | Inherited, no explicit payload pipe | 0 / 0 |

All used one connection attempt. Success used `-vv` and BatchMode=yes; failures
had no verbosity/BatchMode override and used ServerAliveInterval=5 with count 1
or count 2. The retained failure argv was recovered without executing it: the
first from the untruncated tool source and the second from its unchanged driver,
remote-program hash and input JSON. The first ran from the ARC worktree, the
second from its private evidence directory, and success from the main checkout.
Each invoked an unqualified `ssh`; historical resolved executable paths and
necessary inherited environment were not recorded and cannot be reconstructed
from current values. The renewed child PID 59504 was recorded as ssh and returned
255 naturally. Neither failed attempt entered its outer timeout branch. The
first observed error is the same saved `Connection to UNKNOWN port 65535 timed
out`; the first capture normalized newlines, while the second retained CRLF.

Current non-dialing `/usr/bin/ssh -G` parses every profile to the same root user,
100.100.110.24:22 and absolute Tailscale CLI/userspace socket. The user config
has no Include or Match directive. The current installed client is
OpenSSH_10.3p1; its `ssh_config(5)` describes ConnectTimeout as connection plus
initial handshake/KEX, and ProxyCommand as a stdin/stdout transport. The
UNKNOWN/65535 display is consistent with missing socket peer information on
that proxy path; it is not evidence that the configured destination port is
65535. This is an inference, not a failed-attempt packet trace. No failed
debug marker proves whether the proxy, daemon/relay, banner, KEX or
authentication completed. The error occurring both with and without a payload
pipe does not establish stdin/EOF as its cause. There is no confirmed remote
bootstrap error or basis for attributing a server outage, bad credential or
wrong daemon. The earlier admission classification is not a finer stage proof.

Two separate private-control defects were proved and corrected in a frozen,
unactivated candidate. The old outer driver implicitly exited 0 even after
SSH 255 or an unrelated successful observation without a native receipt. The
candidate forwards SSH 255, returns 124 for its own timeout and refuses incomplete
native evidence. Also, a relative 1,100-second wait plus a post-hoc 1,200-second
check did not enforce an absolute 120-second cleanup reserve. The candidate caps
work at absolute 1,080 seconds, clips cleanup waits to the end, caps the remote
budget at 1,000 seconds including its 120-second reserve and bounds cleanup itself.
These defects do not explain the observed earlier connection timeout.

The candidate fixes `/usr/bin/ssh`, records executable/config hashes, UID/cwd,
exact argv and stdin ownership, uses `-vv` and noninteractive BatchMode=yes,
and preserves ConnectTimeout=10, one attempt, payload stdin and existing host-key
policy. It does not use `-n`, extend timeouts or alter SSH/Tailscale configuration.
Raw bytes stay private; presented observations contain necessary errors and
nullable stage markers, not credentials or an assertion that an unlogged stage
was never entered. Distributed cleanup remains a live qualification obligation.

Six offline replays with subprocess doubles passed: both saved failed transcripts reproduce the
old false outer exit 0 and the corrected 255; the unrelated successful inspector record
reproduces old outer0 and corrected exit 1. Twelve pure policy checks cover
deadline boundaries, cleanup bounds and unknown-stage handling. Four current
`ssh -G` profiles were parsed without dialing. This establishes private launcher
logic only, not restored connectivity or SQL acceptance. No SSH connection,
heavy carrier, compiler, service, database or browser was started. Original raw
bytes, the source/manifest, all three native binaries and original owner files
remain unchanged; all actual launch entries remain closed. No new dependency
or license obligation is introduced.

The frozen artifact directory is `lane-arc-diagnostics/arc111-ssh-offline.9vgaoj6h`.
Corrected outer source SHA-256 is
`a43481a9ae1fff7d08c46f9068f4d01aec335927485deee041f5bd62d9c35344`,
remote source SHA-256 is
`4fe8acf210f41a7554b46e01a79b1ed235c212096fcf80f47ab6e6534aec8b51`,
and offline replay result SHA-256 is
`73209413ae4f15b5113284423d681da71c8e8e7a0a2071a9b054c75bb49c93c8`.
The next external release condition is one separately authorized diagnostic
under the frozen actual launch profile that captures proxy/banner/KEX/auth
stages and confirms access. No waiting process, automatic retry or native
qualification is implied by this offline correction.

### Observed remote entry and source-admission failure

A separately authorized observed entry used exact source `3513ab2b`, tree
`c65b8e3fde9833369c253c8303b70c3f13cae5d8`, with all three retained native
binary hashes unchanged. It used the actual failed invocation's cwd, inherited
environment, `/usr/bin/ssh`, configured userspace ProxyCommand/socket, PIPE
payload, effective BatchMode=no, ConnectTimeout=10, one attempt and keepalive
settings. Only `-vv` was added to the SSH options; the offline candidate's
BatchMode=yes override was removed to preserve the actual authentication
profile. The new entry was separate from every closed legacy stub. Raw logs
were actual 0600 files in its private directory.

| Parent-observed stage | Seconds after entry start |
| --- | --- |
| Proxy command/local version | 0.0292 |
| Server banner | 3.1126 |
| KEXINIT | 3.4469 |
| Known-host match | 4.0340 |
| Authentication | 6.4714 |
| Remote command sent | 7.6436 |
| Remote preflight and dual-carrier admission markers | 8.3970 |
| SSH/outer exit 255 (overall elapsed) | 99.0435 |

These are parent observation times with 20 ms polling and exact stream byte
offsets, not packet timestamps. The first necessary error was
`Timeout, server 100.100.110.24 not responding.` The natural client exit and
outer exit were both 255, confirming actual error propagation. Neither the
1,080-second work cutoff nor the final cleanup deadline was reached. Earlier
banner-timeout attempts retain their original evidence; this later successful
handshake does not establish their cause.

The real resource preflight observed Free 3,306,266,624 bytes,
Available 20,793,270,272 bytes, disk 788,917,272,576 bytes, full I/O PSI 0.91
and no competing heavy process. Both original exact carrier identities passed
one NB each. The cached image ID/platform matched the pinned Linux/amd64 input.
No PostgreSQL server or vector extension query was started.

After transport loss, one bounded connection performed cleanup/readback only;
it did not resume upload, retry a native profile or reacquire either carrier.
It returned 0 and recovered the original guard's actual result:
FAILED_OR_INCOMPLETE, PAYLOAD_REFUSAL, 122.163887662 seconds including
0.055226806 seconds of cleanup. The sole command stage was image-inspect,
exit 0 in 0.093082848 seconds. The input length/digest check failed before source
extraction/verification and fixture creation. Received length/digest were not
recorded, so neither truncation nor corruption can be assigned retrospectively.
There are zero actual catalog records, native binary executions, SQL roots or
restricted-role cases, and no source B or actual pin patch.

The cleanup-only audit found the original runner PID 358460 absent/reaped,
no remaining owned group, both exact lock FDs closed and both carrier paths
unchanged. No fixture UUID, container, volume or network creation path was
reached; credential paths were absent. The cleanup audit took 0.000413430 seconds
and required no signals or resource-removal action. Local diagnostic/cleanup
SSH groups and wrappers were absent; the log observer joined. Cleanup confirmation
was retrieved at approximately 436.18 seconds of the original 1,200-second window.
This is an owned-scope proof, not a global foreign-FD or service assertion.

The initial local JSON's default NOTRUN label was too coarse after confirmed
remote entry. Preserve that raw result, but classify the whole opportunity as
entered, transport failed and input admission incomplete. The authoritative
remote receipt proves the SQL/fixture stages did not run. No cleanup success
is inferred merely from SSH exit or an empty local listener list.

The private evidence directory is
`lane-arc-diagnostics/arc111-native-observed.eldemtbg`. Curated trace SHA-256 is
`7fd7627c01cb736c9591891a0134e45ea0fcf617313629480b1e0ca54aec47b0`;
the original remote result SHA-256 is
`c6280bcc225fcb89efbab8ec316311e4ee87551e9044a9cf3521a7fa74e2371b`,
and independent remote cleanup readback SHA-256 is
`79e64db0ba30612a7e8950db0e24eac1408fa621c060eae5f28edd275b5cacad`.
The one actual diagnostic and one necessary cleanup connection ended. Every
entry is closed; no new connection or automatic retry is active. Original owner
source, schema60 pins, agent v1 and prior 11-root race qualification remain intact.

### Bounded input reception and entry-aware failure classification

The actual remote result exposes a private admission defect: blocking
`stdin.buffer.read(expected_bytes+1)` was outside any timed subprocess and the
45-second phase check ran only after it returned. With only 0.093 seconds in
image inspection, the guard still reached 122.164 seconds before input refusal
and cleanup. The phase deadline therefore did not bound that read. The raw
receipt retains its original PAYLOAD_REFUSAL and missing receive counters;
no controlled test reconstructs the actual received bytes or digest.

A frozen, unactivated correction uses the existing monotonic admission/work
deadline for nonblocking reads and readiness waits, records byte count/digest,
EOF and receive duration on success or failure, and restores the original FD
blocking mode. Both exact length and SHA-256 remain required and the original
integrity assertion is retained; extra bytes, missing bytes, wrong digest and
missing EOF fail. No timeout, source requirement, SQL assertion, role grant,
fixture skip or retry is relaxed. A separate parent correction classifies
confirmed entry plus transport loss as failed after entry, preserving unknown
SQL/cleanup state until its authoritative receipt is available. Missing final
output alone no longer produces a whole-opportunity NOTRUN label.

Six controlled local payload cases pass, including the exact retained input,
short/extra input, wrong digest, missing EOF and an expired deadline. Two real
local OS-pipe cases pass: EOF completion and a still-open writer timing out;
both restore FD mode and close their own descriptors. Five existing-record
classification checks retain the distinction between the earlier unconfirmed
entries, this confirmed entry and its cleanup-recovered failure. These are
private I/O/policy checks with no network, child process, compiler or SQL, not
a successful transfer or database qualification. Actual entry files stay closed.

The correction is frozen under the same evidence directory's `post-entry-fix/`.
Remote source SHA-256 is
`6d54f58cbf83b61dc189b279742f37a29e821eebdbd6b7101549e6b9d7df2838`;
parent source SHA-256 is
`4a94e6edda5fc4297ee5b79accc4b5eae36f3bbcb466559f37ceb11a7ad48a30`.
The OS-pipe verification receipt SHA-256 is
`fec29c52e37f95332cd99a96a6c2049af780427a6c58045b48ac7b0c333f26b0`.
The next native prerequisite is verified delivery of the exact input and EOF
within the original bound, followed by fresh original resource/carrier admission
and matching catalog-pin source. A healthy banner/authentication alone does not
prove that prerequisite. No new dial, native retry or source B was produced by
this correction.

### Exact source delivery through a verified read-only cache

The original 12,840,960-byte input requires 285,354.67 bytes/s for a full
45-second receiver window, before source verification. The retained SSH trace
contains an initial 2,097,152-byte channel credit and 1,390,592 bytes of later
credit adjustments. RFC 4254 [section 5.2](https://www.rfc-editor.org/rfc/rfc4254#section-5.2)
defines these as flow-control credits. Their sum is not an application receive
counter, a timed throughput sample or a future bandwidth guarantee. Neither the
earlier healthy handshake nor the local pipe test proves remote input delivery.
Repeating the full package without a measured receive receipt is rejected.

A private prototype keeps target source `3513ab2b`, tree `c65b8e3f`, all three
native binaries, 61 migrations, 16 exact SQL inputs and the original schema60
pins unchanged. It uses a Git binary patch from retained source `149a9501`
to reconstruct a newly owned tree. The existing remote cache is read only:
all 5,015 basis file inputs require their exact length, SHA-256, Git blob ID,
regular-file mode, owner and single-link identity. Symlinks are refused using
directory-relative no-follow opens. The patch is applied only to the new copy;
all 5,102 resulting paths, bytes, Git blob IDs and modes must match the original
target manifest. Missing, changed or incompatible cache inputs fail without a
fallback transfer, native retry or modification of the cache.

Git's [apply contract](https://git-scm.com/docs/git-apply) permits use outside
a repository and supports binary patches. A child-only umask of 0022 produces
the exact Git 0644/0755 file modes; credentials, logs and outer directories keep
0077. The retained original source archive uses exact 0664/0775 tar modes;
that archive's hash remains a local reference and is explicitly **not** claimed
as remotely transferred or reconstructed. The remote proof is the complete
target source manifest, not equality of a differently encoded archive.

The envelope uses standard Python [LZMA/XZ](https://docs.python.org/3/library/lzma.html)
with preset 6 and a SHA-256 check. The frozen wire frame is 720,340 bytes,
SHA-256 `7bf611452afb455b2535b6b928257da726cb250b2ec942adf1d23063df4234cb`.
The decoded tar is exactly 5,304,320 bytes,
SHA-256 `f55b36054aef4e6277c5b1164b07877a9e8ce7fced00dc23c8b8f4f4a451c910`.
Both boundaries, every whitelisted member, the original manifest and all SQL
inputs must verify. Decoding has a 64 MiB memory limit and an exact output bound;
incomplete or extra compressed streams fail. No SQL input is regenerated or
normalized. The original 45-second admission cutoff includes reception,
decoding and source verification and remains unchanged.

The complete actual frame, not a short surrogate, passed one real local OS
pipe using the frozen sender and receiver. Both measured 720,340 bytes and the
same SHA-256; the receiver observed EOF, the sender closed its own descriptor
and its writer thread joined. Local receiver duration was 0.000554875 seconds.
Reconstruction of the complete 42,132,271-byte target tree took 1.386644959
seconds. All 61 migrations and 16 original SQL inputs matched. Wrong cache
hash, absent file and a symlink were refused. These are local equivalence and
I/O results only. Required wire throughput is 16,007.56 bytes/s even before
verification overhead; the channel trace provides no guaranteed lower bound
for the next connection. One separately allocated observed opportunity may
measure delivery, but must fail on the original deadline and record actual
local writes separately from remote bytes, digest, EOF and first failure stage.

Private artifacts are under `lane-arc-diagnostics/arc111-native-delta-preflight`.
No compiler, network, SQL or native binary was run by the prototype. The tools
are already installed and are not added to the product or distributed:
Python uses [PSF-2.0](https://docs.python.org/3/license.html), Git uses
[GPL-2.0](https://raw.githubusercontent.com/git/git/master/COPYING), and
liblzma's upstream [license](https://raw.githubusercontent.com/tukaani-project/xz/master/COPYING)
is 0BSD (older releases used public-domain terms). Commercial and offline
execution is allowed; redistribution must retain each applicable license and
Git's corresponding-source obligations. No model or dataset is introduced.

### Observed complete frame and strict cache-mode refusal

The separately allocated opportunity used the frozen 720,340-byte frame above
and the same source, binaries and SSH profile. The local sender actually wrote
720,340 bytes into its OS pipe in 7.474354375 seconds and closed it; this local
counter is separate from delivery. The authoritative remote receipt measured
720,340 bytes in 2.256576253 seconds with the expected SHA-256 and EOF. The
decoded 5,304,320-byte frame also matched its frozen hash. Input delivery is now
measured for this one frame and opportunity, not for arbitrary future transfers.

Fresh preflight observed Free 3,245,744,128 bytes, Available 20,747,808,768
bytes, disk 788,916,449,280 bytes, full I/O PSI 0 and no competing heavy process.
Both exact original locks passed one NB each; the cached image identity and
platform matched. Authentication was observed at 5.6207 seconds, remote entry
at 7.5918 seconds and payload completion at 9.9627 seconds of local elapsed
time. These remain parent observation timestamps, not network packet times.

The next strict source check failed with `SOURCE_MODE_MISMATCH` while reading
the existing cache, before Git patch application or complete target-source
verification. The remote failure receipt did not include the path or observed
mode, so neither a filename nor a concrete permission value is known. The
local prototype's normalized basis did not reproduce that remote cache state.
Do not treat the completed frame or local reconstruction as actual source
admission, and do not change an assertion to accept an unobserved cache mode.

SSH and the outer driver both returned 1 naturally after 10.179709125 seconds.
The remote runner recorded 2.582643375 seconds including 0.050232410 seconds
of cleanup, no remaining owned group, both original FDs closed and carrier
identities retained. No fixture UUID, container, volume, network or credential
creation path was entered. No database, native binary, catalog, role or SQL
profile ran; no source B or new pin exists. The local SSH group and wrapper were
absent and the observer joined. The authoritative receipt and natural remote
exit required no cleanup-only connection. The owned input/partial source scratch
is retained for forensics; it contains no credential or running service. No
global foreign-FD or customer-environment claim follows from this cleanup.

The actual directory is `lane-arc-diagnostics/arc111-native-delta-observed.yvsxr0l4`.
Outer result SHA-256 is
`a3fd308b8cff6c124e847e9ea43ac2a73ba748879f7270b61b47d662e9ee7ba8`;
cleanup/scope receipt SHA-256 is
`da2b7e3ee9f7427c67f74d3c726222f2d30c8e4c8d748ec852774233927a70e4`.
Both actual entry files are closed and this opportunity is ended without retry.

A separate data-only diagnostic candidate preserves the exact verification
assertions and adds the rejected relative path, expected/actual mode, length,
digest, UID and link count to a failure. Four local cases prove that 0600,
0664 and 0755 still fail against expected 0644, while exact 0644 succeeds.
This does not identify the actual rejected cache mode. The candidate is
unactivated; its source SHA-256 is
`faf141220ffc0d2668ad6de3d64e463874fa31bae151d4922a7eee013a7e409e`.
The next release condition is evidence of the actual cached input identity and
mode, an exact target-source admission and then the original fresh/restore
catalog work. Schema60 pins, original owner files and SQL assertions stay intact.
