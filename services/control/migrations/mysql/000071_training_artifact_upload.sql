ALTER TABLE training_artifacts
    ADD CONSTRAINT training_artifact_upload_fk FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id,id) REFERENCES training_uploads(tenant_id,workspace_id,job_id,attempt_id,id);
