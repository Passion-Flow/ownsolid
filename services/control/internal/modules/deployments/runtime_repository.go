package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func deploymentSQL(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func deploymentError(e error) error {
	if e == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return fail(string(kind))
	}
	if transaction.IsCancellationOnly(e) {
		if errors.Is(e, context.Canceled) {
			return fail(string(transaction.Cancelled))
		}
		return fail(string(transaction.Timeout))
	}
	if e.Error() != "" && wire.KnownInferenceError(e.Error()) {
		return fail(e.Error())
	}
	switch e.Error() {
	case "DEPLOYMENT_STORAGE_MISMATCH", "DEPLOYMENT_IDENTITY_REQUIRED", "AUTH_SESSION_EXPIRED", "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "VERSION_CONFLICT", "IDEMPOTENCY_CONFLICT", "NODE_BUSY", "NODE_UNAVAILABLE", "AGENT_PROFILE_REJECTED", "AGENT_OPERATION_UNSUPPORTED", "AGENT_LEASE_STALE", "INFERENCE_RESOURCE_BUSY", "INFERENCE_PREFLIGHT_REQUIRED", "MODEL_IDENTITY_REQUIRED", "MODEL_STORAGE_MISMATCH", "MODEL_FILE_INVALID", "MODEL_DERIVED_CATALOG_INVALID", "MODEL_SNAPSHOT_INVALID", "RESOURCE_LIMIT_REACHED", "STORAGE_UNAVAILABLE", string(transaction.Deadlock), string(transaction.LockTimeout), string(transaction.Cancelled), string(transaction.Timeout), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return fail(e.Error())
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
}
func (s *Service) runtimeRepository() (transaction.Store, error) {
	if s == nil || s.Nodes == nil || s.Models == nil || s.Nodes.Identity == nil || s.Nodes.Workspaces == nil || s.Nodes.Workspaces.Identity != s.Nodes.Identity || s.Models.Identity != s.Nodes.Identity || s.Models.Workspaces != s.Nodes.Workspaces {
		return nil, fail("DEPLOYMENT_IDENTITY_REQUIRED")
	}
	if s.Nodes.DB != nil && s.Nodes.DB != s.Nodes.Identity.DB || s.Models.DB != nil && s.Models.DB != s.Nodes.Identity.DB {
		return nil, fail("DEPLOYMENT_STORAGE_MISMATCH")
	}
	return s.Nodes.Identity.Repository()
}
func (s *Service) runtimeExecutor(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return deploymentError(e)
	}
	store, e := s.runtimeRepository()
	if e != nil {
		return e
	}
	if q.Dialect() != store.Dialect() || q.Dialect() != transaction.MySQL && q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.Dameng {
		return fail("DEPLOYMENT_STORAGE_MISMATCH")
	}
	if q.Dialect() == transaction.Dameng {
		if !nativeRuntimeOwnsExecutor(store, q) {
			return fail("DEPLOYMENT_STORAGE_MISMATCH")
		}
	}
	return nil
}
func (s *Service) runtimeActor(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil || fn == nil {
		return fail("VALIDATION_FAILED")
	}
	if a.Me.Audience != "admin" && a.Me.Audience != "portal" {
		return fail("AUTH_SESSION_EXPIRED")
	}
	store, e := s.runtimeRepository()
	if e != nil {
		return e
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Nodes.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return deploymentError(store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.runtimeExecutor(ctx, q); e != nil {
			return e
		}
		if e := s.Nodes.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		return fn(ctx, q)
	}))
}
func (s *Service) deploymentWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string, lock bool) (Deployment, error) {
	if q.Dialect() == transaction.Dameng {
		return s.nativeDeploymentWithin(ctx, q, tenant, workspace, id, lock)
	}
	pg := "SELECT " + s.fields() + joins + " WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.id=$3"
	my := "SELECT " + s.mysqlFields() + joins + " WHERE d.tenant_id=? AND d.workspace_id=? AND d.id=?"
	if lock {
		pg += " FOR UPDATE OF d,g"
		my += " FOR UPDATE"
	}
	return scan(q.QueryRow(ctx, deploymentSQL(q, pg, my, tenant, workspace, id)))
}
func (s *Service) mysqlFields() string {
	age := s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds
	// Only validated bounded integers enter this structural expression; identifiers and scope remain bound.
	return fmt.Sprintf(`d.id,d.workspace_id,d.node_id,d.model_id,d.gpu_uuid,d.desired_state,d.observed_state,d.revision,d.observation,d.observed_at,d.last_task_id,t.state,t.error_code,t.deadline_at,g.state,(n.status<>'active' OR n.last_heartbeat_at<TIMESTAMPADD(SECOND,-%d,UTC_TIMESTAMP(6)) OR d.observed_at IS NULL OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(d.observation,'$.revision')) AS UNSIGNED),0)<>d.revision OR d.observed_at<TIMESTAMPADD(SECOND,-%d,UTC_TIMESTAMP(6))),d.created_at,d.removed_at,d.model_kind,d.catalog_digest,d.serving_recipe_sha256`, age, age)
}
func (s *Service) scopedNodeWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace, node string) (nodes.Node, error) {
	if !id(node) || workspace != "" && !id(workspace) {
		return nodes.Node{}, fail("RESOURCE_NOT_FOUND")
	}
	if workspace == "" {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nodes.Node{}, fail("RESOURCE_NOT_FOUND")
		}
		e := q.QueryRow(ctx, deploymentNativeSQL(q, `SELECT N."WORKSPACE_ID" FROM "MANAGED_NODES" N JOIN "INSTALLATION_STATE" I ON I."TENANT_ID"=N."TENANT_ID" WHERE I."ID"=1 AND I."BOOTSTRAP_COMPLETE"=1 AND N."ID"=?`, `SELECT n.workspace_id::text FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1`, `SELECT n.workspace_id FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=?`, node)).Scan(&workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return nodes.Node{}, fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return nodes.Node{}, e
		}
	}
	w, e := s.Nodes.Workspaces.ReadScopeWithin(ctx, q, a, workspace)
	if e != nil {
		return nodes.Node{}, e
	}
	return s.Nodes.PreparationHistoryWithin(ctx, q, w.TenantID, w.ID, node)
}
func (s *Service) getSelected(ctx context.Context, a identity.Auth, workspace, deployment string) (Deployment, error) {
	if !id(deployment) || workspace != "" && !id(workspace) || workspace == "" && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return Deployment{}, fail("RESOURCE_NOT_FOUND")
	}
	var out Deployment
	e := s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var storedWorkspace, node string
		e := q.QueryRow(ctx, deploymentNativeSQL(q, `SELECT D."WORKSPACE_ID",D."NODE_ID" FROM "DEPLOYMENTS" D JOIN "INSTALLATION_STATE" I ON I."TENANT_ID"=D."TENANT_ID" WHERE I."ID"=1 AND I."BOOTSTRAP_COMPLETE"=1 AND D."ID"=?`, `SELECT d.workspace_id::text,d.node_id::text FROM deployments d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND d.id=$1`, `SELECT d.workspace_id,d.node_id FROM deployments d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND d.id=?`, deployment)).Scan(&storedWorkspace, &node)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if workspace != "" && workspace != storedWorkspace {
			return fail("RESOURCE_NOT_FOUND")
		}
		n, e := s.scopedNodeWithin(ctx, q, a, storedWorkspace, node)
		if e != nil {
			return e
		}
		out, e = s.deploymentWithin(ctx, q, n.TenantID, n.WorkspaceID, deployment, false)
		return e
	})
	if e != nil {
		return Deployment{}, e
	}
	return out, nil
}
func (s *Service) listSelected(ctx context.Context, a identity.Auth, workspace, model string, retained bool) ([]Deployment, error) {
	if !id(workspace) || model != "" && !id(model) {
		return nil, fail("VALIDATION_FAILED")
	}
	out := []Deployment{}
	e := s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.Nodes.Workspaces.ReadScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		pg := "SELECT " + s.fields() + joins + " WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND ($3::text='' OR d.model_id::text=$3) AND (NOT $4::boolean OR d.removed_at IS NULL) ORDER BY d.created_at DESC,d.id DESC LIMIT $5"
		my := "SELECT " + s.mysqlFields() + joins + " WHERE d.tenant_id=? AND d.workspace_id=? AND (?='' OR d.model_id=?) AND (NOT ? OR d.removed_at IS NULL) ORDER BY d.created_at DESC,d.id DESC LIMIT ?"
		statement := transaction.SQL(transaction.PostgreSQL, pg, w.TenantID, w.ID, model, retained, s.Nodes.Identity.Config.Policy().UI.LookupPageSize)
		if q.Dialect() == transaction.MySQL {
			statement = transaction.SQL(transaction.MySQL, my, w.TenantID, w.ID, model, model, retained, s.Nodes.Identity.Config.Policy().UI.LookupPageSize)
		}
		if q.Dialect() == transaction.Dameng {
			statement = nativeRuntimeList(w.TenantID, w.ID, model, retained, s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds, s.Nodes.Identity.Config.Policy().UI.LookupPageSize)
		}
		rows, e := q.Query(ctx, statement)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			d, e := runtimeScan(q, rows)
			if e != nil {
				return e
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) nodeSelected(ctx context.Context, a identity.Auth, workspace, node string) (NodeState, error) {
	var out NodeState
	e := s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		n, e := s.scopedNodeWithin(ctx, q, a, workspace, node)
		if e != nil {
			return e
		}
		out = NodeState{NodeID: n.ID, Stale: true}
		var raw []byte
		var received time.Time
		var fresh bool
		age := s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds
		statement := transaction.SQL(transaction.PostgreSQL, "SELECT observation,received_at,received_at>clock_timestamp()-make_interval(secs=>$4) FROM inference_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3", n.TenantID, n.WorkspaceID, n.ID, age)
		if q.Dialect() == transaction.MySQL {
			statement = transaction.SQL(transaction.MySQL, "SELECT observation,received_at,received_at>TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)) FROM inference_nodes WHERE tenant_id=? AND workspace_id=? AND node_id=?", -age, n.TenantID, n.WorkspaceID, n.ID)
		}
		if q.Dialect() == transaction.Dameng {
			statement = nativeRuntimeHardware(n.TenantID, n.WorkspaceID, n.ID, age, false)
		}
		e = q.QueryRow(ctx, statement).Scan(&raw, &received, &fresh)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		var observation wire.InferenceObservation
		if e = runtimeDecodeObservation(q, raw, &observation); e != nil {
			return e
		}
		if q.Dialect() == transaction.Dameng && !gatewayNativeTime(received) {
			return fail("STORAGE_UNAVAILABLE")
		}
		received = received.UTC()
		out.Observation = &observation
		out.ReceivedAt = &received
		out.Stale = n.Status != "active" || n.Connectivity != "online" || !fresh
		return nil
	})
	if e != nil {
		return NodeState{}, e
	}
	return out, nil
}
func (s *Service) preflightCommandWithin(ctx context.Context, q transaction.Executor, n nodes.Node, command wire.InferenceCommand) error {
	if e := admitNode(n); e != nil {
		return e
	}
	o, e := s.HardwareWithin(ctx, q, n)
	if e != nil {
		return hardwareRefusal(e)
	}
	if e := admitObservation(o, command); e != nil {
		return e
	}
	return admitGPU(o, command.GPUUUID, false, false)
}

