ALTER TABLE training_checkpoints
    ADD CONSTRAINT training_checkpoint_upload_fk FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id,id) REFERENCES training_uploads(tenant_id,workspace_id,job_id,attempt_id,id);
