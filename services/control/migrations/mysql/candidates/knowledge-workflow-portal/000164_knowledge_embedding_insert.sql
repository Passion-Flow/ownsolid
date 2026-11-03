CREATE TRIGGER knowledge_embedding_insert BEFORE INSERT ON knowledge_chunks FOR EACH ROW
BEGIN
 DECLARE component_index INTEGER DEFAULT 0;
 DECLARE component_value DOUBLE;
 DECLARE norm_squared DOUBLE DEFAULT 0;
 IF JSON_TYPE(NEW.embedding)<>'ARRAY' COLLATE utf8mb4_bin OR JSON_LENGTH(NEW.embedding)<>384 THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_VECTOR_INVALID';
 END IF;
 WHILE component_index<384 DO
  IF JSON_TYPE(JSON_EXTRACT(NEW.embedding,CONCAT('$[',component_index,']'))) NOT IN('INTEGER' COLLATE utf8mb4_bin,'DOUBLE' COLLATE utf8mb4_bin,'DECIMAL' COLLATE utf8mb4_bin) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_VECTOR_INVALID';
  END IF;
  SET component_value=CAST(JSON_UNQUOTE(JSON_EXTRACT(NEW.embedding,CONCAT('$[',component_index,']'))) AS DOUBLE);
  IF component_value IS NULL OR ABS(component_value)>1.001 THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_VECTOR_INVALID';
  END IF;
  SET norm_squared=norm_squared+component_value*component_value;
  SET component_index=component_index+1;
 END WHILE;
 IF NOT (ABS(SQRT(norm_squared)-1)<0.001) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='KNOWLEDGE_VECTOR_INVALID';
 END IF;
END;
