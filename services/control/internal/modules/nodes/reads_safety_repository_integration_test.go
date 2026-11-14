//go:build repository_integration

package nodes_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func nodeReadPeer(t *testing.T, x nodeReadActual, role string) identity.Auth {
	t.Helper()
	id := uuid.NewString()
	address := "synthetic+" + id + "@example.invalid"
	x.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Synthetic node reader','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Synthetic node reader','en','active')", id, address, address)
	if role != "" {
		x.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,$4)", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,?)", id, x.f.Tenant, x.f.Workspace, role)
	}
	return x.issue(t, id, "portal")
}
func nodeReadRefused(t *testing.T, x nodeReadActual, a identity.Auth, scope, code string, tokens bool) {
	t.Helper()
	before := x.audits(t)
	value, e := x.s.Get(x.ctx, a, scope, x.ids[0])
	nodeReadWant(t, e, code)
	if value.ID != "" || value.Facts.Hostname != "" {
		t.Fatal("refused actor received node DTO")
	}
	page, e := x.s.List(x.ctx, a, scope, nil)
	nodeReadWant(t, e, code)
	if page.Items != nil || page.NextCursor != nil {
		t.Fatal("refused actor received node page")
	}
	history, e := x.s.Tasks(x.ctx, a, scope, x.ids[0], nil)
	nodeReadWant(t, e, code)
	if history.Items != nil || history.NextCursor != nil {
		t.Fatal("refused actor received task page")
	}
	if tokens {
		p, e := x.s.Tokens(x.ctx, a, nil)
		nodeReadWant(t, e, code)
		if p.Items != nil || p.NextCursor != nil {
			t.Fatal("refused actor received token page")
		}
	}
	if x.audits(t) != before {
		t.Fatal("refused reads mutated audit")
	}
}

func TestActualNodeReadAuthority(t *testing.T) {
	for _, kind := range []string{"owner_member_viewer", "missing_membership", "revoked_membership", "disabled_workspace", "revoked_session", "demoted_admin", "disabled_user", "wrong_token", "wrong_user", "foreign_tenant", "inactive_tenant", "unbootstrapped_installation"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			switch kind {
			case "owner_member_viewer":
				for _, role := range []string{"owner", "member", "viewer"} {
					a := nodeReadPeer(t, x, role)
					if _, e := x.s.Get(x.ctx, a, x.f.Workspace, x.ids[0]); e != nil {
						t.Fatal(role, e)
					}
					if p, e := x.s.List(x.ctx, a, x.f.Workspace, nil); e != nil || len(p.Items) != 9 {
						t.Fatal(role, e)
					}
					if p, e := x.s.Tasks(x.ctx, a, x.f.Workspace, x.ids[0], nil); e != nil || len(p.Items) != 9 {
						t.Fatal(role, e)
					}
				}
			case "missing_membership":
				nodeReadRefused(t, x, nodeReadPeer(t, x, ""), x.f.Workspace, "RESOURCE_NOT_FOUND", false)
			case "revoked_membership":
				x.exec(t, "DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2", "DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?", x.f.User, x.f.Workspace)
				nodeReadRefused(t, x, x.portal, x.f.Workspace, "RESOURCE_NOT_FOUND", false)
				if _, e := x.s.Get(x.ctx, x.admin, "", x.ids[0]); e != nil {
					t.Fatal("current Admin lost deliberate membership-free inspection", e)
				}
			case "disabled_workspace":
				x.exec(t, "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1", "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?", x.f.Workspace)
				nodeReadRefused(t, x, x.portal, x.f.Workspace, "RESOURCE_NOT_FOUND", false)
				if _, e := x.s.Get(x.ctx, x.admin, x.f.Workspace, x.ids[0]); e != nil {
					t.Fatal("current Admin cannot inspect disabled scope", e)
				}
			case "revoked_session", "demoted_admin", "disabled_user", "wrong_token", "wrong_user":
				a := x.admin
				switch kind {
				case "revoked_session":
					x.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_node_read' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_node_read' WHERE id=?", a.SessionID)
				case "demoted_admin":
					x.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", a.Me.User.ID)
				case "disabled_user":
					x.exec(t, "UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?", a.Me.User.ID)
				case "wrong_token":
					a.TokenHash = append([]byte{}, a.TokenHash...)
					a.TokenHash[0] ^= 1
				case "wrong_user":
					a.Me.User.ID = nodeReadPeer(t, x, "member").Me.User.ID
				}
				nodeReadRefused(t, x, a, x.f.Workspace, "AUTH_SESSION_EXPIRED", true)
			case "foreign_tenant":
				tenant, workspace, node, token := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
				credential := sha256.Sum256([]byte(uuid.NewString()))
				join := sha256.Sum256([]byte(uuid.NewString()))
				x.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign node tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign node tenant','active')", tenant)
				x.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign node Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign node Workspace','active')", workspace, tenant)
				if _, e := x.f.Store.Exec(x.ctx, x.f.Statement("INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES($1,$2,$3,'Synthetic foreign node','active',$4,'1.0','{}',clock_timestamp())", "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES(?,?,?,'Synthetic foreign node','active',?,'1.0','{}',UTC_TIMESTAMP(6))", node, tenant, workspace, credential[:])); e != nil {
					t.Fatal(e)
				}
				x.exec(t, "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES($1,$2,$3,'Synthetic foreign token',$4,$5,clock_timestamp()+interval '1 hour')", "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES(?,?,?,'Synthetic foreign token',?,?,TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))", token, tenant, workspace, join[:], x.f.User)
				_, e := x.s.Get(x.ctx, x.admin, "", node)
				nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
				_, e = x.s.Get(x.ctx, x.admin, workspace, node)
				nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
				_, e = x.s.List(x.ctx, x.admin, workspace, nil)
				nodeReadWant(t, e, "RESOURCE_NOT_FOUND")
				if p, e := x.s.List(x.ctx, x.admin, "", nil); e != nil || len(p.Items) != 9 {
					t.Fatal("foreign node escaped installed scope", e)
				}
				if p, e := x.s.Tokens(x.ctx, x.admin, nil); e != nil || len(p.Items) != 9 {
					t.Fatal("foreign token escaped installed scope", e)
				}
			case "inactive_tenant":
				x.exec(t, "UPDATE tenants SET status='disabled' WHERE id=$1", "UPDATE tenants SET status='disabled' WHERE id=?", x.f.Tenant)
				nodeReadRefused(t, x, x.admin, x.f.Workspace, "RESOURCE_NOT_FOUND", true)
			case "unbootstrapped_installation":
				x.exec(t, "UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1", "UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1")
				nodeReadRefused(t, x, x.admin, x.f.Workspace, "RESOURCE_NOT_FOUND", true)
			}
		})
	}
}

