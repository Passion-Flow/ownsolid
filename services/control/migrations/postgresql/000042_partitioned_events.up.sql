-- Training and model preparation events move to monthly partitions (ARC-012, ADR 0029). The key of
-- a partitioned table must hold the partition key, and (attempt_id,sequence) must stay unique, so
-- rows partition by the creation time of their attempt: it is fixed per attempt, and adding it to
-- the key weakens nothing. An attempt's events stay together, and a running attempt writes to the
-- month it started in.
ALTER TABLE training_events DROP CONSTRAINT training_events_pkey;
DROP INDEX training_event_job_page_idx;
DROP INDEX training_checkpoint_event_step_idx;
DROP TRIGGER training_event_immutable ON training_events;
ALTER TABLE training_events ADD COLUMN attempt_created_at timestamptz;
UPDATE training_events e SET attempt_created_at=a.created_at FROM training_attempts a WHERE a.id=e.attempt_id;
ALTER TABLE training_events ALTER COLUMN attempt_created_at SET NOT NULL;

ALTER TABLE model_preparation_events DROP CONSTRAINT model_preparation_events_pkey;
DROP TRIGGER model_preparation_event_immutable ON model_preparation_events;
ALTER TABLE model_preparation_events ADD COLUMN attempt_created_at timestamptz;
UPDATE model_preparation_events e SET attempt_created_at=a.created_at FROM model_preparation_attempts a WHERE a.id=e.attempt_id;
ALTER TABLE model_preparation_events ALTER COLUMN attempt_created_at SET NOT NULL;

-- Access goes through the parent, so a partition grants nothing itself.
DO $$
DECLARE
 event_table name;
 grantee_role name;
BEGIN
 FOREACH event_table IN ARRAY ARRAY['training_events','model_preparation_events'] LOOP
  FOR grantee_role IN SELECT DISTINCT r.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid=event_table::regclass AND a.grantee<>c.relowner LOOP
   EXECUTE format('REVOKE ALL ON TABLE %I FROM %I', event_table, grantee_role);
  END LOOP;
 END LOOP;
END
$$;

ALTER TABLE training_events RENAME TO training_events_p000000;
ALTER TABLE training_events_p000000 ADD PRIMARY KEY (attempt_id,sequence,attempt_created_at);
CREATE TABLE training_events (LIKE training_events_p000000 INCLUDING DEFAULTS INCLUDING CONSTRAINTS, PRIMARY KEY (attempt_id,sequence,attempt_created_at)) PARTITION BY RANGE (attempt_created_at);
ALTER TABLE training_events ADD FOREIGN KEY (tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id);
CREATE INDEX training_event_job_page_idx ON training_events(tenant_id,workspace_id,job_id,attempt_id,sequence);
CREATE UNIQUE INDEX training_checkpoint_event_step_idx ON training_events(attempt_id,global_step,attempt_created_at) WHERE kind='checkpoint';
CREATE TRIGGER training_event_immutable BEFORE UPDATE OR DELETE ON training_events FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();

ALTER TABLE model_preparation_events RENAME TO model_preparation_events_p000000;
ALTER TABLE model_preparation_events_p000000 ADD PRIMARY KEY (attempt_id,sequence,attempt_created_at);
CREATE TABLE model_preparation_events (LIKE model_preparation_events_p000000 INCLUDING DEFAULTS INCLUDING CONSTRAINTS, PRIMARY KEY (attempt_id,sequence,attempt_created_at)) PARTITION BY RANGE (attempt_created_at);
ALTER TABLE model_preparation_events ADD FOREIGN KEY (tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id);
CREATE TRIGGER model_preparation_event_immutable BEFORE UPDATE OR DELETE ON model_preparation_events FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_event();

DO $$
DECLARE
 next_month timestamptz := date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month';
BEGIN
 EXECUTE format('ALTER TABLE training_events ATTACH PARTITION training_events_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', next_month);
 EXECUTE format('ALTER TABLE model_preparation_events ATTACH PARTITION model_preparation_events_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', next_month);
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'training_events', 3, 0);
SELECT manage_time_partitions(current_schema()::text, 'model_preparation_events', 3, 0);
