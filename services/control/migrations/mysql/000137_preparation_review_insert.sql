CREATE TRIGGER model_preparation_review_insert BEFORE INSERT ON model_preparations FOR EACH ROW
BEGIN
    IF NEW.state IN('approved','rejected') OR NEW.review_id IS NOT NULL OR NEW.derived_model_id IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_REVIEW_BINDING_INVALID';
    END IF;
END;
