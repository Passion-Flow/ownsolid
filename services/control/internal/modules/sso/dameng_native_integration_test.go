//go:build cgo && odbc_manager && dameng_sso_integration

package sso

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Actual native repository fixture. Gitea exchange is deliberately absent:
// commitFlow receives an explicit synthetic subject, and Complete exercises only
// denial. This is neither live-provider nor HTTP/cookie qualification.
func TestDamengSSORepositoryNative(t *testing.T) {
	path := os.Getenv("PROJECT_ONE_DAMENG_SSO_CONFIGURATION")
	schema := os.Getenv("PROJECT_ONE_DAMENG_SSO_DISPOSABLE_SCHEMA")
	if path == "" || !strings.HasPrefix(schema, "P1_SSO_TEST_") {
		t.Fatal("protected configuration and exclusive disposable P1_SSO_TEST_ schema required")
	}
	c, e := dameng.LoadConnectionConfiguration(path, "")
	if e != nil || c.Schema != schema || c.Policy.MaximumOpenConnections < 3 || c.Policy.MaximumPhysicalConnections < 3 {
		t.Fatal("native configuration/schema admission refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	native, _, e := dameng.Open(ctx, c)
	if native != nil {
		t.Cleanup(func() {
			closeCtx, done := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer done()
			status, e := native.Close(closeCtx)
			if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
				t.Error("native cleanup incomplete; retain recovery evidence")
			}
		})
	}
	if e != nil {
		t.Fatal("native admission failed", dameng.SafeError(e))
	}
	must := func(label string, e error) {
		t.Helper()
		if e != nil {
			t.Fatal(label, e)
		}
	}
	store, e := transaction.FromDameng(native)
	must("repository", e)
	count := func(table string) int {
		t.Helper()
		var n int
		must("fixed fixture table", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM "+table)).Scan(&n))
		return n
	}
	// Identifiers are fixed below; never use a caller-provided table name.
	for _, table := range []string{"TENANTS", "USERS", "PASSWORD_CREDENTIALS", "WORKSPACES", "WORKSPACE_MEMBERSHIPS", "WORKSPACE_NOTES", "SESSIONS", "INVITATIONS", "PASSWORD_RESET_TOKENS", "MAIL_DELIVERIES", "AUDIT_EVENTS", "RATE_LIMIT_BUCKETS", "IDEMPOTENCY_RECORDS", "SSO_CONNECTORS", "SSO_BINDINGS", "SSO_FLOWS", "SSO_MUTATION_INTENTS", "USER_PREFERENCES"} {
		if count(table) != 0 {
			t.Fatal("nonempty fixture refused", table)
		}
	}
	var complete bool
	if count("INSTALLATION_STATE") != 1 {
		t.Fatal("unbootstrapped singleton required")
	}
	must("singleton", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT BOOTSTRAP_COMPLETE FROM INSTALLATION_STATE WHERE ID=1")).Scan(&complete))
	if complete {
		t.Fatal("bootstrapped fixture refused")
	}
	root := t.TempDir()
	blocklist := filepath.Join(root, "synthetic-blocklist")
	must("private blocklist", os.WriteFile(blocklist, []byte("passwordpasswordpassword\n"), 0600))
	passwords, e := security.NewPasswords(blocklist)
	must("password owner", e)
	key := make([]byte, 32)
	_, e = rand.Read(key)
	must("synthetic key", e)
	keyring := filepath.Join(root, "synthetic-sso-keyring.json")
	raw, e := json.Marshal(map[string]any{"schema_version": 1, "active_key_id": "test", "keys": map[string]string{"test": base64.StdEncoding.EncodeToString(key)}})
	must("key encoding", e)
	must("private keyring", os.WriteFile(keyring, raw, 0600))
	accounts := &identity.Service{Storage: store, Passwords: passwords, HMACKey: key}
	accounts.Config.Server.AdminOrigin = "https://admin.example.invalid"
	accounts.Config.Server.PortalOrigin = "https://portal.example.invalid"
	origin := "https://provider.example.invalid"
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: origin, Issuer: origin, DialAddresses: []string{"127.0.0.1"}}}}
	password := "Synthetic native password " + uuid.NewString()
	must("bootstrap", accounts.Bootstrap(ctx, "operator@example.invalid", "Operator \u4e16\u754c", password, "SSO native fixture", "Default \u7a7a\u95f4"))
	s, e := New(accounts)
	must("SSO startup and stored-secret scan", e)
	defer s.Close()
	accounts.Sources = s
	login := func(audience string) (identity.Auth, string) {
		t.Helper()
		_, token, _, e := accounts.Login(ctx, "operator@example.invalid", password, audience, "", uuid.NewString())
		must("local login", e)
		a, e := accounts.Authenticate(ctx, token, audience)
		must("local authenticate", e)
		return a, token
	}
	admin, adminToken := login("admin")
	input := CreateConnector{ID: uuid.NewString(), Name: "Native \u4e16\u754c", Provider: ProviderGitea, Origin: origin, ClientID: uuid.NewString(), ClientSecret: "synthetic-secret-" + uuid.NewString(), Enabled: true, Reason: "Synthetic fixture"}
	intent := uuid.NewString()
	connector, e := s.CreateConnector(ctx, admin, input, intent, uuid.NewString())
	must("connector create", e)
	replayed, e := s.CreateConnector(ctx, admin, input, intent, uuid.NewString())
	must("connector intent CLOB", e)
	if replayed != connector || count("SSO_CONNECTORS") != 1 {
		t.Fatal("connector replay changed outcome")
	}
	second := input
	second.ID, second.ClientID, second.Name = uuid.NewString(), uuid.NewString(), "Second \u4e16\u754c"
	_, e = s.CreateConnector(ctx, admin, second, uuid.NewString(), uuid.NewString())
	must("second connector", e)
	page, e := s.ListConnectors(ctx, admin, 1, "")
	must("catalog page", e)
	if len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("catalog continuation missing")
	}
	next, e := s.ListConnectors(ctx, admin, 1, *page.NextCursor)
	must("catalog seek", e)
	if len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("catalog lost or duplicated row")
	}
	public, e := s.PublicConnectors(ctx)
	must("public catalog", e)
	if len(public) != 2 {
		t.Fatal("public catalog mismatch")
	}
	start := func(mode, previous string, actor *identity.Auth) (StartResult, string) {
		t.Helper()
		in := StartInput{Mode: mode}
		reason := "Synthetic local password confirmation"
		if mode == ModeLink {
			in.Password, in.Reason = &password, &reason
		}
		out, e := s.Start(ctx, connector.ID, "portal", previous, actor, in, uuid.NewString())
		must("flow start", e)
		u, e := url.Parse(out.AuthorizationURL)
		must("authorization URL", e)
		state := u.Query().Get("state")
		if !security.ValidToken(state) || !security.ValidToken(out.BrowserToken) || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("redirect_uri") != accounts.Config.Server.PortalOrigin+"/api/v1/auth/sso/"+connector.ID+"/callback" {
			t.Fatal("authorization boundary changed")
		}
		return out, state
	}
	claim := func(out StartResult, state, previous string) claimedFlow {
		t.Helper()
		f, e := s.claim(ctx, connector.ID, "portal", state, out.BrowserToken, previous)
		must("exclusive claim", e)
		if !validClaimedFlow(f) || f.clientSecret == "" || f.verifierText == "" {
			t.Fatal("claim payload absent")
		}
		return f
	}
	flowState := func(id, want string, session bool) {
		t.Helper()
		var state string
		var issued sql.NullString
		var erased bool
		must("independent flow readback", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT STATE,ISSUED_SESSION_ID,CASE WHEN VERIFIER_KEY_ID IS NULL AND VERIFIER_NONCE IS NULL AND VERIFIER_CIPHERTEXT IS NULL THEN 1 ELSE 0 END FROM SSO_FLOWS WHERE ID=?", id)).Scan(&state, &issued, &erased))
		if state != want || issued.Valid != session || !erased {
			t.Fatal("flow authority or verifier erasure mismatch")
		}
	}
	local, localToken := login("portal")
	started, state := start(ModeLink, localToken, &local)
	if out, e := s.claim(ctx, connector.ID, "portal", state, security.Token(), localToken); e == nil || !reflect.DeepEqual(out, claimedFlow{}) {
		t.Fatal("foreign browser claimed flow")
	}
	f := claim(started, state, localToken)
	if out, e := s.claim(ctx, connector.ID, "portal", state, started.BrowserToken, localToken); e == nil || !reflect.DeepEqual(out, claimedFlow{}) {
		t.Fatal("flow claimed twice")
	}
	const subject = "9007199254740993"
	issued, e := s.commitFlow(ctx, f, subject, localToken, uuid.NewString())
	must("link and source session", e)
	flowState(f.id, flowSucceeded, true)
	if _, e := accounts.Authenticate(ctx, localToken, "portal"); e == nil {
		t.Fatal("previous session not rotated")
	}
	actor, e := accounts.Authenticate(ctx, issued.Token, "portal")
	must("source authenticate", e)
	bindings, e := s.ListBindings(ctx, actor)
	must("current source and binding list", e)
	if len(bindings) != 1 || !bindings[0].Active || bindings[0].Subject != subject {
		t.Fatal("binding missing")
	}
	binding := bindings[0]
	if out, e := s.commitFlow(ctx, f, subject, localToken, uuid.NewString()); e == nil || out != (identity.IssuedSession{}) {
		t.Fatal("completed callback issued twice")
	}
	started, state = start(ModeLogin, "", nil)
	f = claim(started, state, "")
	loginSession, e := s.commitFlow(ctx, f, subject, "", uuid.NewString())
	must("bound anonymous login", e)
	_, e = accounts.Authenticate(ctx, loginSession.Token, "portal")
	must("login source", e)
	started, state = start(ModeLogin, "", nil)
	if out, mode, e := s.Complete(ctx, connector.ID, "portal", state, started.BrowserToken, "", "", true, uuid.NewString()); e == nil || e.Error() != "SSO_PROVIDER_DENIED" || mode != ModeLogin || out != (identity.IssuedSession{}) {
		t.Fatal("denied callback escaped failure boundary")
	}

	// Wrapper faults occur after actual native commit or at callback rollback.
	// They do not qualify driver/wire acknowledgement loss.
	before := count("SSO_CONNECTORS")
	rollback := &damengSSOOutcomeStore{Store: store, rollback: true}
	accounts.Storage = rollback
	failed := input
	failed.ID = uuid.NewString()
	out, e := s.CreateConnector(ctx, admin, failed, uuid.NewString(), uuid.NewString())
	accounts.Storage = store
	if e == nil || out != (Connector{}) || rollback.calls != 1 || count("SSO_CONNECTORS") != before {
		t.Fatal("connector transaction did not roll back")
	}
	started, state = start(ModeLogin, "", nil)
	unknown := &damengSSOOutcomeStore{Store: store}
	accounts.Storage = unknown
	claimed, e := s.claim(ctx, connector.ID, "portal", state, started.BrowserToken, "")
	accounts.Storage = store
	if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(claimed, claimedFlow{}) || unknown.calls != 1 {
		t.Fatal("unknown claim leaked secret payload or replayed")
	}
	var unknownID string
	must("unknown claim identity", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT ID FROM SSO_FLOWS WHERE STATE_HASH=?", security.Digest(state))).Scan(&unknownID))
	flowState(unknownID, flowClaimed, false)
	started, state = start(ModeLogin, "", nil)
	f = claim(started, state, "")
	unknown = &damengSSOOutcomeStore{Store: store}
	accounts.Storage = unknown
	uncertain, e := s.commitFlow(ctx, f, subject, "", uuid.NewString())
	accounts.Storage = store
	if e == nil || e.Error() != string(transaction.CommitUnknown) || uncertain != (identity.IssuedSession{}) || unknown.calls != 1 {
		t.Fatal("unknown completion leaked session or replayed")
	}
	flowState(f.id, flowSucceeded, true)

	started, state = start(ModeLogin, "", nil)
	unknown = &damengSSOOutcomeStore{Store: store, pass: 1}
	accounts.Storage = unknown
	failedSession, _, e := s.Complete(ctx, connector.ID, "portal", state, started.BrowserToken, "", "", true, uuid.NewString())
	accounts.Storage = store
	if e == nil || e.Error() != string(transaction.CommitUnknown) || failedSession != (identity.IssuedSession{}) || unknown.calls != 2 {
		t.Fatal("unknown failure commit was hidden or repeated")
	}
	must("unknown denial identity", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT ID FROM SSO_FLOWS WHERE STATE_HASH=?", security.Digest(state))).Scan(&unknownID))
	flowState(unknownID, "failed", false)

	unlinked, e := s.Unlink(ctx, admin, binding.ID, UnlinkBinding{Revision: binding.Revision, Password: password, Reason: "Synthetic unlink"}, uuid.NewString(), uuid.NewString())
	must("unlink", e)
	if unlinked.Active || unlinked.ID != binding.ID || unlinked.Revision != binding.Revision+1 {
		t.Fatal("unlink lifetime changed")
	}
	for _, token := range []string{issued.Token, loginSession.Token} {
		if _, e := accounts.Authenticate(ctx, token, "portal"); e == nil {
			t.Fatal("unlinked source stayed active")
		}
	}
	local, localToken = login("portal")
	started, state = start(ModeLink, localToken, &local)
	f = claim(started, state, localToken)
	linked, e := s.commitFlow(ctx, f, subject, localToken, uuid.NewString())
	must("reactivate retained binding", e)
	bindings, e = s.ListBindings(ctx, admin)
	must("binding lifetime readback", e)
	if len(bindings) != 1 || bindings[0].ID != binding.ID || bindings[0].Revision != binding.Revision+2 || !bindings[0].Active {
		t.Fatal("binding reactivation reset its lifetime")
	}
	started, state = start(ModeLogin, "", nil)
	clientID, secret := uuid.NewString(), "rotated-synthetic-"+uuid.NewString()
	connector, e = s.UpdateConnector(ctx, admin, connector.ID, UpdateConnector{Revision: connector.Revision, Name: "Rotated \u4e16\u754c", Enabled: true, ClientID: &clientID, ClientSecret: &secret, Reason: "Synthetic rotation"}, uuid.NewString(), uuid.NewString())
	must("credential rotation", e)
	if connector.AuthVersion != 2 || connector.Revision != 2 {
		t.Fatal("rotation authority did not advance")
	}
	if _, e := accounts.Authenticate(ctx, linked.Token, "portal"); e == nil {
		t.Fatal("old connector authority survived rotation")
	}
	if out, e := s.claim(ctx, connector.ID, "portal", state, started.BrowserToken, ""); e == nil || !reflect.DeepEqual(out, claimedFlow{}) {
		t.Fatal("old flow survived rotation")
	}
	must("ciphertext startup verification", s.verifyStoredSecrets(ctx))
	damengSSOLockOrderFixture(t, ctx, store, s, admin, connector)
	damengSSOCleanupFixture(t, ctx, store, s, connector)
	started, state = start(ModeLogin, "", nil)
	must("restore invalidation", store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q) }))
	if out, e := s.claim(ctx, connector.ID, "portal", state, started.BrowserToken, ""); e == nil || !reflect.DeepEqual(out, claimedFlow{}) {
		t.Fatal("restore revived old callback")
	}
	accounts.Config.SSO.Enabled = false
	accounts.Config.SSO.KeyringFile = filepath.Join(root, "absent-recovery-keyring")
	disabled, e := New(accounts)
	must("disabled keyless recovery", e)
	defer disabled.Close()
	accounts.Sources = disabled
	_, e = accounts.Authenticate(ctx, adminToken, "admin")
	must("local recovery authority", e)
	if out, e := disabled.PublicConnectors(ctx); e != nil || len(out) != 0 {
		t.Fatal("disabled public catalog exposed provider")
	}
}

