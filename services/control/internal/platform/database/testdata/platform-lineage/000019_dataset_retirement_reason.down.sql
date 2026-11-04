-- Explicit isolated qualification rollback only. This restores the weaker
-- frozen18 NULL semantics; it is not an automatic production recovery procedure.
-- Keep both table data and immutable version/artifact references unchanged.
ALTER TABLE dataset_versions DROP CONSTRAINT dataset_version_retirement_reason_required;
ALTER TABLE datasets DROP CONSTRAINT dataset_retirement_reason_required;
