CREATE TRIGGER knowledge_base_initialization_guard BEFORE UPDATE ON knowledge_bases FOR EACH ROW
BEGIN
 IF OLD.initialization_state='ready' AND NEW.initialization_state<>'ready' THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_BASE_INITIALIZATION_IMMUTABLE';
 END IF;
END;
