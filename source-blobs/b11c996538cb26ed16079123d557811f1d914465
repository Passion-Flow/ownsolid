-- Remove only unregistered family metadata; immutable versions and references stay intact.
DROP TRIGGER model_family_version_insert ON model_versions;
DROP FUNCTION model_family_before_version();
DROP INDEX model_version_history_order_idx;
ALTER TABLE model_families DROP CONSTRAINT model_family_initial_member_fk;
ALTER TABLE model_versions DROP CONSTRAINT model_version_family_member_fk;
DROP TABLE model_family_members;
DROP TABLE model_families;
DROP FUNCTION model_family_immutable();
DROP FUNCTION model_family_member_insert();
