package nodes

import (
	"context"
	"database/sql"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const mysqlNodeFields = `n.id,n.tenant_id,n.workspace_id,n.name,n.status,n.version,n.protocol_version,n.facts,n.last_heartbeat_at,n.created_at,n.updated_at,CASE WHEN n.last_heartbeat_at>=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)) THEN 'online' WHEN n.last_heartbeat_at>=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)) THEN 'unknown' ELSE 'offline' END,n.execution_profile,n.capabilities,'default','default'`
const pairedNodeFields = `n.id::text,n.tenant_id::text,n.workspace_id::text,n.name,n.status,n.version,n.protocol_version,n.facts,n.last_heartbeat_at,n.created_at,n.updated_at,CASE WHEN n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>$1) THEN 'online' WHEN n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>$2) THEN 'unknown' ELSE 'offline' END,n.execution_profile,n.capabilities,n.site_id,n.cell_id`
const mysqlJoinFields = `j.id,j.tenant_id,j.workspace_id,j.node_name,j.created_by,j.node_id,j.created_at,j.expires_at,CASE WHEN j.revoked_at IS NOT NULL THEN 'revoked' WHEN j.consumed_at IS NOT NULL THEN 'consumed' WHEN j.expires_at<=UTC_TIMESTAMP(6) THEN 'expired' ELSE 'pending' END,j.execution_profile`
const mysqlTaskFields = `id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at`

func nodeID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}
func nodeSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func nodeRepositoryError(e error) error {
	if e == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return fail(string(kind))
	}
	switch e.Error() {
	case "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "INVALID_CURSOR",
		"STORAGE_UNAVAILABLE", string(transaction.Deadlock), string(transaction.LockTimeout),
		string(transaction.Cancelled), string(transaction.Timeout), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return fail(e.Error())
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, fail("NODE_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("NODE_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return nil, e
	}
	switch store.Dialect() {
	case transaction.PostgreSQL, transaction.MySQL, transaction.Dameng:
		return store, nil
	default:
		return nil, fail("NODE_STORAGE_MISMATCH")
	}
}

// Actor/source/session fences and the selected Workspace fence live in the same
// real transaction as the returned private projection. Callers publish only
// after Within succeeds; an uncertain commit never publishes a partial page.
func (s *Service) readActor(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil || a.Me.Audience != "admin" && a.Me.Audience != "portal" {
		return fail("AUTH_SESSION_EXPIRED")
	}
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
	return nodeRepositoryError(e)
}
func nodeTenant(ctx context.Context, q transaction.Executor) (string, error) {
	var tenant string
	e := q.QueryRow(ctx, nodeSQL(q,
		`SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t`,
		`SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR UPDATE`)).Scan(&tenant)
	if errors.Is(e, sql.ErrNoRows) {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	return tenant, e
}
func (s *Service) readScope(ctx context.Context, q transaction.Executor, a identity.Auth, workspace string) (workspaces.Workspace, error) {
	if !nodeID(workspace) {
		return workspaces.Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	return s.Workspaces.ReadScopeWithin(ctx, q, a, workspace)
}
func (s *Service) lookupWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Node, error) {
	p := s.Identity.Config.Policy().Nodes
	if q.Dialect() == transaction.Dameng {
		return scanNode(q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT "+damengNodeFields+" FROM managed_nodes n WHERE n.tenant_id=? AND n.workspace_id=? AND n.id=?", -p.OnlineSeconds, -p.OfflineSeconds, tenant, workspace, id)))
	}
	if q.Dialect() == transaction.MySQL {
		return scanNode(q.QueryRow(ctx, transaction.SQL(transaction.MySQL, "SELECT "+mysqlNodeFields+" FROM managed_nodes n WHERE n.tenant_id=? AND n.workspace_id=? AND n.id=?", -p.OnlineSeconds, -p.OfflineSeconds, tenant, workspace, id)))
	}
	return scanNode(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+pairedNodeFields+" FROM managed_nodes n WHERE n.tenant_id=$3 AND n.workspace_id=$4 AND n.id=$5", p.OnlineSeconds, p.OfflineSeconds, tenant, workspace, id)))
}
func (s *Service) getWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace, id string) (Node, error) {
	if !nodeID(id) || workspace != "" && !nodeID(workspace) {
		return Node{}, fail("RESOURCE_NOT_FOUND")
	}
	if workspace == "" {
		if !isAdmin(a) {
			return Node{}, fail("RESOURCE_NOT_FOUND")
		}
		// Discover only the scope,then authorize/fence it before reading the DTO.
		e := q.QueryRow(ctx, nodeSQL(q,
			`SELECT n.workspace_id::text FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1`,
			`SELECT n.workspace_id FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=?`, `SELECT n.workspace_id FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=?`, id)).Scan(&workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return Node{}, fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return Node{}, e
		}
	}
	w, e := s.readScope(ctx, q, a, workspace)
	if e != nil {
		return Node{}, e
	}
	tenant, e := nodeTenant(ctx, q)
	if e != nil {
		return Node{}, e
	}
	if w.TenantID != tenant {
		return Node{}, fail("RESOURCE_NOT_FOUND")
	}
	return s.lookupWithin(ctx, q, tenant, w.ID, id)
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Node, error) {
	var out Node
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var e error
		out, e = s.getWithin(ctx, q, a, workspace, id)
		return e
	})
	if e != nil {
		return Node{}, e
	}
	return out, nil
}

// CheckRecoveryOwners validates the production graph before readiness. Every
// recovery owner is required even when the inspected node currently has no
// workload rows. It performs no SQL and grants no authority to a later node
// mutation.
func (s *Service) CheckRecoveryOwners() error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	present := func(owner any) bool {
		if owner == nil {
			return false
		}
		value := reflect.ValueOf(owner)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			return !value.IsNil()
		default:
			return true
		}
	}
	{
		if !present(s.Components) {
			return fail("STORAGE_UNAVAILABLE")
		}
		if store.Dialect() != transaction.PostgreSQL {
			if _, ok := s.Components.(ComponentObserverWithin); !ok {
				return fail("STORAGE_UNAVAILABLE")
			}
			if _, ok := s.Components.(ComponentResultObserverWithin); !ok {
				return fail("STORAGE_UNAVAILABLE")
			}
		}
	}
	{
		if !present(s.Inference) {
			return fail("STORAGE_UNAVAILABLE")
		}
		if store.Dialect() != transaction.PostgreSQL {
			if _, ok := s.Inference.(InferenceObserverWithin); !ok {
				return fail("STORAGE_UNAVAILABLE")
			}
		}
	}
	if !present(s.Training) {
		return fail("STORAGE_UNAVAILABLE")
	}
	if !present(s.Preparations) {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}
