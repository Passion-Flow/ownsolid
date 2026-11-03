//go:build repository_integration

package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

type sourceRepositoryFixture struct {
	f        repositoryfixture.Fixture
	accounts *identity.Service
	s        *Service
	source   identity.SessionSource
	ctx      context.Context
}

func newSourceRepositoryFixture(t *testing.T) sourceRepositoryFixture {
	t.Helper()
	f := repositoryfixture.NewUnbootstrapped(t)
	seedContext, seedCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer seedCancel()
	if e := f.Store.Within(seedContext, false, func(ctx context.Context, tx transaction.Executor) error {
		email := "synthetic+" + f.User + "@example.invalid"
		for _, query := range []transaction.Statement{
			f.Statement("INSERT INTO tenants(id,name,status) VALUES($1,'Repository tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Repository tenant','active')", f.Tenant),
			f.Statement("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Repository Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Repository Workspace','active')", f.Workspace, f.Tenant),
			f.Statement("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Repository actor','en','active',true)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Repository actor','en','active',true)", f.User, email, email),
			f.Statement("UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1", "UPDATE installation_state SET bootstrap_complete=true,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1", f.Tenant, f.Workspace, f.User),
		} {
			if _, e := tx.Exec(ctx, query); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal("actual unique synthetic source fixture seed", e)
	}
	accounts := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		accounts.Storage, accounts.DB = nil, f.PostgreSQL
	}
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(map[string]any{"schema_version": 1, "active_key_id": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	path := filepath.Join(dir, "sso-keyring.json")
	if e := os.WriteFile(path, encoded, 0600); e != nil {
		t.Fatal(e)
	}
	accounts.Config.Server.PortalOrigin = "https://portal.example.invalid"
	accounts.Config.Server.AdminOrigin = "https://admin.example.invalid"
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: path, Providers: []config.SSOProvider{{Origin: "https://provider.example.invalid", Issuer: "https://provider.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	s, e := New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	out := sourceRepositoryFixture{f: f, accounts: accounts, s: s, ctx: ctx, source: identity.SessionSource{TenantID: f.Tenant, ConnectorID: uuid.NewString(), BindingID: uuid.NewString(), AuthVersion: 1}}
	sealed, e := s.keys.seal(secretScope{f.Tenant, out.source.ConnectorID, out.source.ConnectorID, clientSecretPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	out.exec(t, f.Statement(
		"INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES($1,$2,'Synthetic source \u4e16\u754c','gitea',$3,$4,$5,$6,$7,true,$8)",
		"INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES(?,?,'Synthetic source \u4e16\u754c','gitea',?,?,?,?,?,true,?)", out.source.ConnectorID, f.Tenant, accounts.Config.SSO.Providers[0].Origin, uuid.NewString(), sealed.keyID, sealed.nonce, sealed.ciphertext, f.User))
	out.exec(t, f.Statement("INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'9007199254740993',$4)", "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,'9007199254740993',?)", out.source.BindingID, f.Tenant, out.source.ConnectorID, f.User))
	out.source.FlowID = out.pending(t, false, false)
	out.succeed(t, out.source.FlowID)
	return out
}

func (f sourceRepositoryFixture) exec(t *testing.T, query transaction.Statement) {
	t.Helper()
	if _, e := f.f.Store.Exec(f.ctx, query); e != nil {
		t.Fatal(e)
	}
}
func (f sourceRepositoryFixture) pending(t *testing.T, expired, corrupt bool) string {
	t.Helper()
	id := uuid.NewString()
	sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, f.source.ConnectorID, id, flowVerifierPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	if corrupt {
		sealed.ciphertext[0] ^= 1
	}
	var now time.Time
	if e = f.f.Store.QueryRow(f.ctx, f.f.Statement("SELECT clock_timestamp()", "SELECT UTC_TIMESTAMP(6)")).Scan(&now); e != nil {
		t.Fatal(e)
	}
	created, expiry := now, now.Add(time.Hour)
	if expired {
		created, expiry = now.Add(-2*time.Hour), now.Add(-time.Hour)
	}
	f.exec(t, f.f.Statement(
		"INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES($1,$2,$3,1,1,'admin','login',$4,$5,$6,$7,$8,$9,$10,$11)",
		"INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,1,1,'admin','login',?,?,?,?,?,?,?,?)", id, f.f.Tenant, f.source.ConnectorID, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest("anonymous"), sealed.keyID, sealed.nonce, sealed.ciphertext, created, expiry))
	return id
}
func (f sourceRepositoryFixture) succeed(t *testing.T, id string) {
	t.Helper()
	f.exec(t, f.f.Statement("UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2", "UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?", uuid.NewString(), id))
	f.exec(t, f.f.Statement("UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE id=$2", "UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=UTC_TIMESTAMP(6) WHERE id=?", f.f.User, id))
}

func TestActualSSOSourceRepository(t *testing.T) {
	t.Run("exact_source_and_scope_denials", func(t *testing.T) {
		f := newSourceRepositoryFixture(t)
		within := func(source identity.SessionSource, user string) error {
			return f.f.Store.Within(f.ctx, false, func(ctx context.Context, tx transaction.Executor) error {
				var actor string
				if e := tx.QueryRow(ctx, f.f.Statement("SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", f.f.User)).Scan(&actor); e != nil {
					return e
				}
				return f.s.CheckSessionSourceWithin(ctx, tx, source, user)
			})
		}
		if e := f.s.CheckSessionSource(f.ctx, nil, f.source, f.f.User); e != nil {
			t.Fatal(e)
		}
		if e := within(f.source, f.f.User); e != nil {
			t.Fatal(e)
		}
		for _, change := range []func(*identity.SessionSource){
			func(s *identity.SessionSource) { s.TenantID = uuid.NewString() }, func(s *identity.SessionSource) { s.ConnectorID = uuid.NewString() }, func(s *identity.SessionSource) { s.BindingID = uuid.NewString() }, func(s *identity.SessionSource) { s.FlowID = uuid.NewString() }, func(s *identity.SessionSource) { s.AuthVersion++ }, func(s *identity.SessionSource) { s.FlowID = uuid.Nil.String() },
		} {
			bad := f.source
			change(&bad)
			for _, e := range []error{f.s.CheckSessionSource(f.ctx, nil, bad, f.f.User), within(bad, f.f.User)} {
				if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
					t.Fatal("cross-scope source accepted", e)
				}
			}
		}
		if e := within(f.source, uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("wrong local user accepted", e)
		}
		pending := f.source
		pending.FlowID = f.pending(t, false, false)
		if e := within(pending, f.f.User); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("pending provider flow established authority", e)
		}
		approved := f.s.approved
		f.s.approved = map[string]config.SSOProvider{}
		if e := within(f.source, f.f.User); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("unapproved origin accepted", e)
		}
		f.s.approved = approved
		f.exec(t, f.f.Statement("UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1", "UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=?", f.source.BindingID))
		if e := within(f.source, f.f.User); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("inactive binding accepted", e)
		}
		f.exec(t, f.f.Statement("UPDATE sso_bindings SET active=true,revision=revision+1 WHERE id=$1", "UPDATE sso_bindings SET active=true,revision=revision+1 WHERE id=?", f.source.BindingID))
		f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
		if e := within(f.source, f.f.User); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("disabled/rotated connector accepted", e)
		}
	})
	for _, kind := range []string{"connector", "binding"} {
		t.Run(kind+"_fence_and_unrelated_row_progress", func(t *testing.T) {
			f := newSourceRepositoryFixture(t)
			result := make(chan error, 1)
			e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, tx transaction.Executor) error {
				var user string
				if e := tx.QueryRow(ctx, f.f.Statement("SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", f.f.User)).Scan(&user); e != nil {
					return e
				}
				if e := f.s.CheckSessionSourceWithin(ctx, tx, f.source, f.f.User); e != nil {
					return e
				}
				progress := make(chan error, 1)
				go func() {
					_, e := f.f.Store.Exec(f.ctx, f.f.Statement("UPDATE installation_state SET bootstrapped_at=bootstrapped_at WHERE id=1", "UPDATE installation_state SET bootstrapped_at=bootstrapped_at WHERE id=1"))
					progress <- e
				}()
				select {
				case e := <-progress:
					if e != nil {
						return e
					}
				case <-time.After(500 * time.Millisecond):
					t.Fatal("source fence overlocked installation row")
				}
				go func() {
					query := f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID)
					if kind == "binding" {
						query = f.f.Statement("UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1", "UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=?", f.source.BindingID)
					}
					_, e := f.f.Store.Exec(f.ctx, query)
					result <- e
				}()
				select {
				case <-result:
					t.Fatal("revocation escaped actual shared source fence")
				case <-time.After(150 * time.Millisecond):
				}
				return audit.WriteWithin(ctx, tx, f.f.User, f.f.User, "admin", "auth.synthetic_source_checked", "success", "user", f.f.User, "", uuid.NewString())
			})
			if e != nil {
				t.Fatal(e)
			}
			if e = <-result; e != nil {
				t.Fatal("waiting actual revocation failed", e)
			}
			if e = f.s.CheckSessionSource(f.ctx, nil, f.source, f.f.User); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
				t.Fatal("revoked authority still authenticates", e)
			}
		})
	}
	t.Run("enabled_startup_decrypts_and_refuses_corruption", func(t *testing.T) {
		f := newSourceRepositoryFixture(t)
		missing := *f.accounts
		missing.DB, missing.Storage = nil, nil
		if next, e := New(&missing); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" {
			if next != nil {
				next.Close()
			}
			t.Fatal("enabled component accepted missing storage", e)
		}
		if f.f.PostgreSQL != nil {
			ambiguous := *f.accounts
			ambiguous.Storage = f.f.Store
			if next, e := New(&ambiguous); e == nil || e.Error() != "IDENTITY_STORAGE_CONFIGURATION_CONFLICT" {
				if next != nil {
					next.Close()
				}
				t.Fatal("enabled component accepted ambiguous storage", e)
			}
		}
		f.pending(t, true, true)
		if next, e := New(f.accounts); e != nil {
			t.Fatal("expired corrupt verifier blocked recovery startup", e)
		} else {
			next.Close()
		}
		f.pending(t, false, false)
		if next, e := New(f.accounts); e != nil {
			t.Fatal("actual scoped encrypted verifier refused", e)
		} else {
			next.Close()
		}
		f.pending(t, false, true)
		if next, e := New(f.accounts); e == nil || e.Error() != "SSO_ENCRYPTED_STATE_INVALID" {
			if next != nil {
				next.Close()
			}
			t.Fatal("active corrupt verifier accepted", e)
		}
	})
	for _, kind := range []string{"wrong_AAD", "unknown_key"} {
		t.Run("startup_rejects_client_secret_"+kind, func(t *testing.T) {
			f := newSourceRepositoryFixture(t)
			wrong := uuid.NewString()
			sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, wrong, wrong, clientSecretPurpose}, []byte(security.Token()))
			if e != nil {
				t.Fatal(e)
			}
			if kind == "unknown_key" {
				sealed.keyID = "unknown-retained-key"
			}
			f.exec(t, f.f.Statement("UPDATE sso_connectors SET secret_key_id=$1,secret_nonce=$2,secret_ciphertext=$3,revision=revision+1,auth_version=auth_version+1 WHERE id=$4", "UPDATE sso_connectors SET secret_key_id=?,secret_nonce=?,secret_ciphertext=?,revision=revision+1,auth_version=auth_version+1 WHERE id=?", sealed.keyID, sealed.nonce, sealed.ciphertext, f.source.ConnectorID))
			if next, e := New(f.accounts); e == nil || e.Error() != "SSO_ENCRYPTED_STATE_INVALID" {
				if next != nil {
					next.Close()
				}
				t.Fatal("invalid actual stored client secret accepted", e)
			}
		})
	}
}
