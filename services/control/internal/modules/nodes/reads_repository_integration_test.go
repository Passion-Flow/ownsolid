//go:build repository_integration

package nodes_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

type nodeReadActual struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	owner         *identity.Service
	s             *nodes.Service
	admin, portal identity.Auth
	ids           []string
	stamp         time.Time
}

func newNodeReadActual(t *testing.T) nodeReadActual {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	x := nodeReadActual{f: f, ctx: ctx, owner: owner, stamp: time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC)}
	x.s = &nodes.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: &workspaces.Service{DB: f.PostgreSQL, Identity: owner}}
	x.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	facts, e := json.Marshal(wire.Facts{OSID: "synthetic", Architecture: "test", Hostname: "Schema catalog \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", MemoryTotalBytes: 1048576, ObservedAt: x.stamp})
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 9; n++ {
		id := uuid.NewString()
		x.ids = append(x.ids, id)
		cred := sha256.Sum256([]byte(uuid.NewString()))
		x.exec(t, "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at,updated_at) VALUES($1,$2,$3,$4,'active',$5,'1.0',$6,$7,$7,$7)", "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at,updated_at) VALUES(?,?,?,?,'active',?,'1.0',?,?,?,?)", id, f.Tenant, f.Workspace, "Node catalog "+id, cred[:], facts, x.stamp)
	}
	// These are explicit synthetic rows for read acceptance. They do not enroll
	// an agent or establish paired mutation/lease execution support.
	for n := 0; n < 9; n++ {
		token := sha256.Sum256([]byte(uuid.NewString()))
		id := uuid.NewString()
		x.exec(t, "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,created_at,expires_at) VALUES($1,$2,$3,'Synthetic token',$4,$5,$6,$7)", "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,created_at,expires_at) VALUES(?,?,?,'Synthetic token',?,?,?,?)", id, f.Tenant, f.Workspace, token[:], f.User, x.stamp, x.stamp.Add(time.Minute))
		x.exec(t, "INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,created_at,updated_at,deadline_at,completed_at) VALUES($1,$2,$3,$4,'collect_facts.v1','succeeded',$5,$6,$7,$7,$8,$9)", "INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,created_at,updated_at,deadline_at,completed_at) VALUES(?,?,?,?,'collect_facts.v1','succeeded',?,?,?,?,?,?)", uuid.NewString(), f.Tenant, f.Workspace, x.ids[0], uuid.NewString(), f.User, x.stamp, x.stamp.Add(time.Minute), x.stamp.Add(time.Second))
	}
	x.admin, x.portal = x.issue(t, f.User, "admin"), x.issue(t, f.User, "portal")
	return x
}
func (x nodeReadActual) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	// MySQL binds each repeated timestamp placeholder independently.
	if x.f.Store.Dialect() == transaction.MySQL {
		if strings.HasPrefix(my, "INSERT INTO managed_nodes") {
			args = append(args, args[len(args)-1], args[len(args)-1])
		}
		if strings.HasPrefix(my, "INSERT INTO node_tasks") {
			args = append(append(append([]any{}, args[:7]...), args[6]), args[7:]...)
		}
	}
	if _, e := x.f.Store.Exec(x.ctx, x.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual synthetic node read setup", e)
	}
}
func (x nodeReadActual) issue(t *testing.T, user, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = x.owner.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
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
func nodeReadWant(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s,got %v", code, e)
	}
}
func (x nodeReadActual) audits(t *testing.T) int {
	t.Helper()
	var n int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestActualNodeReadCatalog(t *testing.T) {
	x := newNodeReadActual(t)
	t.Run("typed_detail_and_nullable_history", func(t *testing.T) {
		before := x.audits(t)
		n, e := x.s.Get(x.ctx, x.admin, "", x.ids[0])
		if e != nil || n.Facts.Hostname != "Schema catalog \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || n.LastHeartbeatAt.Nanosecond() != 123456000 || n.LastHeartbeatAt.Location() != time.UTC || len(n.Capabilities) != 1 || n.Capabilities[0] != wire.CollectFacts || n.Connectivity != "offline" {
			t.Fatal("actual typed node detail", e)
		}
		page, e := x.s.Tasks(x.ctx, x.admin, "", x.ids[0], url.Values{"limit": {"2"}})
		if e != nil || len(page.Items) != 2 || page.Items[0].CompletedAt == nil || page.Items[0].ErrorCode != nil || page.Items[0].State != "succeeded" {
			t.Fatal("actual nullable task history", e)
		}
		raw, e := json.Marshal([]any{n, page})
		if e != nil {
			t.Fatal(e)
		}
		for _, key := range []string{"credential_hash", "token_hash", "lease_token_hash", "result_hash", "payload", "request_fingerprint"} {
			if strings.Contains(string(raw), `"`+key+`"`) {
				t.Fatal("private protocol field escaped", key)
			}
		}
		if x.audits(t) != before {
			t.Fatal("ordinary node reads mutated audit")
		}
	})
	t.Run("three_tied_catalogs_seek_once", func(t *testing.T) {
		for _, kind := range []string{"nodes", "tokens", "tasks"} {
			values := url.Values{"limit": {"2"}}
			seen := map[string]bool{}
			pages := 0
			for {
				var ids []string
				var cursor *string
				switch kind {
				case "nodes":
					p, e := x.s.List(x.ctx, x.admin, x.f.Workspace, values)
					if e != nil {
						t.Fatal(e)
					}
					for _, n := range p.Items {
						ids = append(ids, n.ID)
					}
					cursor = p.NextCursor
				case "tokens":
					p, e := x.s.Tokens(x.ctx, x.admin, values)
					if e != nil {
						t.Fatal(e)
					}
					for _, n := range p.Items {
						ids = append(ids, n.ID)
					}
					cursor = p.NextCursor
				case "tasks":
					p, e := x.s.Tasks(x.ctx, x.admin, "", x.ids[0], values)
					if e != nil {
						t.Fatal(e)
					}
					for _, n := range p.Items {
						ids = append(ids, n.ID)
					}
					cursor = p.NextCursor
				}
				if len(ids) > 2 {
					t.Fatal("unbounded catalog")
				}
				for _, id := range ids {
					if seen[id] {
						t.Fatal("duplicate tied catalog row", kind)
					}
					seen[id] = true
				}
				pages++
				if cursor == nil {
					break
				}
				if pages > 5 {
					t.Fatal("unbounded page count")
				}
				values.Set("cursor", *cursor)
			}
			if len(seen) != 9 || pages != 5 {
				t.Fatal("tied catalog omitted rows", kind, len(seen), pages)
			}
		}
	})
	t.Run("portal_scope_and_secret_free_empty_lists", func(t *testing.T) {
		if _, e := x.s.Get(x.ctx, x.portal, x.f.Workspace, x.ids[0]); e != nil {
			t.Fatal(e)
		}
		if p, e := x.s.List(x.ctx, x.portal, x.f.Workspace, nil); e != nil || len(p.Items) != 9 {
			t.Fatal(e)
		}
		if p, e := x.s.Tasks(x.ctx, x.portal, x.f.Workspace, x.ids[1], nil); e != nil || p.Items == nil || len(p.Items) != 0 || p.NextCursor != nil {
			t.Fatal("empty list is not typed empty", e)
		}
		_, e := x.s.Get(x.ctx, x.portal, "", x.ids[0])
		nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
		_, e = x.s.Get(x.ctx, x.portal, uuid.NewString(), x.ids[0])
		nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
		_, e = x.s.List(x.ctx, x.portal, "", nil)
		nodeReadWant(t, e, "ACCESS_DENIED")
		_, e = x.s.Tokens(x.ctx, x.portal, nil)
		nodeReadWant(t, e, "ACCESS_DENIED")
	})
	t.Run("independent_actual_connection_reopen", func(t *testing.T) {
		owner := &identity.Service{Config: x.owner.Config}
		var e error
		if x.f.PostgreSQL != nil {
			owner.DB, e = pgxpool.NewWithConfig(x.ctx, x.f.PostgreSQL.Config().Copy())
			if e != nil {
				t.Fatal(e)
			}
			defer owner.DB.Close()
		} else {
			c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
			if e != nil {
				t.Fatal(e)
			}
			store, observation, e := mysqlstore.Open(x.ctx, c)
			if e != nil || !observation.TLSAuthenticated {
				t.Fatal("reopen verified TLS", e)
			}
			defer store.Close()
			owner.Storage, e = transaction.FromMySQL(store)
			if e != nil {
				t.Fatal(e)
			}
		}
		s := &nodes.Service{DB: owner.DB, Identity: owner, Workspaces: &workspaces.Service{DB: owner.DB, Identity: owner}}
		n, e := s.Get(x.ctx, x.admin, "", x.ids[0])
		if e != nil || n.ID != x.ids[0] || n.Facts.Hostname != "Schema catalog \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" {
			t.Fatal("actual reopened read lost persisted data", e)
		}
	})
}