// Hold an actual Workspace row in a separate real transaction. The test owns
// its independent cleanup deadline so cancellation of a product call cannot
// strand the lock holder or prevent fixture teardown.
func nodeReadHoldWorkspace(t *testing.T, x nodeReadActual, after func(context.Context, transaction.Executor) error) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	ready, finished, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var result error
	go func() {
		result = x.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, x.f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", x.f.Workspace)).Scan(&id); e != nil {
				return e
			}
			close(ready)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			if after != nil {
				return after(ctx, q)
			}
			return nil
		})
		close(finished)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(release)
			<-finished
			cancel()
			if result != nil {
				t.Error("actual lock holder cleanup", result)
			}
		})
	}
	t.Cleanup(stop)
	select {
	case <-ready:
	case <-finished:
		t.Fatal("actual Workspace lock not acquired", result)
	case <-ctx.Done():
		t.Fatal("actual Workspace lock admission timed out")
	}
	return stop
}
func nodeReadFailedContext(t *testing.T, x nodeReadActual, ctx context.Context, code string) {
	t.Helper()
	v, e := x.s.Get(ctx, x.admin, x.f.Workspace, x.ids[0])
	nodeReadWant(t, e, code)
	if v.ID != "" {
		t.Fatal("failed Get exposed private DTO")
	}
	p, e := x.s.List(ctx, x.admin, x.f.Workspace, nil)
	nodeReadWant(t, e, code)
	if p.Items != nil || p.NextCursor != nil {
		t.Fatal("failed List exposed private page")
	}
	j, e := x.s.Tokens(ctx, x.admin, nil)
	nodeReadWant(t, e, code)
	if j.Items != nil || j.NextCursor != nil {
		t.Fatal("failed Tokens exposed private page")
	}
	h, e := x.s.Tasks(ctx, x.admin, x.f.Workspace, x.ids[0], nil)
	nodeReadWant(t, e, code)
	if h.Items != nil || h.NextCursor != nil {
		t.Fatal("failed Tasks exposed private page")
	}
}
func TestActualNodeReadFailures(t *testing.T) {
	for _, kind := range []string{"damaged_facts", "actual_query_error", "cancelled_caller", "shorter_caller_deadline", "owned_background_deadline"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			before := x.audits(t)
			switch kind {
			case "damaged_facts":
				var original []byte
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT facts FROM managed_nodes WHERE id=$1", "SELECT facts FROM managed_nodes WHERE id=?", x.ids[0])).Scan(&original); e != nil {
					t.Fatal(e)
				}
				x.exec(t, "UPDATE managed_nodes SET facts='[]' WHERE id=$1", "UPDATE managed_nodes SET facts='[]' WHERE id=?", x.ids[0])
				v, e := x.s.Get(x.ctx, x.admin, x.f.Workspace, x.ids[0])
				nodeReadWant(t, e, "STORAGE_UNAVAILABLE")
				if v.ID != "" {
					t.Fatal("damaged facts exposed partial node")
				}
				p, e := x.s.List(x.ctx, x.admin, x.f.Workspace, nil)
				nodeReadWant(t, e, "STORAGE_UNAVAILABLE")
				if p.Items != nil || p.NextCursor != nil {
					t.Fatal("damaged facts exposed partial catalog")
				}
				h, e := x.s.Tasks(x.ctx, x.admin, x.f.Workspace, x.ids[0], nil)
				nodeReadWant(t, e, "STORAGE_UNAVAILABLE")
				if h.Items != nil {
					t.Fatal("damaged facts exposed private history")
				}
				x.exec(t, "UPDATE managed_nodes SET facts=$1 WHERE id=$2", "UPDATE managed_nodes SET facts=? WHERE id=?", original, x.ids[0])
				if _, e := x.s.Get(x.ctx, x.admin, "", x.ids[0]); e != nil {
					t.Fatal("explicit actual data repair did not recover read", e)
				}
			case "actual_query_error":
				x.exec(t, "DROP TABLE node_join_tokens", "DROP TABLE node_join_tokens")
				p, e := x.s.Tokens(x.ctx, x.admin, nil)
				nodeReadWant(t, e, "STORAGE_UNAVAILABLE")
				if p.Items != nil || p.NextCursor != nil {
					t.Fatal("actual SQL failure exposed token page")
				}
			case "cancelled_caller":
				ctx, cancel := context.WithCancel(x.ctx)
				cancel()
				nodeReadFailedContext(t, x, ctx, "STORAGE_CANCELLED")
				if _, e := x.s.Get(x.ctx, x.admin, "", x.ids[0]); e != nil {
					t.Fatal("cancelled connection did not recover", e)
				}
			case "shorter_caller_deadline", "owned_background_deadline":
				stop := nodeReadHoldWorkspace(t, x, nil)
				ctx, cancel := context.WithTimeout(x.ctx, 80*time.Millisecond)
				if kind == "owned_background_deadline" {
					cancel()
					ctx = context.Background()
					cancel = func() {}
					p := config.DefaultRuntimePolicy()
					p.HTTP.RequestSeconds = 2
					p.HTTP.ReadHeaderSeconds = 1
					if e := p.Validate(); e != nil {
						t.Fatal("nondefault owned policy invalid", e)
					}
					x.owner.Config.Runtime = &p
					if x.f.Store.Dialect() == transaction.MySQL {
						// The ordinary fixture intentionally has a one-second lock limit.
						// This separate real pool isolates the longer-lock prerequisite
						// for observing the valid two-second owning operation deadline.
						c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
						if e != nil {
							t.Fatal(e)
						}
						c.Policy.LockSeconds, c.Policy.StatementSeconds = 5, 5
						c.Policy.ReadSeconds, c.Policy.TransactionSeconds = 5, 5
						kernel, observation, e := mysqlstore.Open(x.ctx, c)
						if e != nil || !observation.TLSAuthenticated {
							t.Fatal("actual isolated deadline pool", e)
						}
						defer kernel.Close()
						var lockSeconds int
						if e = kernel.Pool().QueryRowContext(x.ctx, "SELECT @@session.innodb_lock_wait_timeout").Scan(&lockSeconds); e != nil || lockSeconds != 5 {
							t.Fatal("actual longer lock prerequisite missing", e)
						}
						x.owner.Storage, e = transaction.FromMySQL(kernel)
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				defer cancel()
				started := time.Now()
				v, e := x.s.Get(ctx, x.admin, x.f.Workspace, x.ids[0])
				nodeReadWant(t, e, "STORAGE_TIMEOUT")
				if v.ID != "" {
					t.Fatal("timed-out actual lock wait exposed private node")
				}
				elapsed := time.Since(started)
				if kind == "owned_background_deadline" && (elapsed < 1500*time.Millisecond || elapsed > 4*time.Second) {
					t.Fatal("owned real background deadline not enforced", elapsed)
				}
				if kind == "shorter_caller_deadline" && elapsed > time.Second {
					t.Fatal("shorter caller deadline was extended", elapsed)
				}
				stop()
				if _, e := x.s.Get(x.ctx, x.admin, "", x.ids[0]); e != nil {
					t.Fatal("actual lock/connection recovery failed", e)
				}
			}
			if x.audits(t) != before {
				t.Fatal("failure/recovery reads changed audit")
			}
		})
	}
}

