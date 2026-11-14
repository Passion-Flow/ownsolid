ALTER TABLE model_preparations
    ADD CONSTRAINT model_preparation_current_attempt_fk FOREIGN KEY(tenant_id,workspace_id,id,current_attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
    ADD CONSTRAINT model_preparation_pending_recovery_fk FOREIGN KEY(tenant_id,workspace_id,id,pending_recovery_id) REFERENCES model_preparation_recoveries(tenant_id,workspace_id,preparation_id,id);
