ALTER TABLE deployments
    ADD COLUMN model_kind VARCHAR(20) NOT NULL DEFAULT 'approved_base',
    ADD COLUMN catalog_digest VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc',
    ADD COLUMN serving_recipe_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin,
    ADD CONSTRAINT deployment_model_variant CHECK(
        (model_kind='approved_base' AND catalog_digest='4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256 IS NULL)
        OR (model_kind='merged_lora' AND REGEXP_LIKE(catalog_digest,'^[a-f0-9]{64}$','c') AND catalog_digest<>'4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256 IS NOT NULL AND REGEXP_LIKE(serving_recipe_sha256,'^[a-f0-9]{64}$','c'))
    ),
    ADD CONSTRAINT deployment_model_catalog_scope FOREIGN KEY(tenant_id,workspace_id,model_id,model_kind,catalog_digest) REFERENCES model_versions(tenant_id,workspace_id,id,kind,catalog_digest);
