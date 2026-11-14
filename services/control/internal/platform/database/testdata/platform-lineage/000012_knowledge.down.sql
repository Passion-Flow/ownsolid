-- Roll back only the isolated product schema. Preserve the operator-owned shared extension.
DROP TABLE knowledge_mutation_intents;
DROP TABLE knowledge_chunks;
ALTER TABLE knowledge_documents DROP CONSTRAINT knowledge_current_attempt_scope;
DROP TABLE knowledge_attempts;
ALTER TABLE knowledge_documents DROP CONSTRAINT knowledge_pending_source_scope;
ALTER TABLE knowledge_documents DROP CONSTRAINT knowledge_visible_source_scope;
DROP TABLE knowledge_source_versions;
DROP TABLE knowledge_documents;
ALTER TABLE knowledge_bases DROP CONSTRAINT knowledge_building_generation_scope;
ALTER TABLE knowledge_bases DROP CONSTRAINT knowledge_active_generation_scope;
DROP TABLE knowledge_generations;
DROP TABLE knowledge_bases;
DROP FUNCTION knowledge_generation_identity();
DROP FUNCTION knowledge_immutable_source();
DROP FUNCTION knowledge_attempt_identity();
DROP FUNCTION knowledge_source_retirement();
