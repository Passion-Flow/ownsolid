CREATE TRIGGER knowledge_generation_identity BEFORE UPDATE ON knowledge_generations FOR EACH ROW
BEGIN
 IF NOT ((NEW.id <=> OLD.id) AND (NEW.tenant_id <=> OLD.tenant_id) AND (NEW.workspace_id <=> OLD.workspace_id) AND (NEW.base_id <=> OLD.base_id) AND (NEW.catalog_sha256 <=> OLD.catalog_sha256) AND (NEW.dimension <=> OLD.dimension) AND (NEW.chunk_tokens <=> OLD.chunk_tokens) AND (NEW.overlap_tokens <=> OLD.overlap_tokens) AND (NEW.requested_by <=> OLD.requested_by) AND (NEW.requested_audience <=> OLD.requested_audience) AND (NEW.requested_epoch <=> OLD.requested_epoch) AND (NEW.membership_authority <=> OLD.membership_authority) AND (NEW.expected_documents <=> OLD.expected_documents) AND (NEW.created_at <=> OLD.created_at)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_GENERATION_IMMUTABLE';
 END IF;
END;
