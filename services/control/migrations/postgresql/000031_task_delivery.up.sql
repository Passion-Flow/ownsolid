-- Task delivery semantics (ARC-006, ADR 0011): every task of a node gets the next per-node
-- sequence, the agent acknowledges a lease before executing it, and a lease that was never
-- acknowledged returns to pending without spending an attempt.
ALTER TABLE managed_nodes ADD COLUMN task_sequence bigint NOT NULL DEFAULT 0 CHECK(task_sequence BETWEEN 0 AND 9007199254740991);
ALTER TABLE managed_nodes ADD COLUMN task_acknowledged_sequence bigint NOT NULL DEFAULT 0;
ALTER TABLE managed_nodes ADD COLUMN resync_granted_at timestamptz;
ALTER TABLE node_tasks ADD COLUMN sequence bigint;
ALTER TABLE node_tasks ADD COLUMN acknowledged_attempt integer NOT NULL DEFAULT 0;
ALTER TABLE node_tasks ADD COLUMN deliveries integer NOT NULL DEFAULT 0 CHECK(deliveries BETWEEN 0 AND 1000);

-- Existing tasks keep their order and count as delivered: they ran under the old rules.
UPDATE node_tasks t SET sequence=s.sequence,acknowledged_attempt=t.attempt,deliveries=t.attempt
 FROM (SELECT id,row_number() OVER (PARTITION BY tenant_id,workspace_id,node_id ORDER BY created_at,id) AS sequence FROM node_tasks) s
 WHERE s.id=t.id;
UPDATE managed_nodes n SET task_sequence=m.sequence,task_acknowledged_sequence=m.sequence
 FROM (SELECT tenant_id,workspace_id,node_id,max(sequence) AS sequence FROM node_tasks GROUP BY tenant_id,workspace_id,node_id) m
 WHERE (n.tenant_id,n.workspace_id,n.id)=(m.tenant_id,m.workspace_id,m.node_id);

ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_task_acknowledged_sequence_check CHECK(task_acknowledged_sequence BETWEEN 0 AND task_sequence);
ALTER TABLE node_tasks ALTER COLUMN sequence SET NOT NULL;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_sequence_check CHECK(sequence BETWEEN 1 AND 9007199254740991);
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_acknowledged_attempt_check CHECK(acknowledged_attempt BETWEEN 0 AND attempt);
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_node_sequence_key UNIQUE(tenant_id,workspace_id,node_id,sequence);

-- The sequence comes from the node row, so every insert path gets it and concurrent
-- inserts for one node serialize on that row. A caller cannot choose or change it.
CREATE FUNCTION node_task_sequence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.sequence IS DISTINCT FROM OLD.sequence THEN RAISE EXCEPTION 'NODE_TASK_SEQUENCE_IMMUTABLE'; END IF;
  RETURN NEW;
 END IF;
 UPDATE managed_nodes SET task_sequence=task_sequence+1 WHERE tenant_id=NEW.tenant_id AND workspace_id=NEW.workspace_id AND id=NEW.node_id RETURNING task_sequence INTO NEW.sequence;
 IF NEW.sequence IS NULL THEN RAISE EXCEPTION 'NODE_TASK_SEQUENCE_UNAVAILABLE'; END IF;
 NEW.acknowledged_attempt:=0;
 NEW.deliveries:=0;
 RETURN NEW;
END $$;
CREATE TRIGGER node_task_sequence BEFORE INSERT OR UPDATE ON node_tasks FOR EACH ROW EXECUTE FUNCTION node_task_sequence();

-- A training or model preparation lease that was never acknowledged never reached an
-- executing agent, so its stop authority may be bound again when the task is delivered
-- again. Once the task is acknowledged the stop authority is immutable as before.
CREATE OR REPLACE FUNCTION protect_training_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.input_inventory,NEW.resume_checkpoint_id,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.input_inventory,OLD.resume_checkpoint_id,OLD.created_at)
 OR OLD.outcome IS NOT NULL AND ROW(NEW.outcome,NEW.outcome_sha256) IS DISTINCT FROM ROW(OLD.outcome,OLD.outcome_sha256)
 OR OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash
  AND NOT (OLD.state='preparing' AND NEW.state='preparing' AND NEW.stop_token_hash IS NOT NULL AND EXISTS(SELECT 1 FROM node_tasks t WHERE t.id=OLD.task_id AND t.acknowledged_attempt=0))
 OR OLD.physical_stop_receipt IS NOT NULL AND ROW(NEW.physical_stop_receipt,NEW.physical_stop_sha256,NEW.physical_stopped_at) IS DISTINCT FROM ROW(OLD.physical_stop_receipt,OLD.physical_stop_sha256,OLD.physical_stopped_at)
 THEN RAISE EXCEPTION 'TRAINING_ATTEMPT_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;

CREATE OR REPLACE FUNCTION protect_model_preparation_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.command_sha256,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.command_sha256,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_INPUTS_IMMUTABLE'; END IF;
 IF OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash
  AND NOT (OLD.state='preparing' AND NEW.state='preparing' AND NEW.stop_token_hash IS NOT NULL AND EXISTS(SELECT 1 FROM node_tasks t WHERE t.id=OLD.task_id AND t.acknowledged_attempt=0))
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_AUTHORITY_IMMUTABLE'; END IF;
 IF OLD.physical_stopped_at IS NOT NULL AND (NEW.physical_stopped_at,NEW.physical_stop_receipt,NEW.physical_stop_sha256) IS DISTINCT FROM (OLD.physical_stopped_at,OLD.physical_stop_receipt,OLD.physical_stop_sha256) THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_PROOF_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
