-- Refuse stale snapshots before acquiring the retention fence.
SET TRANSACTION ISOLATION LEVEL READ COMMITTED;
LOCK TABLE dataset_drafts, dataset_draft_import_candidates IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM dataset_draft_import_candidates) THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE = 'DATASET_DRAFT_IMPORT_DOWNGRADE_REQUIRES_RESTORE',
            HINT = 'Restore the predecessor backup instead of dropping retained import receipts.';
    END IF;
END
$$;
DROP TABLE dataset_draft_import_candidates;
ALTER TABLE dataset_drafts DROP CONSTRAINT dataset_draft_import_scope_key;
