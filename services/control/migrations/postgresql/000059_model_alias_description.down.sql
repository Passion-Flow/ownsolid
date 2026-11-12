-- Run in one operator transaction with serving stopped.
LOCK TABLE model_aliases, model_alias_description_intents IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM model_aliases WHERE description <> '' OR description_revision <> 1)
   OR EXISTS(SELECT 1 FROM model_alias_description_intents) THEN
  RAISE EXCEPTION 'MODEL_ALIAS_DESCRIPTION_DOWNGRADE_REQUIRES_RESTORE' USING ERRCODE='55000';
 END IF;
END $$;
DROP TABLE model_alias_description_intents;
ALTER TABLE model_aliases
 DROP CONSTRAINT model_alias_description_scope_key,
 DROP COLUMN description_revision,
 DROP COLUMN description;
