ALTER TABLE sessions ADD INDEX sessions_user_live_idx(user_id,revoked_at), ALGORITHM=INPLACE, LOCK=NONE;
