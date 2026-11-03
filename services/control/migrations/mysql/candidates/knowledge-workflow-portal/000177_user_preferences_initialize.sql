CREATE TRIGGER user_preferences_initialize AFTER INSERT ON users FOR EACH ROW
BEGIN
 INSERT INTO user_preferences(user_id) VALUES(NEW.id);
END;
