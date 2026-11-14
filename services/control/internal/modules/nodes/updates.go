package nodes

import (
	"context"
	"errors"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func updateError(e error) error {
	if e == nil {
		return nil
	}
	var storage *transaction.Error
	if errors.As(e, &storage) && storage.Kind == transaction.UniqueConflict {
		return fail("NODE_NAME_EXISTS")
	}
	switch e.Error() {
	case "VERSION_CONFLICT", "NODE_REMOVED", "NODE_NAME_EXISTS", "RESOURCE_LIMIT_REACHED", "AGENT_PACING_UNSUPPORTED", "AGENT_SHED_UNSUPPORTED":
		return fail(e.Error())
	default:
		return nodeRepositoryError(e)
	}
}

func (s *Service) updateWithin(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		return fn(ctx, q)
	})
	return updateError(e)
}

func (s *Service) lockedUpdateNode(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Node, error) {
	p := s.Identity.Config.Policy().Nodes
	if q.Dialect() == transaction.Dameng {
		return scanNode(q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT "+damengNodeFields+" FROM managed_nodes n WHERE n.tenant_id=? AND n.workspace_id=? AND n.id=? FOR UPDATE", -p.OnlineSeconds, -p.OfflineSeconds, tenant, workspace, id)))
	}
	if q.Dialect() == transaction.MySQL {
		return scanNode(q.QueryRow(ctx, transaction.SQL(transaction.MySQL,
			"SELECT "+mysqlNodeFields+" FROM managed_nodes n WHERE n.tenant_id=? AND n.workspace_id=? AND n.id=? FOR UPDATE",
			-p.OnlineSeconds, -p.OfflineSeconds, tenant, workspace, id)))
	}
	return scanNode(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT "+pairedNodeFields+" FROM managed_nodes n WHERE n.tenant_id=$3 AND n.workspace_id=$4 AND n.id=$5 FOR UPDATE OF n",
		p.OnlineSeconds, p.OfflineSeconds, tenant, workspace, id)))
}

func (s *Service) Update(ctx context.Context, a identity.Auth, id, name, status string, version int64, reason, request string) (Node, error) {
	if !isAdmin(a) {
		return Node{}, fail("ACCESS_DENIED")
	}
	if ctx == nil || !nodeID(id) || !nodeID(request) || !workspaces.ValidVersion(version) || !workspaces.ValidReason(reason) ||
		(status != "active" && status != "disabled" && status != "removed") {
		return Node{}, fail("VALIDATION_FAILED")
	}
	name, e := security.Name(name)
	if e != nil {
		return Node{}, e
	}
	var result Node
	e = s.updateWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		// Resolve and fence actual stored scope before acquiring the node's
		// exclusive lock. Neither the client nor a stale DTO supplies authority.
		current, e := s.getWithin(ctx, q, a, "", id)
		if e != nil {
			return e
		}
		current, e = s.lockedUpdateNode(ctx, q, current.TenantID, current.WorkspaceID, id)
		if e != nil {
			return e
		}
		if current.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if current.Status == "removed" {
			return fail("NODE_REMOVED")
		}
		if current.Version == workspaces.MaximumVersion {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		if status == "active" {
			w, e := s.readScope(ctx, q, a, current.WorkspaceID)
			if e != nil {
				return e
			}
			if w.Status != "active" {
				return fail("ACCESS_DENIED")
			}
		}
		// Bare core repositories remain useful for bounded profiles and tests. A
		// composed production graph must never silently omit one enabled owner.
		// Startup also checks the completely missing graph before serving.
		if status != "active" && (s.Components != nil || s.Inference != nil || s.Training != nil || s.Preparations != nil) {
			if e = s.CheckRecoveryOwners(); e != nil {
				return e
			}
		}
		fenceInference := status == "removed" && wire.IsInferenceProfile(current.ExecutionProfile)
		statement := transaction.SQL(transaction.PostgreSQL,
			`UPDATE managed_nodes SET name=$1,status=$2::text,credential_hash=CASE WHEN $2::text='removed' THEN NULL ELSE credential_hash END,inference_fenced_at=CASE WHEN $3::boolean THEN COALESCE(inference_fenced_at,clock_timestamp()) ELSE inference_fenced_at END,version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$4 AND workspace_id=$5 AND id=$6 AND version=$7`,
			name, status, fenceInference, current.TenantID, current.WorkspaceID, id, version)
		if q.Dialect() == transaction.MySQL {
			statement = transaction.SQL(transaction.MySQL,
				`UPDATE managed_nodes SET name=?,status=?,credential_hash=CASE WHEN ?='removed' THEN NULL ELSE credential_hash END,inference_fenced_at=CASE WHEN ? THEN COALESCE(inference_fenced_at,UTC_TIMESTAMP(6)) ELSE inference_fenced_at END,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND version=?`,
				name, status, status, fenceInference, current.TenantID, current.WorkspaceID, id, version)
		}
		if q.Dialect() == transaction.Dameng {
			statement = transaction.SQL(transaction.Dameng, `UPDATE managed_nodes SET name=?,status=?,credential_hash=CASE WHEN ?='removed' THEN NULL ELSE credential_hash END,inference_fenced_at=CASE WHEN ?=1 THEN COALESCE(inference_fenced_at,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) ELSE inference_fenced_at END,version=version+1,updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND id=? AND version=?`, name, status, status, fenceInference, current.TenantID, current.WorkspaceID, id, version)
		}
		changed, e := q.Exec(ctx, statement)
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("VERSION_CONFLICT")
		}
		if status != "active" {
			if _, e = q.Exec(ctx, nodeSQL(q,
				`UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='NODE_UNAVAILABLE',completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND state IN ('pending','running')`,
				`UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='NODE_UNAVAILABLE',completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running')`, `UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='NODE_UNAVAILABLE',completed_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running')`, current.TenantID, current.WorkspaceID, id)); e != nil {
				return e
			}
		}
		if fenceInference && s.Inference != nil {
			if e = s.Inference.FenceInferenceNode(ctx, q, current); e != nil {
				return e
			}
		}
		if status != "active" && s.Training != nil {
			if e = s.Training.FenceTrainingNode(ctx, q, current); e != nil {
				return e
			}
		}
		if status != "active" && s.Preparations != nil {
			if e = s.Preparations.FencePreparationNode(ctx, q, current); e != nil {
				return e
			}
		}
		if _, e = q.Exec(ctx, nodeSQL(q,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin','node.updated','success','node',$5,$6,$7)`,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin','node.updated','success','node',?,?,?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,'admin','node.updated','success','node',?,?,?)`,
			uuid.NewString(), a.Me.User.ID, current.TenantID, current.WorkspaceID, id, reason, request)); e != nil {
			return e
		}
		result, e = s.lookupWithin(ctx, q, current.TenantID, current.WorkspaceID, id)
		return e
	})
	if e != nil {
		return Node{}, e
	}
	return result, nil
}
