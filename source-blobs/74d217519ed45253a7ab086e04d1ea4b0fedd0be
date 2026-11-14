ALTER TABLE training_jobs
    ADD CONSTRAINT training_job_current_attempt_fk FOREIGN KEY(tenant_id,workspace_id,id,current_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
    ADD CONSTRAINT training_job_accepted_artifact_fk FOREIGN KEY(tenant_id,workspace_id,id,accepted_artifact_id) REFERENCES training_artifacts(tenant_id,workspace_id,job_id,id),
    ADD CONSTRAINT training_job_pending_recovery_fk FOREIGN KEY(tenant_id,workspace_id,id,pending_recovery_id) REFERENCES training_recoveries(tenant_id,workspace_id,job_id,id);