// This explicitly identified SQL observer defers only QueryRow until Scan so it
// can observe the typed Workspace boundary. It forwards the exact statement and
// actual driver result;no fake SQL response or vendor transaction is introduced.
type nodeReadScopeStore struct {
	transaction.Store
	attempt chan struct{}
	once    sync.Once
}
type nodeReadScopeExecutor struct {
	transaction.Executor
	owner *nodeReadScopeStore
}
type nodeReadScopeRow struct {
	ctx       context.Context
	statement transaction.Statement
	q         transaction.Executor
	owner     *nodeReadScopeStore
}

func (s *nodeReadScopeStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error { return fn(ctx, nodeReadScopeExecutor{q, s}) })
}
func (q nodeReadScopeExecutor) QueryRow(ctx context.Context, s transaction.Statement) transaction.Row {
	return nodeReadScopeRow{ctx, s, q.Executor, q.owner}
}
func (r nodeReadScopeRow) Scan(dest ...any) error {
	if len(dest) == 8 {
		r.owner.once.Do(func() { close(r.owner.attempt) })
	}
	return r.q.QueryRow(r.ctx, r.statement).Scan(dest...)
}
func TestActualNodeReadMembershipAfterLockWait(t *testing.T) {
	x := newNodeReadActual(t)
	stop := nodeReadHoldWorkspace(t, x, func(ctx context.Context, q transaction.Executor) error {
		_, e := q.Exec(ctx, x.f.Statement("DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2", "DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?", x.f.User, x.f.Workspace))
		return e
	})
	observed := &nodeReadScopeStore{Store: x.f.Store, attempt: make(chan struct{})}
	x.owner.Storage, x.owner.DB = observed, nil
	x.s.DB, x.s.Workspaces.DB = nil, nil
	type result struct {
		node nodes.Node
		err  error
	}
	done := make(chan result, 1)
	go func() { n, e := x.s.Get(x.ctx, x.portal, x.f.Workspace, x.ids[0]); done <- result{n, e} }()
	select {
	case <-observed.attempt:
	case r := <-done:
		t.Fatal("read did not reach actual Workspace fence", r.err)
	case <-x.ctx.Done():
		t.Fatal("Workspace read admission timed out")
	}
	select {
	case r := <-done:
		t.Fatal("private read escaped held Workspace fence", r.err)
	case <-time.After(100 * time.Millisecond):
	}
	stop()
	select {
	case r := <-done:
		nodeReadWant(t, r.err, "RESOURCE_NOT_FOUND")
		if r.node.ID != "" {
			t.Fatal("revoked membership returned stale private node")
		}
	case <-x.ctx.Done():
		t.Fatal("read did not recover after Workspace lock release")
	}
	if _, e := x.s.Get(x.ctx, x.admin, "", x.ids[0]); e != nil {
		t.Fatal("current local Admin recovery failed", e)
	}
}

