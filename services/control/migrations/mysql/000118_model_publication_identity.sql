ALTER TABLE model_versions
    DROP CHECK model_versions_chk_3,
    ADD COLUMN kind VARCHAR(24) NOT NULL DEFAULT 'approved_base',
    ADD CONSTRAINT model_version_kind_check CHECK(kind IN('approved_base','merged_lora')),
    ADD CONSTRAINT model_version_publication_state CHECK((kind='approved_base' AND state IN('ready','deleted')) OR (kind='merged_lora' AND state IN('staged','ready','deleted'))),
    ADD CONSTRAINT model_version_catalog_identity UNIQUE(tenant_id,workspace_id,id,catalog_digest),
    ADD CONSTRAINT model_version_kind_catalog_identity UNIQUE(tenant_id,workspace_id,id,kind,catalog_digest);
