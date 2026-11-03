CREATE TRIGGER model_publication_apply AFTER INSERT ON model_publications FOR EACH ROW
BEGIN
    UPDATE model_versions SET state='ready' WHERE tenant_id=NEW.tenant_id AND workspace_id=NEW.workspace_id AND id=NEW.model_id AND kind='merged_lora' AND state='staged';
    IF ROW_COUNT()<>1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_DERIVED_PUBLICATION_REQUIRED';
    END IF;
END;
