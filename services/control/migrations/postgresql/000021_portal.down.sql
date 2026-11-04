-- Forward cumulative M15: preserve the exact twenty-entry M14 prefix.
-- Used-state downgrade requires a verified compatible backup, never data loss.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM terminal_notifications) OR EXISTS(SELECT 1 FROM user_preferences WHERE theme<>'light' OR revision<>1)
 THEN RAISE EXCEPTION 'PORTAL_DOWNGRADE_REQUIRES_VERIFIED_BACKUP';END IF;
END;$$;
DROP INDEX application_conversation_updated_idx;
DROP TRIGGER workflow_terminal_notification ON workflow_runs;
DROP FUNCTION create_terminal_notification();
DROP TABLE terminal_notifications;
DROP FUNCTION require_terminal_notification_provenance();
DROP FUNCTION protect_terminal_notification();
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_run_recipient_identity;
DROP TRIGGER user_preferences_initialize ON users;
DROP FUNCTION initialize_user_preferences();
DROP TABLE user_preferences;
DROP FUNCTION protect_user_preferences();
