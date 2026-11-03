ALTER TABLE audit_events ADD INDEX audit_operation_lookup_idx(request_id,actor_user_id,audience,tenant_id), ALGORITHM=INPLACE, LOCK=NONE;