type nodeReadSource struct {
	provider  *sso.Service
	connector sso.Connector
	actor     identity.Auth
	source    identity.SessionSource
}

func nodeReadSourcedActor(t *testing.T, x nodeReadActual) nodeReadSource {
	t.Helper()
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(map[string]any{"schema_version": 1, "active_key_id": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(directory, "node-read-sso-keyring.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	x.owner.HMACKey = make([]byte, 32)
	if _, e := rand.Read(x.owner.HMACKey); e != nil {
		t.Fatal(e)
	}
	x.owner.Config.Server.PortalOrigin, x.owner.Config.Server.AdminOrigin = "https://portal.example.invalid", "https://admin.example.invalid"
	x.owner.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: path, Providers: []config.SSOProvider{{Origin: "https://provider.example.invalid", Issuer: "https://provider.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	provider, e := sso.New(x.owner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(provider.Close)
	x.owner.Sources = provider
	connector, e := provider.CreateConnector(x.ctx, x.admin, sso.CreateConnector{ID: uuid.NewString(), Name: "Synthetic node read source \u4e16\u754c", Provider: sso.ProviderGitea, Origin: "https://provider.example.invalid", ClientID: uuid.NewString(), ClientSecret: uuid.NewString() + uuid.NewString(), Enabled: true, Reason: "Isolated node read source prerequisite"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual encrypted source prerequisite", e)
	}
	source := identity.SessionSource{TenantID: x.f.Tenant, ConnectorID: connector.ID, BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}
	x.exec(t, "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'9007199254740993',$4)", "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,'9007199254740993',?)", source.BindingID, source.TenantID, source.ConnectorID, x.f.User)
	state, browser, cookie := sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString()))
	// Explicit synthetic provider history only. Real encryption,SQL transitions,
	// FKs,ordinary session issuance and current source checks remain enabled.
	// No provider request,PKCE exchange or real SSO login is claimed here.
	x.exec(t, "INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at) VALUES($1,$2,$3,1,1,'admin','login',$4,$5,$6,'synthetic',$7,$8,clock_timestamp()+interval '1 hour')", "INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at) VALUES(?,?,?,1,1,'admin','login',?,?,?,'synthetic',?,?,TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))", source.FlowID, source.TenantID, source.ConnectorID, state[:], browser[:], cookie[:], make([]byte, 12), make([]byte, 59))
	x.exec(t, "UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2", "UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?", uuid.NewString(), source.FlowID)
	x.exec(t, "UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE id=$2", "UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=UTC_TIMESTAMP(6) WHERE id=?", x.f.User, source.FlowID)
	var issued identity.IssuedSession
	e = x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = x.owner.IssueSessionWithin(ctx, q, x.f.User, 1, "admin", "", uuid.NewString(), &source)
		return e
	})
	if e != nil {
		t.Fatal("actual sourced node read session issuance", e)
	}
	actor, e := x.owner.Authenticate(x.ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	return nodeReadSource{provider, connector, actor, source}
}
func TestActualNodeReadSourceAuthority(t *testing.T) {
	for _, kind := range []string{"connector_disabled", "binding_inactive_with_unrevoked_session"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			source := nodeReadSourcedActor(t, x)
			if _, e := x.s.Get(x.ctx, source.actor, "", x.ids[0]); e != nil {
				t.Fatal("current sourced node authority refused", e)
			}
			switch kind {
			case "connector_disabled":
				_, e := source.provider.UpdateConnector(x.ctx, x.admin, source.connector.ID, sso.UpdateConnector{Revision: 1, Name: source.connector.Name, Enabled: false, Reason: "Isolated current node source revocation"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
			case "binding_inactive_with_unrevoked_session":
				// Deliberately retain the real session row to prove current binding
				// authority is checked even before bulk session revocation finishes.
				x.exec(t, "UPDATE sso_bindings SET active=false,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", "UPDATE sso_bindings SET active=false,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", source.source.BindingID)
				var activeSession bool
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT revoked_at IS NULL FROM sessions WHERE id=$1", "SELECT revoked_at IS NULL FROM sessions WHERE id=?", source.actor.SessionID)).Scan(&activeSession); e != nil || !activeSession {
					t.Fatal("explicit unrevoked session prerequisite missing", e)
				}
			}
			nodeReadRefused(t, x, source.actor, x.f.Workspace, "AUTH_SESSION_EXPIRED", true)
			if _, e := x.s.Get(x.ctx, x.admin, "", x.ids[0]); e != nil {
				t.Fatal("local node administrator recovery after source revocation failed", e)
			}
		})
	}
}
