ALTER TABLE knowledge_bases
 ADD CONSTRAINT knowledge_active_generation_scope FOREIGN KEY(tenant_id,workspace_id,id,active_generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id),
 ADD CONSTRAINT knowledge_building_generation_scope FOREIGN KEY(tenant_id,workspace_id,id,building_generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id);
