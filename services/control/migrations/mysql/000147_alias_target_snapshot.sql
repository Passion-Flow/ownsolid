CREATE TRIGGER alias_target_snapshot BEFORE INSERT ON model_alias_target_versions FOR EACH ROW
BEGIN
    IF NOT EXISTS(
        SELECT 1 FROM model_aliases a
        JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.node_id=a.node_id AND d.id=a.deployment_id
        WHERE a.tenant_id=NEW.tenant_id AND a.workspace_id=NEW.workspace_id AND a.id=NEW.alias_id
          AND a.revision=NEW.revision AND a.deployment_revision=NEW.deployment_revision
          AND d.id=NEW.deployment_id AND d.model_id=NEW.model_id AND d.model_kind=NEW.model_kind
          AND d.catalog_digest=NEW.catalog_digest AND d.serving_recipe_sha256<=>NEW.serving_recipe_sha256
    ) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='ALIAS_CURRENT_TARGET_SNAPSHOT_REQUIRED';
    END IF;
END;
