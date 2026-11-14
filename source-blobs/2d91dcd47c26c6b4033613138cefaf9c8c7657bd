//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type modelReadFixture struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	owner         *identity.Service
	s             *models.Service
	admin, portal identity.Auth
	model         string
	imports       []string
	operator      *pgxpool.Pool
}

const modelReadFixtureTimeout = 60 * time.Second

func modelReadSetup(t *testing.T) modelReadFixture {
	t.Helper()
	f := repositoryfixture.New(t)
	return modelReadSetupOn(t, f)
}

func modelReadSetupOn(t *testing.T, f repositoryfixture.Fixture) modelReadFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), modelReadFixtureTimeout)
	t.Cleanup(cancel)
	x := modelReadFixture{f: f, ctx: ctx, owner: &identity.Service{Storage: f.Store}, model: uuid.NewString()}
	x.s = &models.Service{Identity: x.owner, Workspaces: &workspaces.Service{Identity: x.owner}}
	x.exec(t, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')`, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')`, f.User, f.Tenant, f.Workspace)
	manifest, e := json.Marshal(wire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	x.exec(t, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','qwen3-0.6b',$4,$5::jsonb,'ready',$6)", "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','qwen3-0.6b',?,CAST(? AS JSON),'ready',?)", x.model, f.Tenant, f.Workspace, wire.ModelCatalogDigest(), manifest, f.User)
	for i := 0; i < 3; i++ {
		id := uuid.NewString()
		x.imports = append(x.imports, id)
		stamp := time.Date(2026, 10, 20, 1, 2, i, 123456000, time.UTC)
		x.exec(t, "INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,state,requested_by,requested_epoch,idempotency_key,fingerprint,reason,created_at,updated_at) VALUES($1,$2,$3,'Synthetic import \u4e16\u754c',$4,100,'cancelled',$5,1,$6,$7,'Synthetic metadata fixture',$8,$9)", "INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,state,requested_by,requested_epoch,idempotency_key,fingerprint,reason,created_at,updated_at) VALUES(?,?,?,'Synthetic import \u4e16\u754c',?,100,'cancelled',?,1,?,?,'Synthetic metadata fixture',?,?)", id, f.Tenant, f.Workspace, wire.ModelCatalogDigest(), f.User, uuid.NewString(), bytes.Repeat([]byte{1}, 32), stamp, stamp)
	}
	x.admin = x.issue(t, "admin")
	x.portal = x.issue(t, "portal")
	return x
}
func (x modelReadFixture) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := x.f.Store.Exec(x.ctx, x.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("synthetic model read fixture", e)
	}
}
func (x modelReadFixture) issue(t *testing.T, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = x.owner.IssueSessionWithin(ctx, q, x.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := x.owner.Authenticate(x.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func modelReadError(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s; received %v", code, e)
	}
}
func (x modelReadFixture) audits(t *testing.T) int {
	t.Helper()
	var n int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT COUNT(*) FROM audit_events`, `SELECT COUNT(*) FROM audit_events`)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (x modelReadFixture) denied(t *testing.T, a identity.Auth, scope, code string) {
	t.Helper()
	p, e := x.s.List(x.ctx, a, scope)
	modelReadError(t, e, code)
	if p != nil {
		t.Fatal("refused model list published metadata")
	}
	v, e := x.s.Get(x.ctx, a, scope, x.model)
	modelReadError(t, e, code)
	if v.ID != "" || v.Manifest != nil {
		t.Fatal("refused model detail published metadata")
	}
}

// This explicit test double injects an unknown outcome after a real vendor
// transaction completes. It verifies projection suppression, not wire loss.
type modelUnknownStore struct{ transaction.Store }

func (s modelUnknownStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	if e := s.Store.Within(ctx, readOnly, fn); e != nil {
		return e
	}
	return &transaction.Error{Kind: transaction.CommitUnknown}
}

// Pause the actual private list query after the shared authority fences exist.
type modelGatedStore struct {
	transaction.Store
	entered, release chan struct{}
	once             sync.Once
}
type modelGatedExecutor struct {
	transaction.Executor
	gate *modelGatedStore
}

func (q modelGatedExecutor) Query(ctx context.Context, s transaction.Statement) (transaction.Rows, error) {
	q.gate.once.Do(func() {
		close(q.gate.entered)
		select {
		case <-q.gate.release:
		case <-ctx.Done():
		}
	})
	return q.Executor.Query(ctx, s)
}
func (s *modelGatedStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error { return fn(ctx, modelGatedExecutor{q, s}) })
}

