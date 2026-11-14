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

On lineage 57, migration 000058 adds operation_only boolean to api_keys
and its down migration drops that column. The database owner needs to
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
