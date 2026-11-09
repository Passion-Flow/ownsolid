-- Run with the migration owner's transaction and installation lock.
LOCK TABLE artifact_backup_holds IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT FROM artifact_backup_holds WHERE generation > 0) THEN
        RAISE EXCEPTION 'OBJECT_BACKUP_DOWNGRADE_UNSAFE' USING ERRCODE = '55000';
    END IF;
END
$$;

DROP TABLE artifact_backup_holds;
DROP TABLE artifact_store_bindings;
