package deployments

import (
	"context"
	"database/sql"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
)

func gatewaySQL(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func (s *Service) gatewayDeploymentWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, deployment string) (Deployment, error) {
	if s == nil || s.Nodes == nil || s.Nodes.Identity == nil || s.Models == nil || s.Models.Identity != s.Nodes.Identity || s.Models.Workspaces != s.Nodes.Workspaces || ctx == nil || q == nil {
		return Deployment{}, fail("GATEWAY_STORAGE_MISMATCH")
	}
	store, e := s.Nodes.Identity.Repository()
	if e != nil {
		return Deployment{}, e
	}
	if store.Dialect() != q.Dialect() {
		return Deployment{}, fail("GATEWAY_STORAGE_MISMATCH")
	}
	if _, pool := q.(transaction.Store); pool {
		return Deployment{}, fail("GATEWAY_TRANSACTION_REQUIRED")
	}
	if !id(tenant) || !id(workspace) || !id(node) || !id(deployment) {
		return Deployment{}, fail("RESOURCE_NOT_FOUND")
	}
	if _, e = s.Nodes.PreparationHistoryWithin(ctx, q, tenant, workspace, node); e != nil {
		return Deployment{}, e
	}
	age := s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds
	pg := `SELECT d.id::text,d.workspace_id::text,d.node_id::text,d.model_id::text,d.gpu_uuid,d.desired_state,d.observed_state,d.revision,d.observation,d.observed_at,d.last_task_id::text,t.state,t.error_code,t.deadline_at,g.state,(n.status<>'active' OR n.last_heartbeat_at IS NULL OR n.last_heartbeat_at<clock_timestamp()-make_interval(secs=>$5) OR d.observed_at IS NULL OR COALESCE((d.observation->>'revision')::bigint,0)<>d.revision OR d.observed_at<clock_timestamp()-make_interval(secs=>$5)),d.created_at,d.removed_at,d.model_kind,d.catalog_digest,d.serving_recipe_sha256 FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id AND g.tenant_id=d.tenant_id AND g.workspace_id=d.workspace_id AND g.node_id=d.node_id AND g.gpu_uuid=d.gpu_uuid AND g.revision=d.revision JOIN managed_nodes n ON n.tenant_id=d.tenant_id AND n.workspace_id=d.workspace_id AND n.id=d.node_id LEFT JOIN node_tasks t ON t.tenant_id=d.tenant_id AND t.workspace_id=d.workspace_id AND t.node_id=d.node_id AND t.id=d.last_task_id WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.node_id=$3 AND d.id=$4 FOR UPDATE OF d,g`
	my := `SELECT d.id,d.workspace_id,d.node_id,d.model_id,d.gpu_uuid,d.desired_state,d.observed_state,d.revision,d.observation,d.observed_at,d.last_task_id,t.state,t.error_code,t.deadline_at,g.state,(n.status<>'active' OR n.last_heartbeat_at IS NULL OR n.last_heartbeat_at<TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6)) OR d.observed_at IS NULL OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(d.observation,'$.revision')) AS UNSIGNED),0)<>d.revision OR d.observed_at<TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6))),d.created_at,d.removed_at,d.model_kind,d.catalog_digest,d.serving_recipe_sha256 FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id AND g.tenant_id=d.tenant_id AND g.workspace_id=d.workspace_id AND g.node_id=d.node_id AND g.gpu_uuid=d.gpu_uuid AND g.revision=d.revision JOIN managed_nodes n ON n.tenant_id=d.tenant_id AND n.workspace_id=d.workspace_id AND n.id=d.node_id LEFT JOIN node_tasks t ON t.tenant_id=d.tenant_id AND t.workspace_id=d.workspace_id AND t.node_id=d.node_id AND t.id=d.last_task_id WHERE d.tenant_id=? AND d.workspace_id=? AND d.node_id=? AND d.id=? FOR UPDATE`
	statement := transaction.SQL(transaction.PostgreSQL, pg, tenant, workspace, node, deployment, age)
	if q.Dialect() == transaction.MySQL {
		statement = transaction.SQL(transaction.MySQL, my, age, age, tenant, workspace, node, deployment)
	}
	if q.Dialect() == transaction.Dameng {
		if e := lockNativeGatewayIdentity(ctx, q, tenant, workspace, node, deployment); e != nil {
			return Deployment{}, e
		}
		statement = nativeGatewayDeployment(tenant, workspace, node, deployment, age)
	}
	value, e := scan(q.QueryRow(ctx, statement))
	if e != nil {
		return Deployment{}, e
	}
	if q.Dialect() == transaction.Dameng && (value.Observation == nil || value.Observation.Revision != value.Revision) {
		value.Stale = true
	}
	if value.Observation != nil {
		if value.ObservedAt == nil {
			return Deployment{}, fail("MODEL_ALIAS_UNAVAILABLE")
		}
		envelope := wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: value.Observation, ObservedAt: *value.ObservedAt}
		if envelope.Validate() != nil {
			return Deployment{}, fail("MODEL_ALIAS_UNAVAILABLE")
		}
	}
	return value, nil
}
func (s *Service) ReadyGatewayTargetWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, deployment string, revision int64) (GatewayTarget, error) {
	value, e := s.gatewayDeploymentWithin(ctx, q, tenant, workspace, node, deployment)
	if e != nil {
		return GatewayTarget{}, e
	}
	if value.Revision != revision || value.DesiredState != "running" || value.ObservedState != "running" || value.Stale || value.RemovedAt != nil || value.ResourceState != "held" || value.Observation == nil || !value.Observation.Ready || value.Observation.Verification == nil {
		return GatewayTarget{}, fail("MODEL_ALIAS_UNAVAILABLE")
	}
	o := value.Observation
	if o.ModelID != value.ModelID || o.DeploymentID != value.ID || o.GPUUUID != value.GPUUUID || !matchesStoredServing(value, *o) || o.Revision != value.Revision || !o.Managed || o.State != "running" || o.ErrorCode != "" || o.Verification.Validate() != nil {
		return GatewayTarget{}, fail("MODEL_ALIAS_UNAVAILABLE")
	}
	var usable bool
	e = q.QueryRow(ctx, gatewayNativeSQL(q, `SELECT CASE WHEN EXECUTION_PROFILE IN (?,?,?) AND INFERENCE_FENCED_AT IS NULL AND STATUS='active' THEN 1 ELSE 0 END FROM MANAGED_NODES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`, "SELECT execution_profile IN ($4,$5,$6) AND inference_fenced_at IS NULL AND status='active' FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT execution_profile IN (?,?,?) AND inference_fenced_at IS NULL AND status='active' FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=?", gatewayProfileArgs(q, tenant, workspace, node)...)).Scan(&usable)
	if e != nil {
		return GatewayTarget{}, e
	}
	if !usable {
		return GatewayTarget{}, fail("MODEL_ALIAS_UNAVAILABLE")
	}
	if value.ModelKind == wire.DerivedModelKind {
		if value.LastTaskID == nil || value.TaskState == nil || *value.TaskState != "succeeded" {
			return GatewayTarget{}, fail("MODEL_ALIAS_UNAVAILABLE")
		}
		_, e = s.Models.DecodeDerivedVersionWithin(ctx, q, tenant, workspace, value.ModelID)
	} else {
		e = s.Models.ReadyWithin(ctx, q, tenant, workspace, value.ModelID)
	}
	if e != nil {
		return GatewayTarget{}, e
	}
	return GatewayTarget{ID: value.ID, Revision: value.Revision, ModelID: value.ModelID, BackendModel: wire.InferenceModelName}, nil
}
func gatewayProfileArgs(q transaction.Executor, tenant, workspace, node string) []any {
	if q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng {
		return []any{wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile, tenant, workspace, node}
	}
	return []any{tenant, workspace, node, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile}
}
func (s *Service) ConfirmGatewayStopWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, deployment string, revision int64, after time.Time) error {
	value, e := s.gatewayDeploymentWithin(ctx, q, tenant, workspace, node, deployment)
	if e != nil {
		return e
	}
	if q.Dialect() == transaction.Dameng && !gatewayNativeTime(after) {
		return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
	}
	if after.IsZero() || value.Revision != revision || value.ResourceState == "recovery_required" {
		return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
	}
	var removed bool
	var hash *string
	e = q.QueryRow(ctx, gatewayNativeSQL(q, `SELECT CASE WHEN N.STATUS='removed' AND N.CREDENTIAL_HASH IS NULL AND N.INFERENCE_FENCED_AT IS NOT NULL THEN 1 ELSE 0 END,D.RECOVERY_RECEIPT_SHA256 FROM MANAGED_NODES N JOIN DEPLOYMENTS D ON D.TENANT_ID=N.TENANT_ID AND D.WORKSPACE_ID=N.WORKSPACE_ID AND D.NODE_ID=N.ID WHERE N.TENANT_ID=? AND N.WORKSPACE_ID=? AND N.ID=? AND D.ID=?`, `SELECT n.status='removed' AND n.credential_hash IS NULL AND n.inference_fenced_at IS NOT NULL,d.recovery_receipt_sha256 FROM managed_nodes n JOIN deployments d ON d.tenant_id=n.tenant_id AND d.workspace_id=n.workspace_id AND d.node_id=n.id WHERE n.tenant_id=$1 AND n.workspace_id=$2 AND n.id=$3 AND d.id=$4`, `SELECT n.status='removed' AND n.credential_hash IS NULL AND n.inference_fenced_at IS NOT NULL,d.recovery_receipt_sha256 FROM managed_nodes n JOIN deployments d ON d.tenant_id=n.tenant_id AND d.workspace_id=n.workspace_id AND d.node_id=n.id WHERE n.tenant_id=? AND n.workspace_id=? AND n.id=? AND d.id=?`, tenant, workspace, node, deployment)).Scan(&removed, &hash)
	if e != nil {
		return e
	}
	if removed {
		if value.DesiredState != "removed" || value.ObservedState != "not_installed" || value.RemovedAt == nil || value.ResourceState != "released" || hash == nil {
			return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
		}
		var accepted bool
		e = q.QueryRow(ctx, gatewayNativeSQL(q, `SELECT CASE WHEN EXISTS(SELECT 1 FROM AUDIT_EVENTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND TARGET_ID=? AND ACTION='inference.removal_reconciled' AND AUDIENCE='operator' AND OCCURRED_AT>? AND JSON_VALUE(METADATA,'$.receipt_sha256')=?) THEN 1 ELSE 0 END FROM DUAL`, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND target_id=$3 AND action='inference.removal_reconciled' AND audience='operator' AND occurred_at>$4 AND metadata->>'receipt_sha256'=$5)`, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE tenant_id=? AND workspace_id=? AND target_id=? AND action='inference.removal_reconciled' AND audience='operator' AND occurred_at>? AND JSON_UNQUOTE(JSON_EXTRACT(metadata,'$.receipt_sha256'))=?)`, tenant, workspace, deployment, after, *hash)).Scan(&accepted)
		if e != nil {
			return e
		}
		if !accepted {
			return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
		}
		return nil
	}
	o := value.Observation
	if value.Stale || o == nil || o.Ready || o.DeploymentID != value.ID || o.ModelID != value.ModelID || o.GPUUUID != value.GPUUUID || !matchesStoredServing(value, *o) || o.Revision != value.Revision || o.ErrorCode != "" || value.LastTaskID == nil || value.TaskState == nil || *value.TaskState != "succeeded" {
		return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
	}
	stopped := value.DesiredState == "stopped" && value.ObservedState == "stopped" && o.State == "stopped" && o.Managed && value.ResourceState == "released"
	uninstalled := value.DesiredState == "removed" && value.ObservedState == "not_installed" && o.State == "not_installed" && !o.Managed && value.ResourceState == "released" && value.RemovedAt != nil
	if !stopped && !uninstalled {
		return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
	}
	var fresh bool
	e = q.QueryRow(ctx, gatewayNativeSQL(q, `SELECT CASE WHEN STATE='succeeded' AND OPERATION IN(?,?,?) AND JSON_VALUE(PAYLOAD,'$.action') IN('stop','remove') AND CREATED_AT>? AND COMPLETED_AT>=CREATED_AT THEN 1 ELSE 0 END FROM NODE_TASKS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND NODE_ID=?`, `SELECT state='succeeded' AND operation IN($6,$7,$8) AND payload->>'action' IN('stop','remove') AND created_at>$3 AND completed_at>=created_at FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND id=$4 AND node_id=$5`, `SELECT state='succeeded' AND operation IN(?,?,?) AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.action')) IN('stop' COLLATE utf8mb4_bin,'remove' COLLATE utf8mb4_bin) AND created_at>? AND completed_at>=created_at FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND id=? AND node_id=?`, gatewayStopTaskArgs(q, tenant, workspace, after, *value.LastTaskID, node)...)).Scan(&fresh)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
	}
	if e != nil {
		return e
	}
	if !fresh {
		return fail("GATEWAY_BACKEND_STOP_UNCONFIRMED")
	}
	return nil
}
func gatewayStopTaskArgs(q transaction.Executor, tenant, workspace string, after time.Time, task, node string) []any {
	if q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng {
		return []any{wire.InferenceOperation("stop"), wire.InferenceOperation("remove"), wire.DerivedInferenceOperation, after, tenant, workspace, task, node}
	}
	return []any{tenant, workspace, after, task, node, wire.InferenceOperation("stop"), wire.InferenceOperation("remove"), wire.DerivedInferenceOperation}
}
