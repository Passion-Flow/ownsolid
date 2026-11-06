-- Monthly partitions for append tables (ARC-012, ADR 0029). The serving role has no DDL rights, so
-- this function, owned by the migration role, creates partitions ahead and drops expired ones for a
-- fixed list of tables. It trusts no caller setting: its search_path is pinned, and the schema it is
-- given must hold both this function and a partitioned table its owner owns.
CREATE FUNCTION manage_time_partitions(owner_schema text, parent text, months_ahead integer, keep_days integer, OUT created integer, OUT dropped integer, OUT blocked integer, OUT reaches timestamptz)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog SET lock_timeout = '2s' SET timezone = 'UTC' AS $$
DECLARE
 parent_oid oid;
 this_month timestamptz := date_trunc('month', clock_timestamp());
 month_start timestamptz;
 cutoff timestamptz := clock_timestamp() - make_interval(days => keep_days);
 expired name;
 upper_bound CONSTANT text := 'TO \(''(.*)''\)';
BEGIN
 created := 0; dropped := 0; blocked := 0;
 IF parent <> ALL(ARRAY['audit_events']) THEN
  RAISE EXCEPTION 'TIME_PARTITION_TABLE_UNKNOWN';
 END IF;
 IF months_ahead < 1 OR months_ahead > 24 OR keep_days < 0 THEN
  RAISE EXCEPTION 'TIME_PARTITION_ARGUMENTS_INVALID';
 END IF;
 SELECT c.oid INTO parent_oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname=owner_schema AND c.relname=parent AND c.relkind='p' AND c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
   AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.pronamespace=n.oid AND p.proname='manage_time_partitions');
 IF parent_oid IS NULL THEN
  RAISE EXCEPTION 'TIME_PARTITION_SCOPE_INVALID';
 END IF;

 SELECT max(substring(pg_get_expr(c.relpartbound, c.oid), upper_bound)::timestamptz) INTO reaches
  FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE i.inhparent=parent_oid;
 -- Months that passed unattended take no writes, so only those from now on are created.
 month_start := greatest(reaches, this_month);
 WHILE month_start <= this_month + make_interval(months => months_ahead) LOOP
  BEGIN
   -- Attaching, unlike PARTITION OF, does not block writers of the parent.
   EXECUTE format('CREATE TABLE %I.%I (LIKE %I.%I INCLUDING DEFAULTS INCLUDING CONSTRAINTS)', owner_schema, parent || '_p' || to_char(month_start, 'YYYYMM'), owner_schema, parent);
   EXECUTE format('ALTER TABLE %I.%I ATTACH PARTITION %I.%I FOR VALUES FROM (%L) TO (%L)', owner_schema, parent, owner_schema, parent || '_p' || to_char(month_start, 'YYYYMM'), month_start, month_start + interval '1 month');
  EXCEPTION WHEN lock_not_available THEN
   blocked := blocked + 1;
   RETURN;
  END;
  created := created + 1;
  month_start := month_start + interval '1 month';
  reaches := month_start;
 END LOOP;

 IF keep_days = 0 THEN
  RETURN;
 END IF;
 FOR expired IN
  SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
   WHERE i.inhparent=parent_oid AND substring(pg_get_expr(c.relpartbound, c.oid), upper_bound)::timestamptz <= cutoff
 LOOP
  BEGIN
   EXECUTE format('DROP TABLE %I.%I', owner_schema, expired);
   dropped := dropped + 1;
  EXCEPTION WHEN lock_not_available THEN
   blocked := blocked + 1;
  END;
 END LOOP;
END
$$;

-- Existing rows stay where they are: the old table becomes the first partition and leaves with the
-- first drop past its last row. Nothing refers to this table, and it no longer refers to anything:
-- an audit row outlives what it names, each foreign key cost a statement per write, and the
-- Workspace key made renames wait for agent traffic (ADR 0020).
DO $$
DECLARE
 name_of_constraint name;
BEGIN
 FOR name_of_constraint IN SELECT conname FROM pg_constraint WHERE conrelid='audit_events'::regclass AND contype IN ('f','p') LOOP
  EXECUTE format('ALTER TABLE audit_events DROP CONSTRAINT %I', name_of_constraint);
 END LOOP;
END
$$;
ALTER TABLE audit_events RENAME TO audit_events_p000000;
ALTER INDEX audit_list_idx RENAME TO audit_events_p000000_list_idx;
ALTER INDEX audit_action_idx RENAME TO audit_events_p000000_action_idx;
ALTER INDEX audit_operation_lookup_idx RENAME TO audit_events_p000000_operation_idx;
ALTER TABLE audit_events_p000000 ADD PRIMARY KEY (id,occurred_at);
CREATE TABLE audit_events (LIKE audit_events_p000000 INCLUDING DEFAULTS INCLUDING CONSTRAINTS, PRIMARY KEY (id,occurred_at)) PARTITION BY RANGE (occurred_at);
CREATE INDEX audit_list_idx ON audit_events(occurred_at DESC, id DESC);
CREATE INDEX audit_action_idx ON audit_events(action, occurred_at DESC, id DESC);
CREATE INDEX audit_operation_lookup_idx ON audit_events(request_id,actor_user_id,audience,tenant_id);
DO $$
BEGIN
 EXECUTE format('ALTER TABLE audit_events ATTACH PARTITION audit_events_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month');
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'audit_events', 3, 0);
