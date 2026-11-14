package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type sessionFixture struct {
	s        *Service
	a        identity.Auth
	local    string
	password string
	source   identity.SessionSource
}

// This is an explicit database fixture for source guards and shared issuance, not
// evidence that the product callback, account binding UI or provider flow is implemented.
func sourceFixture(t *testing.T) sessionFixture {
	t.Helper()
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	_, local, _, e := accounts.Login(context.Background(), "admin@example.invalid", password, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	a, e := accounts.Authenticate(context.Background(), local, "admin")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "active_key_id": "test", "keys": map[string]string{"test": base64.StdEncoding.EncodeToString(key)}})
	path := filepath.Join(dir, "sso-keyring.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: path, Providers: []config.SSOProvider{{Origin: "https://provider.example.invalid", Issuer: "https://provider.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	s, e := New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	accounts.Sources = s
	f := sessionFixture{s: s, a: a, local: local, password: password, source: identity.SessionSource{TenantID: a.Me.Workspace.TenantID, ConnectorID: uuid.NewString(), BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}}
	cipher, e := s.keys.seal(secretScope{f.source.TenantID, f.source.ConnectorID, f.source.ConnectorID, clientSecretPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if _, e = accounts.DB.Exec(ctx, `INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES($1,$2,'Synthetic provider','gitea',$3,$4,$5,$6,$7,true,$8)`, f.source.ConnectorID, f.source.TenantID, "https://provider.example.invalid", uuid.NewString(), cipher.keyID, cipher.nonce, cipher.ciphertext, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'9007199254740993',$4)`, f.source.BindingID, f.source.TenantID, f.source.ConnectorID, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	f.pendingFlow(t, f.source.FlowID)
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$2,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1`, f.source.FlowID, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$2,finished_at=clock_timestamp() WHERE id=$1`, f.source.FlowID, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	return f
}

func (f sessionFixture) pendingFlow(t *testing.T, id string) {
	f.pendingFlowForAudience(t, id, "admin")
}

func (f sessionFixture) pendingFlowForAudience(t *testing.T, id, audience string) {
	t.Helper()
	sealed, e := f.s.keys.seal(secretScope{f.source.TenantID, f.source.ConnectorID, id, flowVerifierPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Identity.DB.Exec(context.Background(), `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at) VALUES($1,$2,$3,1,1,$10,'login',$4,$5,$6,$7,$8,$9,clock_timestamp()+interval '5 minutes')`, id, f.source.TenantID, f.source.ConnectorID, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest("anonymous"), sealed.keyID, sealed.nonce, sealed.ciphertext, audience)
	if e != nil {
		t.Fatal(e)
	}
}

func (f sessionFixture) issue(t *testing.T, previous string) identity.IssuedSession {
	t.Helper()
	var issued identity.IssuedSession
	e := f.s.transaction(context.Background(), func(tx pgx.Tx) error {
		var e error
		if e = f.s.Identity.LockSessionUsers(context.Background(), tx, f.a.Me.User.ID, previous, "admin"); e != nil {
			return e
		}
		issued, e = f.s.Identity.IssueSession(context.Background(), tx, f.a.Me.User.ID, f.a.Epoch, "admin", previous, uuid.NewString(), &f.source)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return issued
}

func requireCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s; got %v", code, e)
	}
}

func TestM11ActualMigrationSharedIssuanceCurrentSourceAndLocalRecovery(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	issued := f.issue(t, "")
	if issued.Me.User.ID != f.a.Me.User.ID || issued.Me.Workspace.ID != f.a.Me.Workspace.ID || !security.ValidToken(issued.Token) {
		t.Fatal("ordinary local identity changed")
	}
	a, e := f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.transaction(ctx, func(tx pgx.Tx) error { return f.s.Identity.Recheck(ctx, tx, a) }); e != nil {
		t.Fatal(e)
	}
	// Direct retained-row mutation intentionally omits bulk session revocation. The
	// independent current-source guard must still fence authentication and writes.
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1,updated_at=clock_timestamp() WHERE id=$1`, f.source.ConnectorID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	requireCode(t, f.s.transaction(ctx, func(tx pgx.Tx) error { return f.s.Identity.Recheck(ctx, tx, a) }), "AUTH_SESSION_EXPIRED")
	if _, e = f.s.Identity.Authenticate(ctx, f.local, "admin"); e != nil {
		t.Fatal("local emergency session revoked", e)
	}
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE sso_connectors SET enabled=true,revision=revision+1,auth_version=auth_version+1,updated_at=clock_timestamp() WHERE id=$1`, f.source.ConnectorID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	if _, _, _, e = f.s.Identity.Login(ctx, "admin@example.invalid", f.password, "admin", "", uuid.NewString()); e != nil {
		t.Fatal("local password recovery failed", e)
	}
}

func TestM11ActualMissingDisabledGuardBindingAndReplayFence(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	issued := f.issue(t, "")
	// Each session-source flow is unique even if a caller repeats a successful issue.
	e := f.s.transaction(ctx, func(tx pgx.Tx) error {
		_, e := f.s.Identity.IssueSession(ctx, tx, f.a.Me.User.ID, f.a.Epoch, "admin", "", uuid.NewString(), &f.source)
		return e
	})
	if e == nil {
		t.Fatal("flow established two sessions")
	}
	f.s.Identity.Sources = nil
	_, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	if _, e = f.s.Identity.Authenticate(ctx, f.local, "admin"); e != nil {
		t.Fatal(e)
	}
	disabled, e := New(&identity.Service{DB: f.s.Identity.DB, Config: f.s.Identity.Config})
	if e != nil {
		t.Fatal(e)
	}
	disabled.enabled = false
	f.s.Identity.Sources = disabled
	_, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	f.s.Identity.Sources = f.s
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1`, f.source.BindingID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
}

func TestM11ActualImmutableBindingScopeAndFlowStateConstraints(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	f.pendingFlow(t, id)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE sso_connectors SET origin='https://other.example.invalid',revision=revision+1 WHERE id=$1`, []any{f.source.ConnectorID}},
		{`UPDATE sso_connectors SET enabled=false,revision=revision+1 WHERE id=$1`, []any{f.source.ConnectorID}},
		{`DELETE FROM sso_bindings WHERE id=$1`, []any{f.source.BindingID}},
		{`UPDATE sso_bindings SET subject='2',revision=revision+1 WHERE id=$1`, []any{f.source.BindingID}},
		{`UPDATE sso_flows SET state='succeeded',authenticated_user_id=$2,finished_at=clock_timestamp(),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1`, []any{id, f.a.Me.User.ID}},
		{`UPDATE sso_flows SET state='pending' WHERE id=$1`, []any{f.source.FlowID}},
		{`INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,sso_tenant_id) VALUES($1,$2,'admin',$3,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 hour',$4)`, []any{uuid.NewString(), f.a.Me.User.ID, security.Digest(security.Token()), f.source.TenantID}},
	} {
		if _, e := f.s.Identity.DB.Exec(ctx, statement.sql, statement.args...); e == nil {
			t.Fatal("integrity constraint accepted invalid mutation")
		}
	}
}

