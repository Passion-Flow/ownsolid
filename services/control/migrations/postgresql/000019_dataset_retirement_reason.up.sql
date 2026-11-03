-- Preserve frozen migration8. CHECK accepts SQL UNKNOWN, so require the
-- conditionally mandatory reason explicitly. Existing invalid rows refuse the
-- entire migration transaction; no operator reason is fabricated or backfilled.
ALTER TABLE datasets ADD CONSTRAINT dataset_retirement_reason_required
 CHECK((retired_at IS NULL AND retire_reason IS NULL)
    OR (retired_at IS NOT NULL AND retire_reason IS NOT NULL AND length(retire_reason)>0));
ALTER TABLE dataset_versions ADD CONSTRAINT dataset_version_retirement_reason_required
 CHECK((retired_at IS NULL AND retire_reason IS NULL)
    OR (retired_at IS NOT NULL AND retire_reason IS NOT NULL AND length(retire_reason)>0));
