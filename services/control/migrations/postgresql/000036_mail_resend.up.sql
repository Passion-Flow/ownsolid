-- Administrators send a failed email again (UX-120). The write is idempotent, like the
-- invitation writes that share this table.
ALTER TABLE idempotency_records DROP CONSTRAINT idempotency_records_operation_id_check;
ALTER TABLE idempotency_records ADD CONSTRAINT idempotency_records_operation_id_check CHECK (operation_id IN ('createInvitation','resendInvitation','resendMailDelivery'));