type damengSSOOutcomeStore struct {
	transaction.Store
	rollback bool
	calls    int
	pass     int
}

func (s *damengSSOOutcomeStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	e := s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if s.rollback {
			return &transaction.Error{Kind: transaction.CommitRolledBack}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if s.calls <= s.pass {
		return nil
	}
	return errors.Join(context.DeadlineExceeded, &transaction.Error{Kind: transaction.CommitUnknown})
}

// Backdated synthetic pending rows exercise expiry/retention without sleeps or
// mutating immutable lifetime timestamps on an existing flow. DDL remains owned
// by the provisioner, and all schema rows remain as evidence after this test.
func damengSSOCleanupFixture(t *testing.T, ctx context.Context, store transaction.Store, s *Service, connector Connector) {
	t.Helper()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal("native cleanup fixture", e)
		}
	}
	var tenant string
	var now time.Time
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT TENANT_ID FROM INSTALLATION_STATE WHERE ID=1")).Scan(&tenant))
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT CURRENT_TIMESTAMP(6) FROM DUAL")).Scan(&now))
	old := now.Add(-time.Duration(s.policy.FlowRetentionDays+1) * 24 * time.Hour)
	ids := []string{uuid.NewString(), uuid.NewString()}
	for i, id := range ids {
		sealed, e := s.keys.seal(secretScope{tenant, connector.ID, id, flowVerifierPurpose}, []byte(security.Token()))
		must(e)
		must(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			n, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "INSERT INTO SSO_FLOWS(ID,TENANT_ID,CONNECTOR_ID,CONNECTOR_REVISION,AUTH_VERSION,AUDIENCE,MODE,STATE_HASH,BROWSER_HASH,PREVIOUS_COOKIE_HASH,VERIFIER_KEY_ID,VERIFIER_NONCE,VERIFIER_CIPHERTEXT,CREATED_AT,EXPIRES_AT) VALUES(?,?,?,?,?,'portal','login',?,?,?,?,?,?,?,?)", id, tenant, connector.ID, connector.Revision, connector.AuthVersion, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest(security.Binding("")), sealed.keyID, sealed.nonce, sealed.ciphertext, old.Add(-time.Minute), old))
			if e != nil {
				return e
			}
			if n != 1 {
				return fail("unconfirmed fixture")
			}
			if i == 1 {
				n, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE SSO_FLOWS SET STATE='expired',ERROR_CODE='SSO_FLOW_EXPIRED',FINISHED_AT=?,VERIFIER_KEY_ID=NULL,VERIFIER_NONCE=NULL,VERIFIER_CIPHERTEXT=NULL WHERE ID=?", old, id))
				if e != nil {
					return e
				}
				if n != 1 {
					return fail("unconfirmed fixture transition")
				}
			}
			return nil
		}))
	}
	must(s.Cleanup(ctx))
	var state string
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT STATE FROM SSO_FLOWS WHERE ID=?", ids[0])).Scan(&state))
	if state != "expired" {
		t.Fatal("bounded expiry did not transition pending flow")
	}
	var n int
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM SSO_FLOWS WHERE ID=?", ids[1])).Scan(&n))
	if n != 0 {
		t.Fatal("unreferenced retained terminal flow was not deleted")
	}
}
