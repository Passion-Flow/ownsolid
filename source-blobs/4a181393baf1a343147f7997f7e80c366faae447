# Unpublished model version history prototype

Revision 2 is an isolated OpenAPI 3.1 / v1 design, not a product route. It keeps
all existing response schemas unchanged. The new exact-version-anchored history
read returns the model owner's family UUID, anchor ID and bounded metadata page.
Retained detail checks both family and exact immutable version. Existing Admin
and Portal ready-only admission remains separate from ready/retired metadata.

This avoids adding fields to old closed response schemas and needs no family
guessing or extra identity ledger. Family UUIDs and row IDs in codec fixtures
are explicit synthetic owner inputs. The catalog fixture is the repository's
pinned metadata; no model bytes, storage approval or live authorization is tested.

model-owner-decisions.json records the durable membership, legacy mapping,
registration/reimport/derivation, cursor and retained-read inputs required from
the model owner. Public OpenAPI/SDK/Help and Web S-202 stay unqualified until
those inputs and the original dispatcher/PostgreSQL flow are verified.
