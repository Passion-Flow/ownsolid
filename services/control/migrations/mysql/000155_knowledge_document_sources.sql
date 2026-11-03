ALTER TABLE knowledge_documents
 ADD CONSTRAINT knowledge_visible_source_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,visible_source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id),
 ADD CONSTRAINT knowledge_pending_source_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,pending_source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id);