func TestActualModelRepositoryReads(t *testing.T) {
	for _, name := range []string{"projection", "isolation", "revoked_session", "disabled_user", "demoted_admin", "disabled_workspace", "invalid_base_manifest", "hidden_states", "nondefault_limit", "cancel_and_unknown_commit", "actor_fence", "workspace_fence"} {
		t.Run(name, func(t *testing.T) {
			x := modelReadSetup(t)
			before := x.audits(t)
			switch name {
			case "projection":
				for _, a := range []identity.Auth{x.admin, x.portal} {
					p, e := x.s.List(x.ctx, a, x.f.Workspace)
					if e != nil || len(p) != 1 || p[0].ID != x.model || p[0].Name != "Synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || p[0].Kind != "approved_base" {
						t.Fatal("exact ready model inventory", e)
					}
					v, e := x.s.Get(x.ctx, a, x.f.Workspace, x.model)
					if e != nil || v.ID != x.model || !json.Valid(v.Manifest) || v.CreatedAt.Location() != time.UTC {
						t.Fatal("model detail", e)
					}
				}
				if v, e := x.s.Get(x.ctx, x.admin, "", x.model); e != nil || v.ID != x.model {
					t.Fatal("Admin scope discovery", e)
				}
				p, e := x.s.Imports(x.ctx, x.admin, x.f.Workspace)
				if e != nil || len(p) != 3 || p[0].ID != x.imports[2] {
					t.Fatal("private import order", e)
				}
				v, e := x.s.GetImport(x.ctx, x.admin, x.imports[0])
				if e != nil || v.ID != x.imports[0] || v.ModelID != nil || v.CreatedAt.Location() != time.UTC {
					t.Fatal("private import detail", e)
				}
				v, e = x.s.GetImport(x.ctx, x.portal, x.imports[0])
				modelReadError(t, e, "ACCESS_DENIED")
				if v.ID != "" {
					t.Fatal("Portal import metadata")
				}
				if p, e := x.s.Imports(x.ctx, x.portal, x.f.Workspace); p != nil || e == nil || e.Error() != "ACCESS_DENIED" {
					t.Fatal("Portal import list", e)
				}
			case "isolation":
				other, foreign := uuid.NewString(), uuid.NewString()
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other','active')`, other, x.f.Tenant)
				x.denied(t, x.portal, other, "RESOURCE_NOT_FOUND")
				v, e := x.s.Get(x.ctx, x.admin, other, x.model)
				modelReadError(t, e, "RESOURCE_NOT_FOUND")
				if v.ID != "" {
					t.Fatal("mismatched scope published model")
				}
				x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign','active')`, foreign)
				foreignWorkspace := uuid.NewString()
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign','active')`, foreignWorkspace, foreign)
				x.denied(t, x.admin, foreignWorkspace, "RESOURCE_NOT_FOUND")
				for _, id := range []string{"not-an-id", strings.ToUpper(x.model), uuid.NewString()} {
					if v, e := x.s.Get(x.ctx, x.admin, x.f.Workspace, id); e == nil || e.Error() != "RESOURCE_NOT_FOUND" || v.ID != "" {
						t.Fatal("invalid/missing model leaked", e)
					}
				}
				x.exec(t, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, `DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?`, x.f.User, x.f.Workspace)
				x.denied(t, x.portal, x.f.Workspace, "RESOURCE_NOT_FOUND")
			case "revoked_session":
				x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_model_read' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_model_read' WHERE id=?`, x.admin.SessionID)
				x.denied(t, x.admin, x.f.Workspace, "AUTH_SESSION_EXPIRED")
				if _, e := x.s.List(x.ctx, x.portal, x.f.Workspace); e != nil {
					t.Fatal("independent current session recovery", e)
				}
			case "disabled_user":
				x.exec(t, `UPDATE users SET status='disabled' WHERE id=$1`, `UPDATE users SET status='disabled' WHERE id=?`, x.f.User)
				x.denied(t, x.admin, x.f.Workspace, "AUTH_SESSION_EXPIRED")
			case "demoted_admin":
				x.exec(t, `UPDATE users SET is_platform_admin=false WHERE id=$1`, `UPDATE users SET is_platform_admin=false WHERE id=?`, x.f.User)
				x.denied(t, x.admin, x.f.Workspace, "AUTH_SESSION_EXPIRED")
			case "disabled_workspace":
				x.exec(t, `UPDATE workspaces SET status='disabled' WHERE id=$1`, `UPDATE workspaces SET status='disabled' WHERE id=?`, x.f.Workspace)
				x.denied(t, x.portal, x.f.Workspace, "RESOURCE_NOT_FOUND")
				if _, e := x.s.List(x.ctx, x.admin, x.f.Workspace); e != nil {
					t.Fatal("deliberate Admin inactive-scope inspection", e)
				}
			case "invalid_base_manifest":
				x.exec(t, `UPDATE model_versions SET manifest='{}'::jsonb WHERE id=$1`, `UPDATE model_versions SET manifest=CAST('{}' AS JSON) WHERE id=?`, x.model)
				x.denied(t, x.portal, x.f.Workspace, "MODEL_FILE_INVALID")
				manifest, _ := json.Marshal(wire.ApprovedModel())
				x.exec(t, `UPDATE model_versions SET manifest=$1::jsonb WHERE id=$2`, `UPDATE model_versions SET manifest=CAST(? AS JSON) WHERE id=?`, manifest, x.model)
				if _, e := x.s.Get(x.ctx, x.portal, x.f.Workspace, x.model); e != nil {
					t.Fatal("manifest repair read recovery", e)
				}
			case "hidden_states":
				x.exec(t, `UPDATE model_versions SET state='deleted',deleted_at=clock_timestamp() WHERE id=$1`, `UPDATE model_versions SET state='deleted',deleted_at=UTC_TIMESTAMP(6) WHERE id=?`, x.model)
				staged := uuid.NewString()
				_, stagingError := x.f.Store.Exec(x.ctx, x.f.Statement(`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES($1,$2,$3,'Staged','qwen3-0.6b',$4,'{}'::jsonb,'staged',$5,'merged_lora')`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES(?,?,?,'Staged','qwen3-0.6b',?,CAST('{}' AS JSON),'staged',?,'merged_lora')`, staged, x.f.Tenant, x.f.Workspace, wire.ModelCatalogDigest(), x.f.User))
				if x.f.Store.Dialect() == transaction.PostgreSQL {
					// PostgreSQL publishes derived graphs using deferred
					// constraints and has no representable staged row.
					modelReadError(t, stagingError, string(transaction.CheckConflict))
				} else if stagingError != nil {
					t.Fatal("actual MySQL staged row fixture", stagingError)
				}
				p, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e != nil || p == nil || len(p) != 0 {
					t.Fatal("hidden model inventory", e)
				}
				for _, id := range []string{x.model, staged} {
					v, e := x.s.Get(x.ctx, x.portal, x.f.Workspace, id)
					modelReadError(t, e, "RESOURCE_NOT_FOUND")
					if v.ID != "" {
						t.Fatal("hidden detail")
					}
				}
			case "nondefault_limit":
				policy := config.DefaultRuntimePolicy()
				policy.UI.LookupPageSize = 2
				x.owner.Config.Runtime = &policy
				p, e := x.s.Imports(x.ctx, x.admin, x.f.Workspace)
				if e != nil || len(p) != 2 || p[0].ID != x.imports[2] || p[1].ID != x.imports[1] {
					t.Fatal("nondefault lookup limit/order", e)
				}
			case "cancel_and_unknown_commit":
				ctx, cancel := context.WithCancel(x.ctx)
				cancel()
				if p, e := x.s.List(ctx, x.admin, x.f.Workspace); p != nil || e == nil || e.Error() != string(transaction.Cancelled) {
					t.Fatal("cancelled read projection", e)
				}
				x.owner.Storage = modelUnknownStore{x.f.Store}
				x.denied(t, x.admin, x.f.Workspace, string(transaction.CommitUnknown))
				if p, e := x.s.Imports(x.ctx, x.admin, x.f.Workspace); p != nil || e == nil || e.Error() != string(transaction.CommitUnknown) {
					t.Fatal("unknown import list", e)
				}
				if v, e := x.s.GetImport(x.ctx, x.admin, x.imports[0]); v.ID != "" || e == nil || e.Error() != string(transaction.CommitUnknown) {
					t.Fatal("unknown import detail", e)
				}
				x.owner.Storage = x.f.Store
				if _, e := x.s.List(x.ctx, x.admin, x.f.Workspace); e != nil {
					t.Fatal("independent read recovery", e)
				}
			case "actor_fence", "workspace_fence":
				gate := &modelGatedStore{Store: x.f.Store, entered: make(chan struct{}), release: make(chan struct{})}
				x.owner.Storage = gate
				finished := make(chan error, 1)
				go func() {
					p, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
					if e == nil && len(p) != 1 {
						e = context.Canceled
					}
					finished <- e
				}()
				select {
				case <-gate.entered:
				case <-x.ctx.Done():
					t.Fatal("private model query did not reach held fence")
				}
				pg, my, id := `UPDATE users SET status='disabled' WHERE id=$1`, `UPDATE users SET status='disabled' WHERE id=?`, x.f.User
				if name == "workspace_fence" {
					pg, my, id = `UPDATE workspaces SET status='disabled' WHERE id=$1`, `UPDATE workspaces SET status='disabled' WHERE id=?`, x.f.Workspace
				}
				blocked, cancel := context.WithTimeout(x.ctx, 100*time.Millisecond)
				_, e := x.f.Store.Exec(blocked, x.f.Statement(pg, my, id))
				cancel()
				close(gate.release)
				if e == nil {
					t.Fatal("writer passed actual held model-read authority fence")
				}
				if e = <-finished; e != nil {
					t.Fatal("fenced read failed", e)
				}
				x.owner.Storage = x.f.Store
				x.exec(t, pg, my, id)
				code := "AUTH_SESSION_EXPIRED"
				if name == "workspace_fence" {
					code = "RESOURCE_NOT_FOUND"
				}
				x.denied(t, x.portal, x.f.Workspace, code)
			}
			if x.audits(t) != before {
				t.Fatal("model read mutated audit")
			}
		})
	}
}
