\set ON_ERROR_STOP on
-- Metadata derivation only: this never substitutes for production Migrate.
BEGIN;
SET LOCAL search_path = :"schema";
COMMIT;
