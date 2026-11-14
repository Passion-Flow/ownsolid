ALTER TABLE gpu_leases
    ADD COLUMN id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT (deployment_id),
    DROP PRIMARY KEY,
    MODIFY COLUMN deployment_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
    ADD PRIMARY KEY(id),
    ADD UNIQUE KEY gpu_deployment_owner_idx(deployment_id),
    ADD COLUMN training_job_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD UNIQUE KEY gpu_training_owner_idx(training_job_id),
    ADD CONSTRAINT gpu_lease_exact_owner_check CHECK((deployment_id IS NOT NULL)+(training_job_id IS NOT NULL)=1),
    ADD CONSTRAINT gpu_lease_training_scope_fk FOREIGN KEY(tenant_id,workspace_id,training_job_id) REFERENCES training_jobs(tenant_id,workspace_id,id),
    ADD CONSTRAINT gpu_lease_node_scope_fk FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
    ADD CONSTRAINT gpu_lease_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    ADD CONSTRAINT gpu_lease_training_job_canonical CHECK(training_job_id IS NULL OR REGEXP_LIKE(training_job_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'));
