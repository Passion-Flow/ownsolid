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

Actual reader consumption is still blocked on IAM's additive graph entry. The
minimal handoff is the concrete pair in AgentGatewayPublishedReaders above,
borrowed from the process owner and bound to its single existing graph. ARC must
receive the original signature and source before opening either configured
namespace in runAgentGateway; it must not open unused readers and return success.
The process input remains installedTenant(original selected store), the existing
Datasets.ArtifactRoot/Training.ArtifactRoot and TRN e639db90's exact Open/Close
APIs. A partial second open or construction failure must close only already opened
readers in reverse order. Those operations are not implemented by an unused
helper or an alternative unbound constructor in this increment.

The same completion counter also owns each session receive loop. It is registered
while its admitted RPC callback is still counted; therefore a racing drain cannot
miss that descendant. It is released only when RecvMsg/channel handling exits,
after stream cancellation/RPC return, without waiting inside the callback and
deadlocking transport teardown. HTTP join and RPC/receive-loop join are both
required before a process closer. The unit source covers real retained file
descriptors, but its execution and the full composed reader contract remain
unverified. No original IAM constructor, TRN owner path or agent v1 field changed.
