CREATE TRIGGER model_derivation_insert_guard BEFORE INSERT ON model_derivations FOR EACH ROW
BEGIN
    IF NOT EXISTS(
        SELECT 1 FROM model_versions m
        JOIN model_preparation_reviews r ON r.tenant_id=m.tenant_id AND r.workspace_id=m.workspace_id AND r.model_id=m.id
        JOIN model_preparations p ON p.tenant_id=r.tenant_id AND p.workspace_id=r.workspace_id AND p.id=r.preparation_id
        WHERE m.id=NEW.model_id AND m.tenant_id=NEW.tenant_id AND m.workspace_id=NEW.workspace_id
          AND m.kind='merged_lora' AND m.state='staged' AND m.catalog_digest=NEW.catalog_digest AND m.manifest=NEW.catalog
          AND r.id=NEW.review_id AND r.decision='approve' AND r.artifact_id=NEW.artifact_id
          AND r.preparation_id=NEW.preparation_id AND r.attempt_id=NEW.attempt_id AND r.report_sha256=NEW.report_sha256
          AND r.reviewed_by=m.created_by AND r.model_name=m.name
          AND p.state='awaiting_review' AND p.current_attempt_id=NEW.attempt_id AND p.model_id=NEW.base_model_id
          AND p.revision=r.expected_revision
    ) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_DERIVED_REVIEW_GRAPH_REQUIRED';
    END IF;
END;
