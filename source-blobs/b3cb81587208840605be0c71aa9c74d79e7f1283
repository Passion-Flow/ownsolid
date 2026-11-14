CREATE TRIGGER model_derived_insert_guard BEFORE INSERT ON model_versions FOR EACH ROW
BEGIN
    IF NEW.kind='merged_lora' AND NEW.state<>'staged' THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_DERIVED_STAGING_REQUIRED';
    END IF;
END;
