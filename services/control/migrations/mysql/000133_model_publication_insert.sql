CREATE TRIGGER model_publication_insert_guard BEFORE INSERT ON model_publications FOR EACH ROW
BEGIN
    IF NOT EXISTS(SELECT 1 FROM model_derivations d
        JOIN model_versions m ON m.tenant_id=d.tenant_id AND m.workspace_id=d.workspace_id AND m.id=d.model_id
        JOIN model_preparation_reviews r ON r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id AND r.id=d.review_id
        JOIN model_preparations p ON p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id AND p.id=d.preparation_id
        WHERE d.model_id=NEW.model_id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id
          AND d.catalog_digest=m.catalog_digest AND d.catalog=m.manifest
          AND r.decision='approve' AND r.model_id=NEW.model_id AND r.artifact_id=d.artifact_id
          AND r.reviewed_by=m.created_by AND r.model_name=m.name AND r.report_sha256=d.report_sha256
          AND r.preparation_id=d.preparation_id AND r.attempt_id=d.attempt_id
          AND p.state='approved' AND p.derived_model_id=NEW.model_id AND p.review_id=r.id
          AND p.current_attempt_id=d.attempt_id AND p.model_id=d.base_model_id
          AND p.revision=r.expected_revision+1 AND m.kind='merged_lora' AND m.state='staged') THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_DERIVED_REVIEW_GRAPH_REQUIRED';
    END IF;
END;
