CREATE TRIGGER model_review_insert_guard BEFORE INSERT ON model_preparation_reviews FOR EACH ROW
BEGIN
    IF NOT EXISTS(
        SELECT 1 FROM model_preparations p
        JOIN model_preparation_attempts a ON a.tenant_id=p.tenant_id AND a.workspace_id=p.workspace_id AND a.preparation_id=p.id AND a.id=p.current_attempt_id
        JOIN model_preparation_artifacts f ON f.tenant_id=a.tenant_id AND f.workspace_id=a.workspace_id AND f.preparation_id=a.preparation_id AND f.attempt_id=a.id
        JOIN gpu_leases g ON g.tenant_id=p.tenant_id AND g.workspace_id=p.workspace_id AND g.model_preparation_id=p.id
        WHERE p.tenant_id=NEW.tenant_id AND p.workspace_id=NEW.workspace_id AND p.id=NEW.preparation_id
          AND p.state='awaiting_review' AND p.revision=NEW.expected_revision AND p.pending_recovery_id IS NULL
          AND a.id=NEW.attempt_id AND a.attempt_number=p.attempt_number AND a.node_id=p.current_node_id
          AND a.state='prepared' AND a.physical_stopped_at IS NOT NULL AND a.physical_stop_receipt IS NOT NULL AND a.physical_stop_sha256 IS NOT NULL
          AND a.outcome_sha256=NEW.report_sha256 AND f.manifest_sha256=NEW.report_sha256 AND f.id=NEW.artifact_id
          AND a.outcome_bytes=f.report_bytes AND SHA2(f.report_bytes,256)=NEW.report_sha256
          AND JSON_EXTRACT(f.report,'$.status')=CAST('"prepared"' AS JSON)
          AND JSON_EXTRACT(f.report,'$.gate_passed')=CAST('true' AS JSON)
          AND JSON_EXTRACT(f.report,'$.event_count')=a.last_event_sequence
          AND g.state='released' AND g.node_id=a.node_id AND g.gpu_uuid=p.gpu_uuid AND g.revision=a.attempt_number
    ) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_REVIEW_BINDING_INVALID';
    END IF;
END;