// hardwareRefusal names why a node's GPU observation cannot be used.
func hardwareRefusal(e error) error {
	switch e.Error() {
	case "TRAINING_PREFLIGHT_REQUIRED":
		return fail("INFERENCE_PREFLIGHT_REQUIRED")
	case "TRAINING_DATA_INVALID":
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}

// admitNode holds the approved recipe's demands on the node itself, before its GPUs are read.
func admitNode(n nodes.Node) error {
	recipe := wire.ApprovedInferenceRecipe()
	if n.Connectivity != "online" {
		return fail("NODE_UNAVAILABLE")
	}
	if n.Facts.OSID != "ubuntu" || n.Facts.OSVersion != "24.04" || n.Facts.Architecture != recipe.Architecture {
		return fail("INFERENCE_PLATFORM_UNSUPPORTED")
	}
	if n.Facts.MemoryTotalBytes < recipe.MinimumHostMemoryBytes {
		return fail("INFERENCE_HOST_MEMORY_LOW")
	}
	return nil
}

// admitObservation refuses a node whose last GPU probe failed as a whole.
func admitObservation(o wire.InferenceObservation, command wire.InferenceCommand) error {
	if e := registryProbeAdmission(o, command); e != nil {
		return e
	}
	if o.ErrorCode != "" && o.Deployment == nil {
		return fail(o.ErrorCode)
	}
	return nil
}

// admitGPU holds one observed GPU to the approved recipe, and a derived model's GPU to the
// derived recipe as well. A new load also needs the memory the agent will demand free now.
func admitGPU(o wire.InferenceObservation, gpu string, derived, newLoad bool) error {
	recipe := wire.ApprovedInferenceRecipe()
	serving := wire.ApprovedDerivedServingRecipe()
	for _, g := range o.GPUs {
		if g.UUID != gpu {
			continue
		}
		major, e := strconv.Atoi(strings.Split(g.DriverVersion, ".")[0])
		if e != nil || major < recipe.MinimumDriverMajor {
			return fail("INFERENCE_DRIVER_UNSUPPORTED")
		}
		if g.ComputeCapability < recipe.MinimumComputeCapability || derived && g.ComputeCapability < serving.MinimumComputeCapability {
			return fail("INFERENCE_GPU_UNSUPPORTED")
		}
		if g.MIGMode == "Enabled" {
			return fail("INFERENCE_MIG_UNSUPPORTED")
		}
		if g.MemoryTotalMiB < recipe.MinimumGPUMemoryMiB {
			return fail("INFERENCE_GPU_MEMORY_LOW")
		}
		if newLoad && (g.MemoryFreeMiB < g.MemoryTotalMiB*wire.InferenceFreeMemoryPercent/100 || derived && g.MemoryFreeMiB < serving.MinimumGPUMemoryMiB) {
			return fail("INFERENCE_GPU_MEMORY_LOW")
		}
		return nil
	}
	return fail("INFERENCE_GPU_UNAVAILABLE")
}
func decodeObservation(raw []byte, out *wire.InferenceObservation) error {
	if json.Unmarshal(raw, out) != nil || out.Validate() != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}
