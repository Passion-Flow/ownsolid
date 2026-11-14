-- What a running task reports: its steps and its log (UX-104, ADR 0030). This is an append table, so
-- it partitions by month (ADR 0029), by the creation time of its task: that is fixed per task, so a
-- task's rows stay together and adding it to the key weakens nothing. It keeps no foreign key,
-- because a task's row leaves node_tasks while its progress stays readable through the history.
CREATE TABLE node_task_progress (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 task_id uuid NOT NULL,
 task_created_at timestamptz NOT NULL,
 attempt integer NOT NULL CHECK (attempt>=1),
 sequence integer NOT NULL CHECK (sequence BETWEEN 1 AND 4129),
 kind varchar(8) NOT NULL CHECK (kind IN ('step','log')),
 step varchar(32),
 step_state varchar(16) CHECK (step_state IN ('running','succeeded','failed')),
 level varchar(8) CHECK (level IN ('info','warning','error')),
 message varchar(512),
 reported_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (task_id,attempt,sequence,task_created_at),
 CHECK ((kind='step' AND step IS NOT NULL AND step_state IS NOT NULL AND level IS NULL AND message IS NULL)
     OR (kind='log' AND step IS NULL AND step_state IS NULL AND level IS NOT NULL AND message IS NOT NULL))
) PARTITION BY RANGE (task_created_at);
-- The timeline reads a few step rows out of a log that may hold thousands of lines.
CREATE INDEX node_task_progress_steps_idx ON node_task_progress(task_id,attempt,sequence) WHERE kind='step';

-- A task created before this migration may still run, and reports into the first partition.
DO $$
BEGIN
 EXECUTE format('CREATE TABLE node_task_progress_p000000 PARTITION OF node_task_progress FOR VALUES FROM (MINVALUE) TO (%L)', date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month');
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'node_task_progress', 3, 0);

-- The task list reads every node of a tenant, newest first, live tasks and the history apart.
CREATE INDEX node_tasks_tenant_live_idx ON node_tasks(tenant_id,created_at DESC,id DESC) WHERE completed_at IS NULL;
CREATE INDEX node_tasks_requester_live_idx ON node_tasks(requested_by,created_at DESC,id DESC) WHERE completed_at IS NULL;
CREATE INDEX node_task_history_tenant_idx ON node_task_history(tenant_id,created_at DESC,id DESC);
CREATE INDEX node_task_history_tenant_state_idx ON node_task_history(tenant_id,state,created_at DESC,id DESC);
