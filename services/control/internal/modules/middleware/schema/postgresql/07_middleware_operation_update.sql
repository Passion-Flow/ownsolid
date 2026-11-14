CREATE TRIGGER middleware_operation_update BEFORE UPDATE OR DELETE ON middleware_operations FOR EACH ROW EXECUTE FUNCTION middleware_operation_immutable();
