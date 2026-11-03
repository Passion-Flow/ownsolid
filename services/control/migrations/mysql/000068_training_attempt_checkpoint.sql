ALTER TABLE training_attempts
    ADD CONSTRAINT training_attempt_resume_checkpoint_fk FOREIGN KEY(tenant_id,workspace_id,job_id,resume_checkpoint_id) REFERENCES training_checkpoints(tenant_id,workspace_id,job_id,id);
