CREATE TRIGGER knowledge_attempt_identity BEFORE UPDATE ON knowledge_attempts FOR EACH ROW
BEGIN
 IF NOT ((NEW.id <=> OLD.id) AND (NEW.tenant_id <=> OLD.tenant_id) AND (NEW.workspace_id <=> OLD.workspace_id) AND (NEW.base_id <=> OLD.base_id) AND (NEW.document_id <=> OLD.document_id) AND (NEW.source_id <=> OLD.source_id) AND (NEW.generation_id <=> OLD.generation_id) AND (NEW.kind <=> OLD.kind) AND (NEW.attempt_number <=> OLD.attempt_number) AND (NEW.expected_document_revision <=> OLD.expected_document_revision) AND (NEW.requested_by <=> OLD.requested_by) AND (NEW.requested_audience <=> OLD.requested_audience) AND (NEW.requested_epoch <=> OLD.requested_epoch) AND (NEW.membership_authority <=> OLD.membership_authority) AND (NEW.created_at <=> OLD.created_at)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_ATTEMPT_IMMUTABLE';
 END IF;
END;
