CREATE TRIGGER artifact_ingestions_update BEFORE UPDATE ON artifact_object_ingestions FOR EACH ROW
BEGIN
 IF NOT(CAST(NEW.id AS BINARY) <=> CAST(OLD.id AS BINARY)) OR
    NOT(CAST(NEW.tenant_id AS BINARY) <=> CAST(OLD.tenant_id AS BINARY)) OR
    NOT(CAST(NEW.workspace_id AS BINARY) <=> CAST(OLD.workspace_id AS BINARY)) OR
    NOT(CAST(NEW.object_id AS BINARY) <=> CAST(OLD.object_id AS BINARY)) OR
    NOT(CAST(NEW.created_by AS BINARY) <=> CAST(OLD.created_by AS BINARY)) OR
    NOT(CAST(NEW.creation_key AS BINARY) <=> CAST(OLD.creation_key AS BINARY)) OR
    NOT(CAST(NEW.fingerprint AS BINARY) <=> CAST(OLD.fingerprint AS BINARY)) OR
    NOT(CAST(NEW.kind AS BINARY) <=> CAST(OLD.kind AS BINARY)) OR
    NOT(CAST(NEW.target_dataset_id AS BINARY) <=> CAST(OLD.target_dataset_id AS BINARY)) OR
    NOT(CAST(NEW.input AS BINARY) <=> CAST(OLD.input AS BINARY)) OR
    NOT(NEW.created_at <=> OLD.created_at) OR (OLD.model_import_id IS NOT NULL AND NOT(CAST(NEW.model_import_id AS BINARY) <=> CAST(OLD.model_import_id AS BINARY))) OR (OLD.dataset_draft_id IS NOT NULL AND NOT(CAST(NEW.dataset_draft_id AS BINARY) <=> CAST(OLD.dataset_draft_id AS BINARY))) OR (OLD.source_verified_sha256 IS NOT NULL AND (NOT(CAST(NEW.source_verified_sha256 AS BINARY) <=> CAST(OLD.source_verified_sha256 AS BINARY)) OR NOT(NEW.source_verified_at <=> OLD.source_verified_at))) OR (OLD.state IN('submitted','failed') AND (NOT(CAST(NEW.id AS BINARY) <=> CAST(OLD.id AS BINARY)) OR
    NOT(CAST(NEW.tenant_id AS BINARY) <=> CAST(OLD.tenant_id AS BINARY)) OR
    NOT(CAST(NEW.workspace_id AS BINARY) <=> CAST(OLD.workspace_id AS BINARY)) OR
    NOT(CAST(NEW.object_id AS BINARY) <=> CAST(OLD.object_id AS BINARY)) OR
    NOT(CAST(NEW.created_by AS BINARY) <=> CAST(OLD.created_by AS BINARY)) OR
    NOT(CAST(NEW.creation_key AS BINARY) <=> CAST(OLD.creation_key AS BINARY)) OR
    NOT(CAST(NEW.fingerprint AS BINARY) <=> CAST(OLD.fingerprint AS BINARY)) OR
    NOT(CAST(NEW.kind AS BINARY) <=> CAST(OLD.kind AS BINARY)) OR
    NOT(CAST(NEW.model_import_id AS BINARY) <=> CAST(OLD.model_import_id AS BINARY)) OR
    NOT(CAST(NEW.dataset_draft_id AS BINARY) <=> CAST(OLD.dataset_draft_id AS BINARY)) OR
    NOT(CAST(NEW.target_dataset_id AS BINARY) <=> CAST(OLD.target_dataset_id AS BINARY)) OR
    NOT(CAST(NEW.state AS BINARY) <=> CAST(OLD.state AS BINARY)) OR
    NOT(CAST(NEW.source_verified_sha256 AS BINARY) <=> CAST(OLD.source_verified_sha256 AS BINARY)) OR
    NOT(NEW.source_verified_at <=> OLD.source_verified_at) OR
    NOT(CAST(NEW.input AS BINARY) <=> CAST(OLD.input AS BINARY)) OR
    NOT(CAST(NEW.receipt AS BINARY) <=> CAST(OLD.receipt AS BINARY)) OR
    NOT(CAST(NEW.error_code AS BINARY) <=> CAST(OLD.error_code AS BINARY)) OR
    NOT(NEW.created_at <=> OLD.created_at) OR
    NOT(NEW.updated_at <=> OLD.updated_at))) OR NOT(NEW.state=OLD.state OR (OLD.state='intent' AND NEW.state IN('running','failed')) OR (OLD.state='running' AND NEW.state IN('submitted','failed'))) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;

 IF NEW.model_import_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM model_imports m JOIN artifact_objects o ON o.tenant_id=m.tenant_id AND o.workspace_id=m.workspace_id WHERE o.id=NEW.object_id AND m.id=NEW.model_import_id AND m.tenant_id=NEW.tenant_id AND m.workspace_id=NEW.workspace_id AND m.requested_by=NEW.created_by AND m.expected_bytes=o.expected_bytes) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
 IF NEW.dataset_draft_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dataset_drafts d JOIN artifact_objects o ON o.tenant_id=d.tenant_id AND o.workspace_id=d.workspace_id WHERE o.id=NEW.object_id AND d.id=NEW.dataset_draft_id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND d.dataset_id=NEW.target_dataset_id AND d.created_by=NEW.created_by AND d.expected_bytes=o.expected_bytes AND d.source_sha256=o.sha256) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
 IF ((NEW.source_verified_sha256 IS NULL)<>(NEW.source_verified_at IS NULL)) OR (NEW.source_verified_sha256 IS NOT NULL AND NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.sha256=NEW.source_verified_sha256)) OR (NEW.state='submitted' AND (NEW.receipt IS NULL OR NEW.source_verified_sha256 IS NULL OR NEW.source_verified_at IS NULL)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
END;