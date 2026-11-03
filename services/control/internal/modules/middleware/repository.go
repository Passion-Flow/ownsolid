package middleware

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Service struct {
	Identity      *identity.Service
	Workspaces    *workspaces.Service
	configuration Configuration
	workerMu      sync.Mutex
	workerRunning bool
}

func New(accounts *identity.Service, scopes *workspaces.Service, c Configuration) (*Service, error) {
	if accounts == nil || scopes == nil || scopes.Identity != accounts || c.Validate() != nil {
		return nil, fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	// Deep-copy slice ownership; subsequent caller edits cannot silently retarget.
	copy := c
	copy.Profiles = append([]Profile(nil), c.Profiles...)
	for i := range copy.Profiles {
		copy.Profiles[i].RestoreTargets = append([]RestoreTarget(nil), c.Profiles[i].RestoreTargets...)
	}
	s := &Service{Identity: accounts, Workspaces: scopes, configuration: copy}
	if _, e := s.repository(); e != nil {
		return nil, e
	}
	return s, nil
}

func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("MIDDLEWARE_IDENTITY_REPOSITORY_REQUIRED")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return nil, e
	}
	if store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
		return nil, fail("MIDDLEWARE_STORAGE_MISMATCH")
	}
	return store, nil
}

func statement(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}

func (s *Service) profile(key string) (Profile, error) {
	for _, p := range s.configuration.Profiles {
		if p.Key == key {
			return p, nil
		}
	}
	return Profile{}, fail("MIDDLEWARE_PROFILE_UNAVAILABLE")
}

// RequestBudgetSeconds exposes only the validated public request policy.
func (s *Service) RequestBudgetSeconds() int {
	if s == nil || s.configuration.Policy.Validate() != nil {
		return 0
	}
	return s.configuration.Policy.RequestSeconds
}

func (s *Service) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(s.configuration.Policy.RequestSeconds)*time.Second)
}

func actor(a identity.Auth) bool {
	return validID(a.Me.User.ID) && validID(a.SessionID) && len(a.TokenHash) == 32 && a.Epoch > 0 && (a.Me.Audience == "portal" || a.Me.Audience == "admin")
}

func (s *Service) authority(ctx context.Context, q transaction.Executor, a identity.Auth, workspace string, write bool) (workspaces.Workspace, error) {
	if !actor(a) {
		return workspaces.Workspace{}, fail("AUTH_SESSION_EXPIRED")
	}
	if !validID(workspace) {
		return workspaces.Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	var w workspaces.Workspace
	var e error
	if write {
		w, e = s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
	} else {
		w, e = s.Workspaces.ReadScopeWithin(ctx, q, a, workspace)
	}
	if e != nil {
		return workspaces.Workspace{}, e
	}
	if write && a.Me.Audience != "admin" && (w.Role == nil || *w.Role != "owner") {
		return workspaces.Workspace{}, fail("ACCESS_DENIED")
	}
	if w.Status != "active" {
		return workspaces.Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	return w, nil
}

// Admission observes License only after a real current actor/scope check, then
// every final read/write transaction rechecks the same current authority.
func (s *Service) admit(ctx context.Context, a identity.Auth, workspace string, write bool) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := s.authority(ctx, q, a, workspace, write)
		return e
	})
	if e != nil {
		return e
	}
	if write {
		return s.Identity.RequireNewBusiness(ctx)
	}
	return s.Identity.RequireBusinessIntegrity(ctx)
}

func audit(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target, id, reason, request string) error {
	_, e := q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success',$7,$8,NULLIF($9,''),$10)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,'success',?,?,NULLIF(?,''),?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, request))
	return e
}

const instancePG = `id::text,tenant_id::text,workspace_id::text,profile_key,kind,name,version,state,profile_fingerprint,created_at`
const instanceMy = `id,tenant_id,workspace_id,profile_key,kind,name,version,state,profile_fingerprint,created_at`

func scanInstance(row transaction.Row) (Instance, error) {
	var v Instance
	e := row.Scan(&v.ID, &v.TenantID, &v.WorkspaceID, &v.ProfileKey, &v.Kind, &v.Name, &v.Version, &v.State, &v.Fingerprint, &v.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Instance{}, fail("RESOURCE_NOT_FOUND")
	}
	return v, e
}

func instanceWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, id string, lock bool) (Instance, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	return scanInstance(q.QueryRow(ctx, statement(q, "SELECT "+instancePG+" FROM middleware_instances WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3"+suffix, "SELECT "+instanceMy+" FROM middleware_instances WHERE tenant_id=? AND workspace_id=? AND id=?"+suffix, w.TenantID, w.ID, id)))
}

func committed[T any](out T, e error) (T, error) {
	if e != nil {
		var zero T
		return zero, e
	}
	return out, nil
}

func normalize(e error) error {
	if e == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return &transaction.Error{Kind: kind}
	}
	var storage *transaction.Error
	if errors.As(e, &storage) {
		return storage
	}
	code := e.Error()
	if len(code) < 96 && !strings.ContainsAny(code, " /:\r\n") && (strings.HasPrefix(code, "MIDDLEWARE_") || strings.HasPrefix(code, "MAINTENANCE_") || strings.HasPrefix(code, "OFFLINE_SCOPE_") || strings.HasPrefix(code, "OFFLINE_ENGINE_") || strings.HasPrefix(code, "OFFLINE_COMPOSE_") || strings.HasPrefix(code, "OFFLINE_OPERATOR_") || strings.HasPrefix(code, "AUTH_") || strings.HasPrefix(code, "LICENSE_") || code == "ACCESS_DENIED" || code == "RESOURCE_NOT_FOUND" || code == "VALIDATION_FAILED" || code == "IDEMPOTENCY_CONFLICT" || code == "VERSION_CONFLICT") {
		return fail(code)
	}
	return fail("MIDDLEWARE_STORAGE_UNAVAILABLE")
}
