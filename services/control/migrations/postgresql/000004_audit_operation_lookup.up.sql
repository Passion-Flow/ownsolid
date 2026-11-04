CREATE INDEX audit_operation_lookup_idx ON audit_events(request_id,actor_user_id,audience,tenant_id);
