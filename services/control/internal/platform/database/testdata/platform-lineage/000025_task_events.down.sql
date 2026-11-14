-- Stopped isolated rollback only. Preserve all retained delivery/recovery proof.
LOCK TABLE task_event_outbox, task_event_receipts, task_event_dead_letters IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM task_event_outbox)
    OR EXISTS(SELECT 1 FROM task_event_receipts)
    OR EXISTS(SELECT 1 FROM task_event_dead_letters) THEN
  RAISE EXCEPTION 'TASK_EVENT_ROLLBACK_REQUIRES_EMPTY_STATE';
 END IF;
END $$;
DROP TABLE task_event_dead_letters;
DROP TABLE task_event_receipts;
DROP TABLE task_event_outbox;
DROP FUNCTION task_event_dead_letter_guard();
DROP FUNCTION task_event_receipt_guard();
DROP FUNCTION task_event_outbox_guard();
