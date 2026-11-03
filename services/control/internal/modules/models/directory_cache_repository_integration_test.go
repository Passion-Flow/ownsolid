//go:build repository_integration && valkey_integration

package models_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/modelcache"
)

// Real Valkey connections and real vendor SQL; the wrapper observes only calls.
type observedDirectoryCache struct {
	actual     *modelcache.Client
	gets, sets int
	key        string
	beforeGet  func()
}

func (c *observedDirectoryCache) Get(ctx context.Context, key string) ([]byte, error) {
	c.gets++
	c.key = key
	if c.beforeGet != nil {
		f := c.beforeGet
		c.beforeGet = nil
		f()
	}
	return c.actual.Get(ctx, key)
}
func (c *observedDirectoryCache) Set(ctx context.Context, key string, raw []byte) error {
	c.sets++
	c.key = key
	return c.actual.Set(ctx, key, raw)
}

// This explicit error injector verifies SQL fallback; it is not a real outage.
type unavailableDirectoryCache struct{}

func (unavailableDirectoryCache) Get(context.Context, string) ([]byte, error) {
	return nil, modelcache.ErrUnavailable
}
func (unavailableDirectoryCache) Set(context.Context, string, []byte) error {
	return modelcache.ErrUnavailable
}

type countedDirectoryStore struct {
	transaction.Store
	queries int
}
type countedDirectoryExecutor struct {
	transaction.Executor
	owner *countedDirectoryStore
}

