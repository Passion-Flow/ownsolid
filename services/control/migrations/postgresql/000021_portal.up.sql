-- Forward cumulative M15: preserve the exact twenty-entry M14 prefix.
-- M15 private preferences and atomic terminal notices; no historical run backfill.
CREATE TABLE user_preferences (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 theme varchar(8) NOT NULL DEFAULT 'light' CHECK(theme IN ('light','dark')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>=1 AND revision<=9007199254740991),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO user_preferences(user_id) SELECT id FROM users;
CREATE FUNCTION initialize_user_preferences() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO user_preferences(user_id) VALUES(NEW.id);
 RETURN NEW;
END;$$;
CREATE TRIGGER user_preferences_initialize AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION initialize_user_preferences();
CREATE FUNCTION protect_user_preferences() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.user_id IS DISTINCT FROM OLD.user_id THEN RAISE EXCEPTION 'PREFERENCE_OWNER_IMMUTABLE';END IF;
 IF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'PREFERENCE_REVISION_MUST_ADVANCE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER user_preferences_revision BEFORE UPDATE ON user_preferences FOR EACH ROW EXECUTE FUNCTION protect_user_preferences();

ALTER TABLE workflow_runs ADD CONSTRAINT workflow_run_recipient_identity UNIQUE(tenant_id,workspace_id,workflow_id,id,owner_id);
CREATE TABLE terminal_notifications (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 run_id uuid NOT NULL UNIQUE,
 recipient_id uuid NOT NULL,
 outcome varchar(16) NOT NULL CHECK(outcome IN ('succeeded','failed','cancelled','interrupted')),
 error_code varchar(100) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{0,99}$'),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 read_at timestamptz CHECK(read_at IS NULL OR read_at>=occurred_at),
 CHECK((outcome='succeeded')=(error_code IS NULL)),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,run_id,recipient_id) REFERENCES workflow_runs(tenant_id,workspace_id,workflow_id,id,owner_id) ON DELETE CASCADE
);
CREATE INDEX terminal_notification_recipient_page_idx ON terminal_notifications(recipient_id,occurred_at DESC,id DESC);
CREATE INDEX terminal_notification_unread_idx ON terminal_notifications(recipient_id,occurred_at DESC,id DESC) WHERE read_at IS NULL;
CREATE INDEX terminal_notification_retention_idx ON terminal_notifications(tenant_id,occurred_at,id);
CREATE INDEX application_conversation_updated_idx ON workflow_conversations(tenant_id,workspace_id,owner_id,updated_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE FUNCTION create_terminal_notification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state IN ('pending','running') AND NEW.state IN ('succeeded','failed','cancelled','interrupted') THEN
  INSERT INTO terminal_notifications(tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code)
  VALUES(NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.id,NEW.owner_id,NEW.state,NEW.error_code)
  ON CONFLICT(run_id) DO NOTHING;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER workflow_terminal_notification AFTER UPDATE OF state ON workflow_runs FOR EACH ROW EXECUTE FUNCTION create_terminal_notification();
CREATE FUNCTION protect_terminal_notification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.run_id,NEW.recipient_id,NEW.outcome,NEW.error_code,NEW.occurred_at)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.workflow_id,OLD.run_id,OLD.recipient_id,OLD.outcome,OLD.error_code,OLD.occurred_at)
 THEN RAISE EXCEPTION 'NOTIFICATION_PROVENANCE_IMMUTABLE';END IF;
 IF OLD.read_at IS NOT NULL AND NEW.read_at IS DISTINCT FROM OLD.read_at THEN RAISE EXCEPTION 'NOTIFICATION_READ_TIME_IMMUTABLE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER terminal_notification_immutable BEFORE UPDATE ON terminal_notifications FOR EACH ROW EXECUTE FUNCTION protect_terminal_notification();

CREATE FUNCTION require_terminal_notification_provenance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM terminal_notifications WHERE id=NEW.id) THEN RETURN NEW;END IF;
 IF NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=NEW.tenant_id AND r.workspace_id=NEW.workspace_id AND r.workflow_id=NEW.workflow_id AND r.id=NEW.run_id AND r.owner_id=NEW.recipient_id AND r.state=NEW.outcome AND r.error_code IS NOT DISTINCT FROM NEW.error_code AND r.finished_at IS NOT NULL)
 THEN RAISE EXCEPTION 'NOTIFICATION_RUN_PROVENANCE_REQUIRED';END IF;
 RETURN NEW;
END;$$;
CREATE CONSTRAINT TRIGGER terminal_notification_provenance AFTER INSERT OR UPDATE ON terminal_notifications DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_terminal_notification_provenance();
