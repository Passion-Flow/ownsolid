CREATE TRIGGER artifact_grants_update BEFORE UPDATE ON artifact_object_grants FOR EACH ROW
BEGIN
 IF NOT(CAST(NEW.id AS BINARY) <=> CAST(OLD.id AS BINARY)) OR
    NOT(CAST(NEW.tenant_id AS BINARY) <=> CAST(OLD.tenant_id AS BINARY)) OR
    NOT(CAST(NEW.workspace_id AS BINARY) <=> CAST(OLD.workspace_id AS BINARY)) OR
    NOT(CAST(NEW.object_id AS BINARY) <=> CAST(OLD.object_id AS BINARY)) OR
    NOT(CAST(NEW.recipient_user_id AS BINARY) <=> CAST(OLD.recipient_user_id AS BINARY)) OR
    NOT(CAST(NEW.created_by AS BINARY) <=> CAST(OLD.created_by AS BINARY)) OR
    NOT(NEW.creator_epoch <=> OLD.creator_epoch) OR
    NOT(CAST(NEW.creator_membership AS BINARY) <=> CAST(OLD.creator_membership AS BINARY)) OR
    NOT(NEW.recipient_epoch <=> OLD.recipient_epoch) OR
    NOT(CAST(NEW.recipient_membership AS BINARY) <=> CAST(OLD.recipient_membership AS BINARY)) OR
    NOT(CAST(NEW.action AS BINARY) <=> CAST(OLD.action AS BINARY)) OR
    NOT(CAST(NEW.creation_key AS BINARY) <=> CAST(OLD.creation_key AS BINARY)) OR
    NOT(CAST(NEW.fingerprint AS BINARY) <=> CAST(OLD.fingerprint AS BINARY)) OR
    NOT(CAST(NEW.not_before AS BINARY) <=> CAST(OLD.not_before AS BINARY)) OR
    NOT(NEW.expires_at <=> OLD.expires_at) OR
    NOT(NEW.created_at <=> OLD.created_at) OR OLD.revoked_at IS NOT NULL AND NOT(NEW.revoked_at <=> OLD.revoked_at) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
END;