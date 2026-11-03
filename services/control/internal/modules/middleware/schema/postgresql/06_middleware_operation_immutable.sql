CREATE FUNCTION middleware_operation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MIDDLEWARE_HISTORY_IMMUTABLE'; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.instance_id,NEW.kind,NEW.requested_by,NEW.intent_key,NEW.intent_fingerprint,NEW.input_json,NEW.actor_json,NEW.instance_version,NEW.profile_fingerprint,NEW.request_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.instance_id,OLD.kind,OLD.requested_by,OLD.intent_key,OLD.intent_fingerprint,OLD.input_json,OLD.actor_json,OLD.instance_version,OLD.profile_fingerprint,OLD.request_id,OLD.created_at) THEN RAISE EXCEPTION 'MIDDLEWARE_INTENT_IMMUTABLE'; END IF;
 IF OLD.state IN('succeeded','failed','unknown') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'MIDDLEWARE_TERMINAL_IMMUTABLE'; END IF;
 RETURN NEW;
END $$;