func TestActualNodeReadPolicyAndCursor(t *testing.T) {
	x := newNodeReadActual(t)
	p := config.DefaultRuntimePolicy()
	p.UI.PageSize = 3
	p.Nodes.OnlineSeconds = 90
	p.Nodes.OfflineSeconds = 240
	x.owner.Config.Runtime = &p
	for index, age := range []int{60, 180, 480} {
		x.exec(t, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-make_interval(secs=>$1) WHERE id=$2", "UPDATE managed_nodes SET last_heartbeat_at=TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6)) WHERE id=?", age, x.ids[index])
	}
	for index, want := range []string{"online", "unknown", "offline"} {
		n, e := x.s.Get(x.ctx, x.admin, "", x.ids[index])
		if e != nil || n.Connectivity != want {
			t.Fatal("nondefault persisted connectivity policy", index, n.Connectivity, e)
		}
	}
	page, e := x.s.List(x.ctx, x.admin, x.f.Workspace, nil)
	if e != nil || len(page.Items) != 3 || page.NextCursor == nil {
		t.Fatal("configured default page size", e)
	}
	for _, values := range []url.Values{{"limit": {"0"}}, {"limit": {"101"}}, {"limit": {""}}, {"limit": {"1", "2"}}, {"tenant_id": {x.f.Tenant}}} {
		_, e := x.s.List(x.ctx, x.admin, x.f.Workspace, values)
		nodeReadWant(t, e, "VALIDATION_FAILED")
	}
	cursor := url.Values{"limit": {"3"}, "cursor": {*page.NextCursor}}
	_, e = x.s.List(x.ctx, x.portal, x.f.Workspace, cursor)
	nodeReadWant(t, e, "INVALID_CURSOR")
	_, e = x.s.List(x.ctx, x.admin, "", cursor)
	nodeReadWant(t, e, "INVALID_CURSOR")
	_, e = x.s.Tokens(x.ctx, x.admin, cursor)
	nodeReadWant(t, e, "INVALID_CURSOR")
	_, e = x.s.Tasks(x.ctx, x.admin, "", x.ids[0], cursor)
	nodeReadWant(t, e, "INVALID_CURSOR")
	for _, id := range []string{"invalid", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", uuid.Nil.String()} {
		_, e := x.s.Get(x.ctx, x.admin, x.f.Workspace, id)
		nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
	}
	_, e = x.s.List(x.ctx, x.admin, x.f.Workspace, url.Values{"limit": {"100"}})
	if e != nil {
		t.Fatal("upper valid limit refused", e)
	}
}
