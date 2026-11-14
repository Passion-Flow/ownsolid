-- Finished tasks get an append table (ARC-012, ADR 0029). node_tasks cannot be partitioned: its rows
-- change, other tables refer to them and one partial unique index keeps one running task per node.
-- A trigger writes the history, so no path that ends a task can miss it, and finished rows nothing
-- refers to then leave node_tasks. The columns are those the task list shows.
CREATE TABLE node_task_history (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 operation varchar(64) NOT NULL,
 state varchar(16) NOT NULL CHECK (state IN ('succeeded','failed','cancelled')),
 attempt integer NOT NULL,
 error_code varchar(64),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 deadline_at timestamptz NOT NULL,
 completed_at timestamptz NOT NULL,
 PRIMARY KEY (id,completed_at)
) PARTITION BY RANGE (completed_at);
CREATE INDEX node_task_history_list_idx ON node_task_history(tenant_id,workspace_id,node_id,created_at DESC,id DESC);

-- Tasks that finished before this migration land in the first partition.
CREATE TABLE node_task_history_p000000 (LIKE node_task_history INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
DO $$
BEGIN
 EXECUTE format('ALTER TABLE node_task_history ATTACH PARTITION node_task_history_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month');
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'node_task_history', 3, 0);
INSERT INTO node_task_history(site_id,cell_id,id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at)
 SELECT site_id,cell_id,id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at FROM node_tasks WHERE completed_at IS NOT NULL;

CREATE FUNCTION record_node_task_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.completed_at IS NULL OR (TG_OP='UPDATE' AND OLD.completed_at IS NOT NULL) THEN
  RETURN NULL;
 END IF;
 INSERT INTO node_task_history(site_id,cell_id,id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at)
  VALUES(NEW.site_id,NEW.cell_id,NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.operation,NEW.state,NEW.attempt,NEW.error_code,NEW.created_at,NEW.updated_at,NEW.deadline_at,NEW.completed_at);
 RETURN NULL;
END$$;
CREATE TRIGGER node_task_finished AFTER INSERT OR UPDATE OF state ON node_tasks FOR EACH ROW EXECUTE FUNCTION record_node_task_history();

-- Pruning walks finished tasks in order, and deleting one checks what refers to it.
CREATE INDEX node_tasks_finished_idx ON node_tasks(completed_at,id) WHERE completed_at IS NOT NULL;
CREATE INDEX managed_components_last_task_idx ON managed_components(last_task_id) WHERE last_task_id IS NOT NULL;
CREATE INDEX deployments_last_task_idx ON deployments(last_task_id) WHERE last_task_id IS NOT NULL;
CREATE INDEX model_preparation_attempts_task_idx ON model_preparation_attempts(task_id);
CREATE INDEX task_event_outbox_task_idx ON task_event_outbox(task_id);
CREATE INDEX image_registry_grants_task_idx ON image_registry_grants(task_id);
