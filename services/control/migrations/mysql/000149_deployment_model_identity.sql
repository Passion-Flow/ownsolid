CREATE TRIGGER deployment_model_identity BEFORE UPDATE ON deployments FOR EACH ROW
BEGIN
    IF NOT (NEW.id<=>OLD.id AND NEW.tenant_id<=>OLD.tenant_id AND NEW.workspace_id<=>OLD.workspace_id AND NEW.node_id<=>OLD.node_id AND NEW.model_id<=>OLD.model_id AND NEW.gpu_uuid<=>OLD.gpu_uuid AND NEW.model_kind<=>OLD.model_kind AND NEW.catalog_digest<=>OLD.catalog_digest AND NEW.serving_recipe_sha256<=>OLD.serving_recipe_sha256) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='DEPLOYMENT_MODEL_IDENTITY_IMMUTABLE';
    END IF;
END;
