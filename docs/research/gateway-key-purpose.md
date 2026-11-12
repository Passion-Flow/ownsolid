# Gateway key purpose and rotation (GW-105)

Reviewed 2026-11-09.

[LiteLLM Virtual Keys](https://docs.litellm.ai/docs/proxy/virtual_keys)
treats model access as key authority, and its service accounts separate
application ownership from a person's account. [OpenAI project service
accounts](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/service_accounts)
likewise scope application credentials to a project. Ownsolid's existing
accepted-operation key is a private one-use credential stored in the same
table as ordinary workspace keys. Machine admission distinguishes the
credential's MAC purpose. The human rotation path has no proof of that
purpose when it reads only a predecessor ID.

The candidate stores the server-selected purpose as a private, nullable PostgreSQL boolean
on the key row. A new issuance records whether it is operation-only. A
public rotation can issue a successor only when the locked predecessor
has proved ordinary purpose. Refuse an operation key or an unclassified
legacy key with the existing API_KEY_NOT_ACTIVE rotation error. An already
committed idempotent rotation still returns its retained redacted result.

A historical one-way MAC cannot be classified from its bytes. Do not guess
from labels, audit text, lifetimes or user-controlled idempotency keys. Keep
legacy credentials valid at their existing scope. A successfully admitted
call proves the MAC purpose and records it in the same update as last use.
A legacy ordinary key can then rotate, and an operation key remains
ineligible. Replacing the entire ordinary rotation flow with proof of
possession would prevent authorized operators from rotating another
person's key. A migration with a default ordinary value would silently
grant rotation authority to old operation credentials.

