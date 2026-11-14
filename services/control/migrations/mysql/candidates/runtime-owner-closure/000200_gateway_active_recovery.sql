ALTER TABLE gateway_requests
    ADD CONSTRAINT gateway_request_active_recovery_scope_fk FOREIGN KEY(tenant_id,workspace_id,id,active_recovery_id) REFERENCES gateway_recoveries(tenant_id,workspace_id,request_id,id);
