ALTER TABLE sessions ADD INDEX sessions_user_fk_idx(user_id), DROP INDEX sessions_user_live_idx, ALGORITHM=INPLACE, LOCK=NONE;