func (q countedDirectoryExecutor) Query(ctx context.Context, s transaction.Statement) (transaction.Rows, error) {
	q.owner.queries++
	return q.Executor.Query(ctx, s)
}
func (s *countedDirectoryStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, countedDirectoryExecutor{q, s})
	})
}
func actualDirectoryCache(t *testing.T) *observedDirectoryCache {
	t.Helper()
	c, e := config.Load(os.Getenv("PROJECT_ONE_MODEL_CACHE_TEST_CONFIGURATION"))
	if e != nil {
		t.Fatal("explicit private cache fixture configuration required", e)
	}
	if !c.ModelCache.Enabled || !strings.HasPrefix(c.ModelCache.Address, "127.0.0.1:") || !strings.HasPrefix(c.ModelCache.Namespace, "m23_test_") || c.ModelCache.TTLSeconds != 1 {
		t.Fatal("isolated loopback Valkey fixture with one-second TTL required")
	}
	// Each test receives an exclusive namespace. Expiry reclaims these disposable keys.
	c.ModelCache.Namespace = "m23_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	client, e := modelcache.New(c.ModelCache)
	if e != nil {
		t.Fatal(e)
	}
	probe := "owned-probe"
	if e = client.Set(t.Context(), probe, []byte("actual")); e != nil {
		t.Fatal("real Valkey unavailable", e)
	}
	if raw, e := client.Get(t.Context(), probe); e != nil || !bytes.Equal(raw, []byte("actual")) {
		t.Fatal("real Valkey readback", e)
	}
	return &observedDirectoryCache{actual: client}
}
func TestActualModelDirectoryValkey(t *testing.T) {
	for _, name := range []string{"hit_mutation_corruption_expiry", "cross_workspace", "revoked_session", "removed_membership", "disabled_workspace", "unknown_commit", "concurrent_fill", "source_corruption", "retired_version", "limit_change", "adapter_fault"} {
		t.Run(name, func(t *testing.T) {
			x := modelReadSetup(t)
			cache := actualDirectoryCache(t)
			x.s.DirectoryCache = cache
			counted := &countedDirectoryStore{Store: x.f.Store}
			x.owner.Storage = counted
			cold, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
			if e != nil || len(cold) != 1 || cache.sets != 1 {
				t.Fatal("real miss/fill", e)
			}
			firstKey := cache.key
			counted.queries = 0
			warm, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
			if e != nil || len(warm) != 1 || cache.sets != 1 || counted.queries != 1 {
				t.Fatal("real hit must omit full manifest query", e)
			}
			if !strings.Contains(firstKey, ":"+x.f.Tenant+":"+x.f.Workspace+":") {
				t.Fatal("unscoped key")
			}
			switch name {
			case "hit_mutation_corruption_expiry":
				x.exec(t, `UPDATE model_versions SET name='Changed' WHERE id=$1`, `UPDATE model_versions SET name='Changed' WHERE id=?`, x.model)
				changed, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e != nil || changed[0].Name != "Changed" || cache.key == firstKey {
					t.Fatal("immediate invalidation", e)
				}
				if e = cache.actual.Set(x.ctx, cache.key, []byte(`[{"header":{"name":"foreign"}}]`)); e != nil {
					t.Fatal(e)
				}
				after, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e != nil || after[0].Name != "Changed" {
					t.Fatal("pollution fallback", e)
				}
				key := cache.key
				select {
				case <-time.After(1100 * time.Millisecond):
				case <-x.ctx.Done():
					t.Fatal(x.ctx.Err())
				}
				if raw, e := cache.actual.Get(x.ctx, key); e != nil || raw != nil {
					t.Fatal("actual expiry", e)
				}
				before := cache.sets
				if _, e = x.s.List(x.ctx, x.portal, x.f.Workspace); e != nil || cache.sets != before+1 {
					t.Fatal("expiry recovery", e)
				}
			case "cross_workspace":
				original, e := cache.actual.Get(x.ctx, firstKey)
				if e != nil {
					t.Fatal(e)
				}
				other := uuid.NewString()
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other','active')`, other, x.f.Tenant)
				if v, e := x.s.List(x.ctx, x.admin, other); e != nil || len(v) != 0 {
					t.Fatal("other empty", e)
				}
				if e = cache.actual.Set(x.ctx, cache.key, original); e != nil {
					t.Fatal(e)
				}
				if v, e := x.s.List(x.ctx, x.admin, other); e != nil || len(v) != 0 {
					t.Fatal("foreign cache exposed", e)
				}
			case "revoked_session":
				before := cache.gets
				x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic' WHERE id=?`, x.portal.SessionID)
				v, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e == nil || v != nil || cache.gets != before {
					t.Fatal("warm session revocation bypass", e)
				}
			case "removed_membership":
				before := cache.gets
				x.exec(t, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, `DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?`, x.f.User, x.f.Workspace)
				v, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e == nil || v != nil || cache.gets != before {
					t.Fatal("warm membership revocation bypass", e)
				}
			case "disabled_workspace":
				before := cache.gets
				x.exec(t, `UPDATE workspaces SET status='disabled' WHERE id=$1`, `UPDATE workspaces SET status='disabled' WHERE id=?`, x.f.Workspace)
				v, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e == nil || v != nil || cache.gets != before {
					t.Fatal("warm disabled workspace bypass", e)
				}
			case "source_corruption":
				x.exec(t, `UPDATE model_versions SET manifest='{}'::jsonb WHERE id=$1`, `UPDATE model_versions SET manifest=CAST('{}' AS JSON) WHERE id=?`, x.model)
				v, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e == nil || v != nil {
					t.Fatal("warm cache hid corrupt source", e)
				}
			case "retired_version":
				x.exec(t, `UPDATE model_versions SET state='deleted',deleted_at=clock_timestamp() WHERE id=$1`, `UPDATE model_versions SET state='deleted',deleted_at=UTC_TIMESTAMP(6) WHERE id=?`, x.model)
				if v, e := x.s.List(x.ctx, x.portal, x.f.Workspace); e != nil || len(v) != 0 || cache.key == firstKey {
					t.Fatal("retired model stayed cached", e)
				}
			case "limit_change":
				policy := config.DefaultRuntimePolicy()
				policy.UI.LookupPageSize = 1
				x.owner.Config.Runtime = &policy
				if v, e := x.s.List(x.ctx, x.portal, x.f.Workspace); e != nil || len(v) != 1 || cache.key == firstKey {
					t.Fatal("page policy omitted from key", e)
				}
			case "adapter_fault":
				x.s.DirectoryCache = unavailableDirectoryCache{}
				if v, e := x.s.List(x.ctx, x.portal, x.f.Workspace); e != nil || len(v) != 1 || v[0].ID != x.model {
					t.Fatal("SQL fallback failed", e)
				}
			case "unknown_commit":
				x.owner.Storage = modelUnknownStore{x.f.Store}
				before := cache.sets
				v, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e == nil || v != nil || cache.sets != before {
					t.Fatal("unknown transaction published", e)
				}
			case "concurrent_fill":
				// Force a different key before racing the miss's second SQL statement.
				x.exec(t, `UPDATE model_versions SET name='Before race' WHERE id=$1`, `UPDATE model_versions SET name='Before race' WHERE id=?`, x.model)
				before := cache.sets
				cache.beforeGet = func() {
					x.exec(t, `UPDATE model_versions SET name='After race' WHERE id=$1`, `UPDATE model_versions SET name='After race' WHERE id=?`, x.model)
				}
				v, e := x.s.List(x.ctx, x.portal, x.f.Workspace)
				if e != nil || len(v) != 1 {
					t.Fatal("concurrent read", e)
				}
				// PostgreSQL READ COMMITTED sees the new statement; MySQL may retain its
				// transaction snapshot. Both must key their exact observed result.
				if v[0].Name == "After race" && cache.sets != before {
					t.Fatal("new result stored under old key")
				}
				if v, e = x.s.List(x.ctx, x.portal, x.f.Workspace); e != nil || v[0].Name != "After race" {
					t.Fatal("committed race remained stale", e)
				}
			}
		})
	}
}
