CREATE TRIGGER model_derived_update_guard BEFORE UPDATE ON model_versions FOR EACH ROW
BEGIN
    IF NOT(NEW.kind<=>OLD.kind) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_KIND_IMMUTABLE';
    END IF;
    IF OLD.kind='merged_lora' THEN
        IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.kind<=>OLD.kind) OR NOT(NEW.name<=>OLD.name) OR NOT(NEW.model_key<=>OLD.model_key) OR NOT(NEW.catalog_digest<=>OLD.catalog_digest) OR NOT(NEW.manifest<=>OLD.manifest) OR NOT(NEW.created_by<=>OLD.created_by) OR NOT(NEW.created_at<=>OLD.created_at) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_DERIVED_IDENTITY_IMMUTABLE';
        END IF;
        IF NOT(NEW.state=OLD.state OR (OLD.state='staged' AND NEW.state='ready') OR (OLD.state='ready' AND NEW.state='deleted')) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_DERIVED_STATE_INVALID';
        END IF;
        IF NEW.state='ready' AND (NOT EXISTS(SELECT 1 FROM model_publications x WHERE x.tenant_id=NEW.tenant_id AND x.workspace_id=NEW.workspace_id AND x.model_id=NEW.id)
            OR NOT EXISTS(SELECT 1 FROM model_derivations d
        JOIN model_preparation_reviews r ON r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id AND r.id=d.review_id
        JOIN model_preparations p ON p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id AND p.id=d.preparation_id
        WHERE d.model_id=NEW.id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id
          AND d.catalog_digest=NEW.catalog_digest AND d.catalog=NEW.manifest
          AND r.decision='approve' AND r.model_id=NEW.id AND r.artifact_id=d.artifact_id
          AND r.reviewed_by=NEW.created_by AND r.model_name=NEW.name AND r.report_sha256=d.report_sha256
          AND r.preparation_id=d.preparation_id AND r.attempt_id=d.attempt_id
          AND p.state='approved' AND p.derived_model_id=NEW.id AND p.review_id=r.id
          AND p.current_attempt_id=d.attempt_id AND p.model_id=d.base_model_id
          AND p.revision=r.expected_revision+1)) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_DERIVED_REVIEW_GRAPH_REQUIRED';
        END IF;
    END IF;
END;
