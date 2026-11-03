ALTER TABLE workflows
 ADD CONSTRAINT workflow_current_version_scope FOREIGN KEY(tenant_id,workspace_id,id,current_version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id);
