-- Forward-only account22 evolution. Never replay the independent platform ledger.
-- Preserve accepted membership/session/component/review/GPU identity constraints.
-- Digest values are already checked as exactly 64 hex bytes: this conversion
-- preserves every value and gives the full repository its canonical SQL types.
ALTER TABLE deployments DROP CONSTRAINT deployment_model_variant;
ALTER TABLE deployments ALTER COLUMN catalog_digest TYPE char(64);
ALTER TABLE deployments ALTER COLUMN catalog_digest SET DEFAULT '4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc';
ALTER TABLE deployments ALTER COLUMN serving_recipe_sha256 TYPE char(64);
ALTER TABLE deployments ADD CONSTRAINT deployment_model_variant CHECK(
 (model_kind='approved_base' AND catalog_digest='4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256 IS NULL)
 OR (model_kind='merged_lora' AND catalog_digest~'^[a-f0-9]{64}$' AND catalog_digest<>'4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256~'^[a-f0-9]{64}$' AND serving_recipe_sha256 IS NOT NULL)
);
-- Keep the scoped reference; its historical generated name becomes canonical.
ALTER TABLE managed_components RENAME CONSTRAINT managed_components_tenant_id_workspace_id_node_id_last_tas_fkey TO components_task_scope_fk;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_last_task_id_fkey FOREIGN KEY(last_task_id) REFERENCES node_tasks(id);
-- The old chain already has immutable snapshots. Fill only a genuinely missing
-- current snapshot using the original scoped model/deployment/history identities;
-- the existing BEFORE INSERT trigger independently checks the current target.
ALTER TABLE model_alias_target_versions DROP CONSTRAINT model_alias_target_versions_action_check;
ALTER TABLE model_alias_target_versions ADD CONSTRAINT model_alias_target_versions_action_check CHECK(action IN ('migration_current','published','disabled','paused','resumed','retargeted','rolled_back'));
INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest,serving_recipe_sha256)
 SELECT gen_random_uuid(),a.tenant_id,a.workspace_id,a.id,a.revision,'migration_current',d.id,a.deployment_revision,d.model_id,d.model_kind,d.catalog_digest,d.serving_recipe_sha256
 FROM model_aliases a JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.id=a.deployment_id
 WHERE NOT EXISTS(SELECT 1 FROM model_alias_target_versions v WHERE v.tenant_id=a.tenant_id AND v.workspace_id=a.workspace_id AND v.alias_id=a.id AND v.revision=a.revision);
-- Reject corrupt old provenance; never manufacture an approval or replace IDs.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM model_preparation_reviews WHERE NOT
  ((decision='approve' AND model_id IS NOT NULL AND model_id=artifact_id AND length(model_name)>0)
   OR (decision='reject' AND model_id IS NULL AND model_name='')))
 THEN RAISE EXCEPTION 'MODEL_REVIEW_IDENTITY_INCOMPATIBLE'; END IF;
END $$;
