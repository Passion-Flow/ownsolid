-- Apply in the migration transaction. An accrued ledger must remain available.
LOCK TABLE usage_ledger IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM usage_ledger) THEN
    RAISE EXCEPTION 'METERING_LEDGER_NOT_EMPTY' USING ERRCODE = '55000';
  END IF;
END;
$$;

DROP TABLE usage_ledger;
DROP FUNCTION guard_usage_ledger_write();
