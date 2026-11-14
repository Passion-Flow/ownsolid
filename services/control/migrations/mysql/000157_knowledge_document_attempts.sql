ALTER TABLE knowledge_documents
 ADD CONSTRAINT knowledge_current_attempt_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,current_attempt_id) REFERENCES knowledge_attempts(tenant_id,workspace_id,base_id,document_id,id);
