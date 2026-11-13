-- Only an empty candidate can be removed without losing established identities.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM model_families) THEN
  RAISE EXCEPTION 'MODEL_FAMILY_DOWNGRADE_REQUIRES_RETENTION' USING ERRCODE='0A000';
 END IF;
END $$;
DROP TRIGGER model_family_version_insert ON model_versions;
DROP FUNCTION model_family_before_version();
DROP INDEX model_version_history_order_idx;
ALTER TABLE model_families DROP CONSTRAINT model_family_initial_member_fk;
ALTER TABLE model_versions DROP CONSTRAINT model_version_family_member_fk;
DROP TABLE model_family_members;
DROP TABLE model_families;
DROP FUNCTION model_family_immutable();
DROP FUNCTION model_family_member_insert();
