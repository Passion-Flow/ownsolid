ALTER TABLE dataset_drafts
    ADD COLUMN current_job_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD COLUMN published_version_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD CONSTRAINT dataset_draft_current_job_canonical CHECK(current_job_id IS NULL OR REGEXP_LIKE(current_job_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    ADD CONSTRAINT dataset_draft_published_version_canonical CHECK(published_version_id IS NULL OR REGEXP_LIKE(published_version_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    ADD CONSTRAINT dataset_draft_current_job_fk FOREIGN KEY(tenant_id,workspace_id,dataset_id,id,current_job_id) REFERENCES dataset_jobs(tenant_id,workspace_id,dataset_id,draft_id,id),
    ADD CONSTRAINT dataset_draft_published_version_fk FOREIGN KEY(tenant_id,workspace_id,dataset_id,id,published_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,draft_id,id),
    ADD CONSTRAINT dataset_draft_parent_version_fk FOREIGN KEY(tenant_id,workspace_id,dataset_id,parent_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id);
