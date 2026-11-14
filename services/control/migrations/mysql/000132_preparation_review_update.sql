CREATE TRIGGER model_preparation_review_update BEFORE UPDATE ON model_preparations FOR EACH ROW
BEGIN
    IF OLD.state IN('approved','rejected') THEN
        IF NOT(NEW.state<=>OLD.state) OR NOT(NEW.review_id<=>OLD.review_id) OR NOT(NEW.derived_model_id<=>OLD.derived_model_id)
            OR NOT(NEW.current_attempt_id<=>OLD.current_attempt_id) OR NOT(NEW.attempt_number<=>OLD.attempt_number)
            OR NOT(NEW.current_node_id<=>OLD.current_node_id) OR NOT(NEW.revision<=>OLD.revision) OR NOT(NEW.pending_recovery_id<=>OLD.pending_recovery_id) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_DECISION_IMMUTABLE';
        END IF;
    ELSEIF NEW.state IN('approved','rejected') THEN
        IF OLD.state<>'awaiting_review' OR NEW.revision<>OLD.revision+1 OR NOT(NEW.current_attempt_id<=>OLD.current_attempt_id)
            OR NEW.pending_recovery_id IS NOT NULL OR NOT EXISTS(
            SELECT 1 FROM model_preparation_reviews r WHERE r.tenant_id=NEW.tenant_id AND r.workspace_id=NEW.workspace_id
              AND r.id=NEW.review_id AND r.preparation_id=NEW.id AND r.attempt_id=NEW.current_attempt_id
              AND r.expected_revision=OLD.revision
              AND ((NEW.state='approved' AND r.decision='approve' AND r.model_id=NEW.derived_model_id AND EXISTS(
                  SELECT 1 FROM model_derivations d JOIN model_versions m ON m.tenant_id=d.tenant_id AND m.workspace_id=d.workspace_id AND m.id=d.model_id
                  WHERE d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND d.model_id=NEW.derived_model_id
                    AND d.review_id=r.id AND d.preparation_id=NEW.id AND d.attempt_id=NEW.current_attempt_id
                    AND d.base_model_id=NEW.model_id AND m.kind='merged_lora' AND m.state='staged'))
                OR (NEW.state='rejected' AND r.decision='reject' AND r.model_id IS NULL AND NEW.derived_model_id IS NULL))
        ) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_REVIEW_BINDING_INVALID';
        END IF;
    END IF;
END;
