-- Stopped isolated rollback only. Retained maintenance history must be empty.
LOCK TABLE middleware_operations, middleware_instances IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM middleware_operations) OR EXISTS(SELECT 1 FROM middleware_instances) THEN
  RAISE EXCEPTION 'MIDDLEWARE_ROLLBACK_REQUIRES_EMPTY_STATE';
 END IF;
END $$;
DROP TABLE middleware_operations;
DROP TABLE middleware_instances;
DROP FUNCTION middleware_operation_immutable();
