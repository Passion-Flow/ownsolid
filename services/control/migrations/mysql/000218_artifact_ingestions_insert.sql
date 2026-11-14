CREATE TRIGGER artifact_ingestions_insert BEFORE INSERT ON artifact_object_ingestions FOR EACH ROW
BEGIN
 IF NEW.state<>'intent' OR NEW.model_import_id IS NOT NULL OR NEW.dataset_draft_id IS NOT NULL OR NEW.source_verified_sha256 IS NOT NULL OR NEW.source_verified_at IS NOT NULL OR NEW.receipt IS NOT NULL OR NEW.error_code IS NOT NULL THEN
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