[PostgreSQL ALTER TABLE](https://www.postgresql.org/docs/18/sql-altertable.html)
allows a nullable column to be added without a table rewrite. Rotation is
already PostgreSQL-only. Keep the other engines' issuance and one-use MAC
checks as they are. No API fields, credential formats or new dependencies
are required. [PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) allow commercial use,
offline use and redistribution under their PostgreSQL License and MIT
notice terms. No model or dataset is involved.

- During a real accepted-operation callback, use the authorized key list
  and public rotation service without the old secret. Refuse rotation
  and ensure cleanup leaves no successor. An ordinary key still rotates.
- Exercise both legacy purposes. Before proof, refuse new rotation. Once
  a valid admission records the original MAC purpose, permit ordinary
  rotation and reject operation rotation. Verify failed admissions do
  not classify a key, one-use restrictions and cleanup still hold, and
  concurrent ordinary rotations retain one successor.
- Preserve the existing HTTPS rotation contract and denial across owners.
  Authoritative, persistent application identity and automatic quota
  assignment remain separate work.

## PostgreSQL qualification and integration boundary

The existing authorized rotation path issued a successor from a live
operation key and the successor admitted two calls. A candidate with a
nullable operation_only column refused rotation and created no successor.
Two credentials were inserted into the real predecessor schema before
upgrade. A declined call did not classify them. A successful admission
restored ordinary rotation and retained one-use operation authority.
Existing HTTPS rotation, concurrent rotation, independent cleanup, and
the native Rust/Go license-expiry path passed against the candidate.

The candidate migration alone failed the standard catalog gate with
SCHEMA_CATALOG_INCOMPATIBLE. PostgreSQL 18.6 and pgvector 0.8.7 in an
isolated container reproduced both current catalog fingerprints first.
After adding the column, fresh and logical dump/reparse fingerprints
were calculated with the production CatalogSQL. The focused suite used
a test-only source overlay for the two exact candidate signatures.

| Catalog | Candidate SHA-256 |
| --- | --- |
| Fresh | 0bd2948ab067934a658502d56644571fd651f0e6296a2a2e25d6438e574a7f33 |
| Restored | 6a58b24c139d2d4c3645b81598b13c4743878acc6a5a88a97a2b76821456699d |

The historical lineage-57 overlay used the label 000058 for the
operation_only column. The current candidate keeps both SQL files
unnumbered. The database owner needs to
adopt the candidate migration and both fingerprints together, keeping
the table count at 118. Recompute both fingerprints if another migration
lands first. Run a fresh install and an upgrade with existing ordinary
and operation credentials, reparse a logical backup, and rerun the
distribution rotation and accepted-operation tests with normal catalog
verification. The migration and production code remain a candidate
pending this shared integration.

The Linux candidate patch is retained in /root/lane-gw-acceptance/gw105.ZhApgteQ/candidate.patch, based
on 9eb9b9f411c4c04b75aca83e813f7d221eb8dcae, SHA-256 882ba16eb9f8f47089a722a5c8b9f8b3f9d8fb21d9d4d351ac0fb09f3e04ad96. The adjacent catalog.patch contains only the
two verified fingerprint changes. Neither patch contains key material.

## Current registered entrances and compatibility fixture

The registered Portal rotation route accepts the key ID and an authorized
session, so it can create a successor without checking the original MAC.
The separate machine routes for models, admissions and outcomes are
called by the Rust gateway. The current trusted operation caller also
uses the same /v1/chat/completions route and bearer credential. The
admission request contains no installation or operation proof. A new
caller header would be forgeable. Denying all unused operation keys at
that route would also break the trusted caller.

Reject a consumed operation credential in the shared key-authority
check, before both model discovery and admission. Repeat the check under
the existing key lock. The first accepted request can still finish
reporting through its gateway credential. Preserve the distinct expiry
and revocation semantics and the ordinary key path. Test fresh and
consumed operation keys through the registered machine TLS routes.

Qualify the saved rotation patch in a source overlay with the exact
unnumbered nullable-column SQL. Seed predecessor credentials before
applying the SQL in the owned PostgreSQL fixture. Test Portal rotation
and revocation and the registered machine models, admissions and
outcomes. Check the ordinary successor's scope, the redacted rotation
replay and refusal to mint or admit through an operation successor.
The candidate source and database change must activate together. ARC
owns the next numbered migration and exact production fingerprints.
The previous number and fingerprints describe the historical overlay.

CAT-103 requires an installation-owned app identity and durable model and
workspace bindings, an authenticated route or proof for an accepted
operation, and secret-reference delivery, rotation and revocation tied
to that installation's lifecycle. The current bearer wire cannot
distinguish an unused operation credential presented by its intended
caller from the same credential presented by another caller. No
installation-level authority is inferred from purpose, labels or a
caller-supplied field. Existing issued credentials and redacted rotation
receipts preserve their documented behavior. This work reuses the
existing Go and PostgreSQL dependencies and adds no model or data set.

### Disconnected current-lineage evidence

The registered machine models probe returned HTTP 200 for a spent
operation key before the shared check changed. It returned 401
API_KEY_INVALID afterward. A repeated admission also returned 401
and the accepted request still received its receipt through the
gateway credential. The ordinary HTTPS rotation contract and the
concurrent single-pool accepted-operation test passed. The test
fixture now points distribution, identity, workspaces, nodes and
models at the same single-connection pool.

The unnumbered upgrade ran on the committed schema-57 baseline in an
owned PostgreSQL 18.6 fixture with pgvector 0.8.7 and only loopback.
Portal refused both unclassified predecessor rotations. A wrong-alias
admission did not classify either key. After proved admission, the
ordinary key rotated once, its redacted replay returned the same
successor and explicit revocation ended the successor's authority.
The operation credential was refused by rotation before and after
admission, could not be used again, and its already admitted receipt
settled after explicit revocation. Fresh operation issuance wrote its
private purpose, stayed ineligible for rotation and cleaned up after
the callback. The test applied the down SQL in its disposable schema.

The new SQL assets in distribution testdata are unnumbered. The saved
source patch contains only the three distribution files that need the
new column. Apply it after the shared consumed-key check in
`7b4c44f3`, which is already present in the tested source overlay. Its
SHA-256 is
`1d713e79510f38541cf41620757db52a7623b380c49bcc68b1f26f8cdfabe6bd`.
ARC must pair those source changes with the actual numbered migration
and recompute the fresh, current restore and older accepted restore
signatures. The candidate adds no table. The tracked consumed-key
check works on the predecessor independently of this activation.

The upgrade does not infer the purpose of a historical predecessor
from its MAC bytes, including a key used before migration. A legacy
ordinary key must complete a new authenticated admission before
rotation. If its secret is unavailable or its alias cannot admit,
rotation cannot classify it. The existing API_KEY_NOT_ACTIVE problem
covers this refusal. The installation help needs to describe this case
when the migration is enabled. Previously issued ordinary successors
keep their existing authority unless explicitly revoked. An unspent operation
credential still requires an independently authenticated operation or
installation channel to distinguish its intended first use. CAT-103
remains open for that authority and installation lifecycle.

With the pinned fixture and `PROJECT_ONE_TEST_DSN_FILE` in its loopback
namespace, run the predecessor regression directly.

```sh
go -C services/control test -p 1 ./internal/modules/distribution -run '^Test(ConsumedOperationKeyRefusedAtMachineEntrances|RotationAndCallLogOverHTTPSMatchTheContract|M19AcceptedOperationAcrossExpirySinglePoolAndConcurrentReplay)$' -count=1
```

For the compatibility case, apply the saved source patch to temporary
copies of the three source files and point the Go overlay's Replace
map at those copies. Run the tagged test against the predecessor with
the raw DDL embedded from testdata.

```sh
go -C services/control test -p 1 -overlay "$candidate_overlay" -tags gateway_key_purpose_candidate ./internal/modules/distribution -run '^TestPostgresGatewayKeyPurposeCandidateAtRegisteredEntrances$' -count=1
```


The independent consumed-key check, ordinary HTTPS rotation and the
single-pool/concurrent accepted-operation regression also passed on
committed schema 58 with the retained gateway source at 6f31064f. The
native Rust/Go/PostgreSQL path passed in the same isolated qualification.
Evidence is retained in
/root/lane-gw-acceptance/schema58-gateway-regression.zxq09kh2. This run
uses the predecessor key schema. The nullable-purpose source overlay
and its prior upgrade proof remain separate from production activation.
The intended-first-caller proof and CAT-103 installation authority are
still required; a spent-key refusal does not establish either.
