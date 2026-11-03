ALTER TABLE gpu_leases
    ADD COLUMN model_preparation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD UNIQUE KEY gpu_preparation_owner_idx(model_preparation_id),
    DROP CHECK gpu_lease_exact_owner_check,
    ADD CONSTRAINT gpu_lease_exact_owner_check CHECK((deployment_id IS NOT NULL)+(training_job_id IS NOT NULL)+(model_preparation_id IS NOT NULL)=1),
    ADD CONSTRAINT gpu_lease_preparation_scope_fk FOREIGN KEY(tenant_id,workspace_id,model_preparation_id) REFERENCES model_preparations(tenant_id,workspace_id,id),
    ADD CONSTRAINT gpu_lease_preparation_id_canonical CHECK(model_preparation_id IS NULL OR REGEXP_LIKE(model_preparation_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'));