func TestM11ActualPasswordConfirmationRechecksSessionAndCredentialWithoutLogin(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	var before, after int
	if e := f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	_, e := f.s.Identity.ConfirmPassword(ctx, f.a, fixture.Password())
	requireCode(t, e, "SSO_PASSWORD_REQUIRED")
	proof, e := f.s.Identity.ConfirmPassword(ctx, f.a, f.password)
	if e != nil {
		t.Fatal(e)
	}
	if proof.Version() != 1 {
		t.Fatal("wrong credential version")
	}
	if e = f.s.transaction(ctx, func(tx pgx.Tx) error { return f.s.Identity.RecheckConfirmation(ctx, tx, f.a, proof) }); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&after); e != nil || before != after {
		t.Fatal("confirmation created a session", e)
	}
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE password_credentials SET version=version+1 WHERE user_id=$1`, f.a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	requireCode(t, f.s.transaction(ctx, func(tx pgx.Tx) error { return f.s.Identity.RecheckConfirmation(ctx, tx, f.a, proof) }), "SSO_PASSWORD_CHANGED")
	if e = f.s.Identity.Logout(ctx, f.local, "admin", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	requireCode(t, f.s.transaction(ctx, func(tx pgx.Tx) error { return f.s.Identity.RecheckConfirmation(ctx, tx, f.a, proof) }), "AUTH_SESSION_EXPIRED")
}

func TestM11ActualFlowCannotReissueAfterOrdinarySessionRetention(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	issued := f.issue(t, "")
	if _, e := f.s.Identity.DB.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, issued.SessionID); e != nil {
		t.Fatal(e)
	}
	e := f.s.transaction(ctx, func(tx pgx.Tx) error {
		_, e := f.s.Identity.IssueSession(ctx, tx, f.a.Me.User.ID, f.a.Epoch, "admin", "", uuid.NewString(), &f.source)
		return e
	})
	if e == nil {
		t.Fatal("retained successful flow issued a second session after original session retention")
	}
	var id string
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT issued_session_id::text FROM sso_flows WHERE id=$1`, f.source.FlowID).Scan(&id); e != nil || id != issued.SessionID {
		t.Fatal("single-issuance journal was lost", e)
	}
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE sso_flows SET issued_session_id=NULL WHERE id=$1`, f.source.FlowID); e == nil {
		t.Fatal("issuance journal could be cleared")
	}
}
