package sso

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM11ActualConnectorPersistenceIntentCurrentAuthorityAndRevocation(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	secret := security.Token()
	input := CreateConnector{ID: uuid.NewString(), Name: "Second synthetic provider", Provider: "gitea", Origin: "https://provider.example.invalid", ClientID: uuid.NewString(), ClientSecret: secret, Enabled: true, Reason: "Synthetic connector persistence"}
	key := uuid.NewString()
	c, e := f.s.CreateConnector(ctx, f.a, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	replayed, e := f.s.CreateConnector(ctx, f.a, input, key, uuid.NewString())
	if e != nil || replayed.ID != c.ID {
		t.Fatal("create intent replay", e)
	}
	changed := input
	changed.ClientSecret = security.Token()
	_, e = f.s.CreateConnector(ctx, f.a, changed, key, uuid.NewString())
	requireCode(t, e, "IDEMPOTENCY_CONFLICT")
	page, e := f.s.ListConnectors(ctx, f.a, 1, "")
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("bounded inventory", e)
	}
	next, e := f.s.ListConnectors(ctx, f.a, 1, *page.NextCursor)
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.NextCursor != nil {
		t.Fatal("keyset page", e)
	}
	if _, e = f.s.ListConnectors(ctx, f.a, 1, *page.NextCursor+"x"); e == nil {
		t.Fatal("cursor tampering accepted")
	}
	var atRest string
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT row_to_json(c)::text FROM sso_connectors c WHERE id=$1`, c.ID).Scan(&atRest); e != nil {
		t.Fatal(e)
	}
	public, _ := json.Marshal(page)
	if strings.Contains(atRest, secret) || strings.Contains(string(public), "ciphertext") || strings.Contains(string(public), secret) {
		t.Fatal("connector credential escaped encryption boundary")
	}
	var intents string
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT json_agg(row_to_json(i))::text FROM sso_mutation_intents i`).Scan(&intents); e != nil || strings.Contains(intents, secret) {
		t.Fatal("plaintext idempotency secret", e)
	}
	issued := f.issue(t, "")
	start, e := f.s.Start(ctx, f.source.ConnectorID, "admin", "", nil, StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var before Connector
	before, e = scanConnector(f.s.Identity.DB.QueryRow(ctx, `SELECT `+connectorColumns+` FROM sso_connectors WHERE id=$1`, f.source.ConnectorID))
	if e != nil {
		t.Fatal(e)
	}
	update := UpdateConnector{Revision: before.Revision, Name: "Renamed provider", Enabled: true, Reason: "Synthetic name-only revision"}
	after, e := f.s.UpdateConnector(ctx, f.a, before.ID, update, uuid.NewString(), uuid.NewString())
	if e != nil || after.AuthVersion != before.AuthVersion {
		t.Fatal("name-only auth version", e)
	}
	if _, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal("name edit revoked session", e)
	}
	authorization, _ := url.Parse(start.AuthorizationURL)
	_, e = f.s.claim(ctx, before.ID, "admin", authorization.Query().Get("state"), start.BrowserToken, "")
	requireCode(t, e, "SSO_FLOW_UNUSABLE")
	update.Revision, update.Enabled = after.Revision, false
	disabled, e := f.s.UpdateConnector(ctx, f.a, before.ID, update, uuid.NewString(), uuid.NewString())
	if e != nil || disabled.AuthVersion != before.AuthVersion+1 {
		t.Fatal(e)
	}
	_, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	if _, e = f.s.Identity.Authenticate(ctx, f.local, "admin"); e != nil {
		t.Fatal("unrelated local recovery revoked", e)
	}
	var live int
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE sso_connector_id=$1 AND revoked_at IS NULL`, before.ID).Scan(&live); e != nil || live != 0 {
		t.Fatal("bulk source revocation", e)
	}
	publicItems, e := f.s.PublicConnectors(ctx)
	if e != nil || len(publicItems) != 1 || publicItems[0].ID != c.ID {
		t.Fatal("public enabled inventory", e)
	}
}

func startAndClaim(t *testing.T, f sessionFixture, previous string, actor *identity.Auth, mode string) claimedFlow {
	t.Helper()
	input := StartInput{Mode: mode}
	if mode == "link" {
		password, reason := f.password, "Explicit synthetic account binding"
		input.Password, input.Reason = &password, &reason
	}
	result, e := f.s.Start(context.Background(), f.source.ConnectorID, "admin", previous, actor, input, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := url.Parse(result.AuthorizationURL)
	claim, e := f.s.claim(context.Background(), f.source.ConnectorID, "admin", parsed.Query().Get("state"), result.BrowserToken, previous)
	if e != nil {
		t.Fatal(e)
	}
	return claim
}

func TestM11ActualSingleClaimRetainedOwnershipUnlinkAndOrdinarySession(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	claimed := startAndClaim(t, f, f.local, &f.a, "link")
	var retained int
	if e := f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE id=$1 AND state='claimed' AND verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL`, claimed.id).Scan(&retained); e != nil || retained != 1 {
		t.Fatal("claim retained retry authority", e)
	}
	// Explicit provider-output double: the real provider protocol has separate
	// actual Gitea evidence. This exercises the complete real PostgreSQL commit.
	issued, e := f.s.commitFlow(ctx, claimed, "2", f.local, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Identity.Authenticate(ctx, f.local, "admin"); e == nil {
		t.Fatal("prior browser session was not rotated")
	}
	if _, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.commitFlow(ctx, claimed, "2", f.local, uuid.NewString()); e == nil {
		t.Fatal("claimed flow committed twice")
	}
	_, local, _, e := f.s.Identity.Login(ctx, "admin@example.invalid", f.password, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.s.Identity.Authenticate(ctx, local, "admin")
	if e != nil {
		t.Fatal(e)
	}
	bindings, e := f.s.ListBindings(ctx, a)
	if e != nil || len(bindings) != 2 {
		t.Fatal("current binding inventory", e)
	}
	var selected Binding
	for _, binding := range bindings {
		if binding.Subject == "2" {
			selected = binding
		}
	}
	key := uuid.NewString()
	unlink := UnlinkBinding{Revision: selected.Revision, Password: f.password, Reason: "Synthetic explicit unlink"}
	result, e := f.s.Unlink(ctx, a, selected.ID, unlink, key, uuid.NewString())
	if e != nil || result.Active || result.Revision != selected.Revision+1 {
		t.Fatal("unlink", e)
	}
	_, e = f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	if _, e = f.s.Identity.Authenticate(ctx, local, "admin"); e != nil {
		t.Fatal("local session revoked by unlink", e)
	}
	retry, e := f.s.Unlink(ctx, a, selected.ID, unlink, key, uuid.NewString())
	if e != nil || retry.ID != selected.ID || retry.Revision != result.Revision {
		t.Fatal("unlink exact replay", e)
	}
	login := startAndClaim(t, f, "", nil, "login")
	_, e = f.s.commitFlow(ctx, login, "2", "", uuid.NewString())
	requireCode(t, e, "SSO_BINDING_REQUIRED")
	if e = f.s.failFlow(ctx, login, e.Error(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	relink := startAndClaim(t, f, local, &a, "link")
	relinked, e := f.s.commitFlow(ctx, relink, "2", local, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Identity.Authenticate(ctx, relinked.Token, "admin"); e != nil {
		t.Fatal(e)
	}
	var id string
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT id::text FROM sso_bindings WHERE connector_id=$1 AND subject='2'`, f.source.ConnectorID).Scan(&id); e != nil || id != selected.ID {
		t.Fatal("retained identity was replaced", e)
	}
}

func TestM11ActualBrowserAndCredentialChangesBeforeClaimAndConcurrentClaim(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	password, reason := f.password, "Synthetic link freshness"
	start, e := f.s.Start(ctx, f.source.ConnectorID, "admin", f.local, &f.a, StartInput{Mode: "link", Password: &password, Reason: &reason}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	authorization, _ := url.Parse(start.AuthorizationURL)
	state := authorization.Query().Get("state")
	_, e = f.s.claim(ctx, f.source.ConnectorID, "admin", state, security.Token(), f.local)
	requireCode(t, e, "SSO_BROWSER_CHANGED")
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE password_credentials SET version=version+1 WHERE user_id=$1`, f.a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.claim(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local)
	requireCode(t, e, "SSO_PASSWORD_CHANGED")
	login, e := f.s.Start(ctx, f.source.ConnectorID, "admin", f.local, nil, StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	loginURL, _ := url.Parse(login.AuthorizationURL)
	if e = f.s.Identity.Logout(ctx, f.local, "admin", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.claim(ctx, f.source.ConnectorID, "admin", loginURL.Query().Get("state"), login.BrowserToken, f.local)
	requireCode(t, e, "SSO_BROWSER_CHANGED")
	anonymous, e := f.s.Start(ctx, f.source.ConnectorID, "admin", "", nil, StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := url.Parse(anonymous.AuthorizationURL)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.s.claim(ctx, f.source.ConnectorID, "admin", parsed.Query().Get("state"), anonymous.BrowserToken, "")
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else {
			requireCode(t, e, "SSO_FLOW_UNUSABLE")
		}
	}
	if wins != 1 {
		t.Fatal("concurrent claim did not establish exactly one owner")
	}
}

func TestM11ActualAuditFailureRollsBackBindingSessionRotationAndFinalFlow(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	claimed := startAndClaim(t, f, f.local, &f.a, "link")
	if _, e := f.s.Identity.DB.Exec(ctx, `CREATE FUNCTION reject_sso_success() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='sso.binding_linked' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER reject_sso_success BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_sso_success()`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.commitFlow(ctx, claimed, "2", f.local, uuid.NewString()); e == nil {
		t.Fatal("audit rejection reported success")
	}
	if _, e := f.s.Identity.Authenticate(ctx, f.local, "admin"); e != nil {
		t.Fatal("rotation escaped rollback", e)
	}
	var count int
	if e := f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_bindings WHERE subject='2'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("binding escaped rollback", e)
	}
	if e := f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE id=$1 AND state='claimed' AND authenticated_user_id IS NULL`, claimed.id).Scan(&count); e != nil || count != 1 {
		t.Fatal("flow success escaped rollback", e)
	}
	if e := f.s.failFlow(ctx, claimed, "STORAGE_UNAVAILABLE", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e := f.s.transaction(ctx, func(tx pgx.Tx) error { return InvalidateAuthorities(ctx, tx) }); e != nil {
		t.Fatal(e)
	}
	t.Log("Provider output is an explicit test double; persistence, source checks, password verification, claims, rollback and audit use actual PostgreSQL and identity implementation.")
}

func TestM11ActualConnectorMutationRejectsStaleActorAndForeignBinding(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	_, e := f.s.Unlink(ctx, f.a, uuid.NewString(), UnlinkBinding{Revision: 1, Password: f.password, Reason: "Synthetic foreign scope"}, uuid.NewString(), uuid.NewString())
	requireCode(t, e, "SSO_BINDING_NOT_FOUND")
	_, e = f.s.Identity.ConfirmPassword(ctx, f.a, fixture.Password())
	requireCode(t, e, "SSO_PASSWORD_REQUIRED")
	if e = f.s.Identity.Logout(ctx, f.local, "admin", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.UpdateConnector(ctx, f.a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Stale actor", Enabled: false, Reason: "Synthetic stale actor"}, uuid.NewString(), uuid.NewString())
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
}

// Logout owns the user fence before the claim. The session blocker is test-only;
// production still uses user -> connector -> live-session ordering.
func TestM11ActualLogoutWhileClaimWaitsIsRejectedBeforeExchange(t *testing.T) {
	f := sourceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start, e := f.s.Start(ctx, f.source.ConnectorID, "admin", f.local, nil, StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := url.Parse(start.AuthorizationURL)
	state := parsed.Query().Get("state")
	locker, e := f.s.Identity.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer locker.Rollback(context.Background())
	var blockingPID int
	if e = locker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blockingPID); e != nil {
		t.Fatal(e)
	}
	if _, e = locker.Exec(ctx, "SELECT id FROM sessions WHERE id=$1 FOR UPDATE", f.a.SessionID); e != nil {
		t.Fatal(e)
	}
	logout := make(chan error, 1)
	go func() { logout <- f.s.Identity.Logout(ctx, f.local, "admin", uuid.NewString()) }()
	var logoutPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e = f.s.Identity.DB.QueryRow(ctx, "SELECT COALESCE((SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1),0)", blockingPID).Scan(&logoutPID); e != nil {
			t.Fatal(e)
		}
		if logoutPID != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if logoutPID == 0 {
		t.Fatal("actual logout did not hold its user fence before waiting for session")
	}
	done := make(chan error, 1)
	go func() {
		_, e := f.s.claim(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local)
		done <- e
	}()
	blocked := false
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e = f.s.Identity.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", logoutPID).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("claim did not wait behind the earlier logout user fence")
	}
	if e = locker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-logout; e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		requireCode(t, e, "SSO_BROWSER_CHANGED")
	case <-ctx.Done():
		t.Fatal("claim did not reject after logout")
	}
	var pending bool
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT state='pending' AND verifier_ciphertext IS NOT NULL FROM sso_flows WHERE state_hash=$1`, security.Digest(state)).Scan(&pending); e != nil || !pending {
		t.Fatal("rejected claim consumed external-exchange authority", e)
	}
}

func TestM11ActualClaimWinningUserFenceStillRejectsCompletionAfterLogout(t *testing.T) {
	f := sourceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start, e := f.s.Start(ctx, f.source.ConnectorID, "admin", f.local, nil, StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := url.Parse(start.AuthorizationURL)
	locker, e := f.s.Identity.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer locker.Rollback(context.Background())
	var blocker int
	if e = locker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blocker); e != nil {
		t.Fatal(e)
	}
	if _, e = locker.Exec(ctx, "SELECT id FROM sso_connectors WHERE id=$1 FOR UPDATE", f.source.ConnectorID); e != nil {
		t.Fatal(e)
	}
	type outcome struct {
		flow claimedFlow
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		v, e := f.s.claim(ctx, f.source.ConnectorID, "admin", parsed.Query().Get("state"), start.BrowserToken, f.local)
		done <- outcome{v, e}
	}()
	var claimPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e = f.s.Identity.DB.QueryRow(ctx, "SELECT COALESCE((SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1),0)", blocker).Scan(&claimPID); e != nil {
			t.Fatal(e)
		}
		if claimPID != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if claimPID == 0 {
		t.Fatal("claim did not reach connector after its user fence")
	}
	logout := make(chan error, 1)
	go func() { logout <- f.s.Identity.Logout(ctx, f.local, "admin", uuid.NewString()) }()
	blocked := false
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e = f.s.Identity.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", claimPID).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("logout bypassed the earlier claim user fence")
	}
	if e = locker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	result := <-done
	if result.err != nil || !validClaimedFlow(result.flow) {
		t.Fatal("claim failed before external IO", result.err)
	}
	if e = <-logout; e != nil {
		t.Fatal(e)
	}
	issued, e := f.s.commitFlow(ctx, result.flow, "9007199254740993", f.local, uuid.NewString())
	if e == nil || issued.Token != "" {
		t.Fatal("logout allowed late issuance", e)
	}
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	var used bool
	if e = f.s.Identity.DB.QueryRow(ctx, "SELECT issued_session_id IS NOT NULL FROM sso_flows WHERE id=$1", result.flow.id).Scan(&used); e != nil || used {
		t.Fatal("failed completion issued session", e)
	}
	// A claimed intent cannot regain code exchange authority after logout/restart.
	_, e = f.s.claim(ctx, f.source.ConnectorID, "admin", parsed.Query().Get("state"), start.BrowserToken, f.local)
	if e == nil {
		t.Fatal("claimed intent became retryable")
	}
}

func TestM11ActualForeignSubjectOwnershipSurvivesUnlink(t *testing.T) {
	f := sourceFixture(t)
	other := secondAdmin(t, f)
	ctx := context.Background()
	claimed := startAndClaim(t, f, f.local, &f.a, "link")
	if _, e := f.s.commitFlow(ctx, claimed, "2", f.local, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for index := 0; index < 2; index++ {
		attempt := startAndClaim(t, other, other.local, &other.a, "link")
		_, e := f.s.commitFlow(ctx, attempt, "2", other.local, uuid.NewString())
		requireCode(t, e, "SSO_BINDING_CONFLICT")
		if e = f.s.failFlow(ctx, attempt, e.Error(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if index == 0 {
			owner := fixture.Login(t, f.s.Identity, "admin@example.invalid", f.password, "admin")
			bindings, e := f.s.ListBindings(ctx, owner)
			if e != nil {
				t.Fatal(e)
			}
			for _, b := range bindings {
				if b.Subject == "2" {
					if _, e = f.s.Unlink(ctx, owner, b.ID, UnlinkBinding{Revision: b.Revision, Password: f.password, Reason: "Retain immutable ownership after unlink"}, uuid.NewString(), uuid.NewString()); e != nil {
						t.Fatal(e)
					}
				}
			}
		}
	}
	var user string
	if e := f.s.Identity.DB.QueryRow(ctx, `SELECT user_id::text FROM sso_bindings WHERE connector_id=$1 AND subject='2' AND NOT active`, f.source.ConnectorID).Scan(&user); e != nil || user != f.a.Me.User.ID {
		t.Fatal("foreign user adopted retained subject", e)
	}
}
