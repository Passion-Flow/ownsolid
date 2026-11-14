CREATE TRIGGER knowledge_source_retirement BEFORE UPDATE ON knowledge_source_versions FOR EACH ROW
BEGIN
 IF OLD.superseded_at IS NOT NULL OR NEW.superseded_at IS NULL OR NOT ((NEW.id <=> OLD.id) AND (NEW.tenant_id <=> OLD.tenant_id) AND (NEW.workspace_id <=> OLD.workspace_id) AND (NEW.base_id <=> OLD.base_id) AND (NEW.document_id <=> OLD.document_id) AND (NEW.source_version <=> OLD.source_version) AND (NEW.content <=> OLD.content) AND (NEW.sha256 <=> OLD.sha256) AND (NEW.created_by <=> OLD.created_by) AND (NEW.created_at <=> OLD.created_at)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_SOURCE_IMMUTABLE';
 END IF;
END;
