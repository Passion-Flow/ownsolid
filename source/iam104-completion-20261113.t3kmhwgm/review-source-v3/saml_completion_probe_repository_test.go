//go:build repository_integration

package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/beevik/etree"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type samlCompletionFixture struct {
	f                  sourceRepositoryFixture
	owner              samlClaimProbe
	claim              samlProbeClaim
	staged             samlStagedProbe
	previous, password string
}

func samlCompletionFixtureFor(t *testing.T, shortSession bool) samlCompletionFixture {
	return samlCompletionFixtureWithResultTTL(t, shortSession, 0)
}
func samlCompletionFixtureWithResultTTL(t *testing.T, shortSession bool, resultTTL time.Duration) samlCompletionFixture {
	return samlCompletionFixtureOptions(t, shortSession, resultTTL, false)
}
func samlCompletionFixtureOptions(t *testing.T, shortSession bool, resultTTL time.Duration, shortAssertion bool) samlCompletionFixture {
	t.Helper()
	f, password, p, crypto := samlStageFixture(t)
	if resultTTL > 0 {
		p.ttl = resultTTL
	}
	start, previous, target := samlProbeStarted(t, f, password, p, "portal", false)
	claim, e := p.claim(f.ctx, target, "portal", start.relay, start.browser, uuid.NewString())
	if e != nil {
		t.Fatal("actual SAML request claim", e)
	}
	proof := samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), func(a *etree.Element) {
		if shortSession {
			a.SelectElement("AuthnStatement").CreateAttr("SessionNotOnOrAfter", crypto.now.Add(5*time.Second).Format(time.RFC3339))
		}
		if shortAssertion {
			expiry := crypto.now.Add(5 * time.Second).Format(time.RFC3339)
			a.SelectElement("Conditions").CreateAttr("NotOnOrAfter", expiry)
			a.FindElement("./Subject/SubjectConfirmation/SubjectConfirmationData").CreateAttr("NotOnOrAfter", expiry)
		}
	})
	staged, e := p.stage(f.ctx, claim, proof, uuid.NewString())
	if e != nil {
		t.Fatal("actual signed result staged", e)
	}
	return samlCompletionFixture{f: f, owner: p, claim: claim, staged: staged, previous: previous, password: password}
}
func samlCompletionEnableOutbox(t *testing.T, c samlCompletionFixture) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(map[string]any{"active": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "mail-keyring.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	keys, e := mail.LoadKeyring(path)
	if e != nil {
		t.Fatal(e)
	}
	templates, e := mail.LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	c.f.accounts.Config.SMTP.FromAddress = "platform@example.invalid"
	c.f.accounts.Mail = &mail.Outbox{Storage: c.f.f.Store, Config: c.f.accounts.Config, Keys: keys, Templates: templates}
}
func samlCompletionRotatePassword(t *testing.T, c samlCompletionFixture, token string) {
	t.Helper()
	samlCompletionEnableOutbox(t, c)
	actor, e := c.f.accounts.Authenticate(c.f.ctx, token, "portal")
	if e != nil {
		t.Fatal(e)
	}
	next := "Explicit private replacement " + security.Token()
	issued, e := c.f.accounts.ChangePassword(c.f.ctx, actor, c.password, next, uuid.NewString())
	if e != nil || issued.Token == "" {
		t.Fatal("actual password owner rotation", e)
	}
	if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e != nil {
		t.Fatal("actual replacement local session", e)
	}
}
func (c samlCompletionFixture) finish(ctx context.Context) (identity.IssuedSession, error) {
	return c.owner.complete(ctx, c.claim.connector, "portal", c.claim.id, c.staged.continuation, c.previous, uuid.NewString())
}
func (c samlCompletionFixture) counts(t *testing.T) (string, [3]int) {
	t.Helper()
	var state string
	var counts [3]int
	if e := c.f.f.PostgreSQL.QueryRow(c.f.ctx, `SELECT state FROM sso_saml_requests WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.claim.id).Scan(&state); e != nil {
		t.Fatal(e)
	}
	for i, query := range []string{`SELECT count(*) FROM sessions WHERE sso_tenant_id=$1 AND sso_flow_id=$2`, `SELECT count(*) FROM sso_saml_results WHERE tenant_id=$1 AND flow_id=$2 AND completed_binding_version IS NOT NULL`, `SELECT count(*) FROM sso_saml_identities WHERE tenant_id=$1 AND id=(SELECT identity_id FROM sso_saml_results WHERE tenant_id=$1 AND flow_id=$2) AND active`} {
		if e := c.f.f.PostgreSQL.QueryRow(c.f.ctx, query, c.f.f.Tenant, c.claim.id).Scan(&counts[i]); e != nil {
			t.Fatal(e)
		}
	}
	return state, counts
}
func TestSAMLCompletionProbeActualSourceSession(t *testing.T) {
	c := samlCompletionFixtureFor(t, false)
	var manualToken string
	// The fixture creates an independent retained manual session through Identity.
	if e := c.f.f.Store.Within(c.f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		issued, e := c.f.accounts.IssueSessionWithin(ctx, q, c.f.f.User, 1, "portal", "", uuid.NewString(), nil)
		manualToken = issued.Token
		return e
	}); e != nil {
		t.Fatal(e)
	}
	var legacy identity.IssuedSession
	if e := c.f.f.Store.Within(c.f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		legacy, e = c.f.accounts.IssueSessionWithin(ctx, q, c.f.f.User, 1, "admin", "", uuid.NewString(), &c.f.source)
		return e
	}); e != nil {
		t.Fatal("retained actual Gitea source", e)
	}
	issued, e := c.finish(c.f.ctx)
	if e != nil || issued.Token == "" || issued.Me.User.ID != c.f.f.User {
		t.Fatal("actual source-bound SAML session completion", e)
	}
	actor, e := c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal")
	if e != nil || actor.SessionID != issued.SessionID {
		t.Fatal("actual SAML session authentication", e)
	}
	state, counts := c.counts(t)
	if state != "succeeded" || counts != ([3]int{1, 1, 1}) {
		t.Fatal("binding/result/session did not share actual completion")
	}
	var deadline time.Time
	var stored identity.SessionSource
	if e = c.f.f.PostgreSQL.QueryRow(c.f.ctx, `SELECT se.sso_tenant_id::text,se.sso_connector_id::text,se.sso_binding_id::text,se.sso_flow_id::text,se.sso_auth_version,r.source_until FROM sessions se JOIN sso_saml_results r ON r.tenant_id=se.sso_tenant_id AND r.flow_id=se.sso_flow_id WHERE se.id=$1`, issued.SessionID).Scan(&stored.TenantID, &stored.ConnectorID, &stored.BindingID, &stored.FlowID, &stored.AuthVersion, &deadline); e != nil || stored.FlowID != c.claim.id || stored.BindingID != c.staged.identity || issued.ExpiresAt.After(deadline) {
		t.Fatal("actual session lost its source or deadline", e)
	}
	if _, e = c.f.accounts.Authenticate(c.f.ctx, c.previous, "portal"); e == nil {
		t.Fatal("initiating session was not rotated")
	}
	for _, item := range []struct{ token, audience string }{{manualToken, "portal"}, {legacy.Token, "admin"}} {
		if _, e = c.f.accounts.Authenticate(c.f.ctx, item.token, item.audience); e != nil {
			t.Fatal("unrelated local or Gitea source was changed", e)
		}
	}
	if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "admin"); e == nil {
		t.Fatal("SAML session crossed entrances")
	}
	c.f.accounts.Sources = c.f.s
	if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e == nil {
		t.Fatal("missing SAML owner treated its sourced session as local")
	}
	sourceOwner, e := NewSAMLCompletion(c.f.ctx, c.f.s)
	if e != nil {
		t.Fatal(e)
	}
	c.f.accounts.Sources = sourceOwner
	retry, e := c.owner.complete(c.f.ctx, c.claim.connector, "portal", c.claim.id, c.staged.continuation, issued.Token, uuid.NewString())
	if e == nil || retry != (identity.IssuedSession{}) {
		t.Fatal("replay reconstructed or issued a second token")
	}
	if state, counts = c.counts(t); state != "succeeded" || counts != ([3]int{1, 1, 1}) {
		t.Fatal("replay changed source authority")
	}
}
func TestSAMLCompletionProbeCurrentAuthorityAndScope(t *testing.T) {
	for _, kind := range []string{"cookie_changed", "same_user_other_session", "logout", "credential_rotation", "account_disabled", "profile_revision", "connector_disabled", "expired_result", "wrong_continuation", "wrong_audience", "foreign_connector", "binding_conflict"} {
		t.Run(kind, func(t *testing.T) {
			var c samlCompletionFixture
			if kind == "expired_result" {
				c = samlCompletionFixtureWithResultTTL(t, false, 3*time.Second)
			} else {
				c = samlCompletionFixtureFor(t, false)
			}
			connector, audience, continuation := c.claim.connector, "portal", c.staged.continuation
			switch kind {
			case "cookie_changed":
				c.previous = security.Token()
			case "same_user_other_session":
				if e := c.f.f.Store.Within(c.f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					issued, e := c.f.accounts.IssueSessionWithin(ctx, q, c.f.f.User, 1, "portal", "", uuid.NewString(), nil)
					c.previous = issued.Token
					return e
				}); e != nil {
					t.Fatal(e)
				}
			case "logout":
				if e := c.f.accounts.Logout(c.f.ctx, c.previous, "portal", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "credential_rotation":
				samlCompletionRotatePassword(t, c, c.previous)
			case "account_disabled":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, c.f.f.User))
			case "profile_revision":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_profiles SET revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, c.f.f.Tenant, connector))
			case "connector_disabled":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, connector))
			case "expired_result":
				if _, e := c.f.f.PostgreSQL.Exec(c.f.ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM $1::timestamptz-clock_timestamp())))`, c.staged.expires.Add(20*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
			case "wrong_continuation":
				continuation = security.Token()
			case "wrong_audience":
				audience = "admin"
			case "foreign_connector":
				tenant := uuid.NewString()
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign completion tenant','active')`, tenant))
				connector = samlProbeSeedConnector(t, c.f, tenant, "https://keycloak.iam104.test/realms/iam104")
			case "binding_conflict":
				user := bindingOtherUser(t, c.f)
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_identities SET bound_user_id=$1,active=true,binding_version=binding_version+1 WHERE tenant_id=$2 AND id=$3`, user, c.f.f.Tenant, c.staged.identity))
			}
			out, e := c.owner.complete(c.f.ctx, connector, audience, c.claim.id, continuation, c.previous, uuid.NewString())
			if e == nil || out != (identity.IssuedSession{}) {
				t.Fatal("changed authority released actual session", e)
			}
			state, counts := c.counts(t)
			expected := [3]int{}
			if kind == "binding_conflict" {
				expected[2] = 1
			}
			if state != "verified" || counts != expected {
				t.Fatal("completion refusal partially persisted authority")
			}
		})
	}
}
func TestSAMLCompletionProbeSourceExpiryAndRevocation(t *testing.T) {
	for _, kind := range []string{"signed_session_end", "profile_disable", "connector_disable", "binding_revoke", "password_rotation", "assertion_window_elapsed"} {
		t.Run(kind, func(t *testing.T) {
			var c samlCompletionFixture
			if kind == "assertion_window_elapsed" {
				c = samlCompletionFixtureOptions(t, false, 0, true)
			} else {
				c = samlCompletionFixtureFor(t, kind == "signed_session_end")
			}
			issued, e := c.finish(c.f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "assertion_window_elapsed":
				if _, e = c.f.f.PostgreSQL.Exec(c.f.ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM $1::timestamptz-clock_timestamp())))`, c.staged.expires.Add(20*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
				if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e != nil {
					t.Fatal("accepted local session incorrectly used the assertion acceptance deadline", e)
				}
				return
			case "signed_session_end":
				if _, e = c.f.f.PostgreSQL.Exec(c.f.ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM $1::timestamptz-clock_timestamp())))`, issued.ExpiresAt.Add(20*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
			case "profile_disable":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_profiles SET enabled=false,revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, c.f.f.Tenant, c.claim.connector))
			case "connector_disable":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.claim.connector))
			case "password_rotation":
				samlCompletionRotatePassword(t, c, issued.Token)
			case "binding_revoke":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_identities SET active=false,binding_version=binding_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.staged.identity))
			}
			if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e == nil {
				t.Fatal("actual sourced session outlived revoked or expired authority")
			}
			if kind == "binding_revoke" {
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_identities SET active=true,binding_version=binding_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.staged.identity))
				if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e == nil {
					t.Fatal("reactivating binding revived its previous source session")
				}
			}
		})
	}
}
func TestSAMLCompletionProbeConcurrentAndAudit(t *testing.T) {
	for _, kind := range []string{"two_finishers", "audit_rollback", "result_expires_while_waiting"} {
		t.Run(kind, func(t *testing.T) {
			var c samlCompletionFixture
			if kind == "result_expires_while_waiting" {
				c = samlCompletionFixtureWithResultTTL(t, false, 3*time.Second)
			} else {
				c = samlCompletionFixtureFor(t, false)
			}
			if kind == "result_expires_while_waiting" {
				observer := samlProbeConnection(t, c.f)
				holder, e := samlProbeConnection(t, c.f).Begin(c.f.ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer holder.Rollback(context.Background())
				var id string
				var pid int
				if e = holder.QueryRow(c.f.ctx, `SELECT id::text FROM sso_saml_identities WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, c.f.f.Tenant, c.staged.identity).Scan(&id); e != nil {
					t.Fatal(e)
				}
				if e = holder.QueryRow(c.f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
					t.Fatal(e)
				}
				done := make(chan error, 1)
				ctx, cancel := context.WithTimeout(c.f.ctx, 8*time.Second)
				defer cancel()
				go func() {
					out, e := c.finish(ctx)
					if e == nil || out != (identity.IssuedSession{}) {
						done <- fail("PRIVATE_COMPLETION_EXPIRY_NOT_ENFORCED")
						return
					}
					done <- nil
				}()
				observed := false
				until := time.Now().Add(2 * time.Second)
				for time.Now().Before(until) {
					var count int
					if e = observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, pid).Scan(&count); e != nil {
						t.Fatal(e)
					}
					if count > 0 {
						observed = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !observed {
					t.Fatal("completion never waited on actual binding authority")
				}
				if _, e = observer.Exec(ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM $1::timestamptz-clock_timestamp())))`, c.staged.expires.Add(20*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
				if e = holder.Commit(ctx); e != nil {
					t.Fatal(e)
				}
				select {
				case e = <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-ctx.Done():
					t.Fatal("expired completion did not drain")
				}
				state, counts := c.counts(t)
				if state != "verified" || counts != ([3]int{}) {
					t.Fatal("late completion persisted binding or session")
				}
				return
			}
			if kind == "audit_rollback" {
				if _, e := c.f.f.PostgreSQL.Exec(c.f.ctx, `CREATE FUNCTION saml_completion_audit_refusal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='auth.sso_login_succeeded' THEN RAISE EXCEPTION 'private completion audit fault'; END IF; RETURN NEW; END; $$; CREATE TRIGGER saml_completion_audit_refusal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION saml_completion_audit_refusal()`); e != nil {
					t.Fatal(e)
				}
				out, e := c.finish(c.f.ctx)
				if e == nil || out != (identity.IssuedSession{}) {
					t.Fatal("post-insertion audit failure released a session")
				}
				state, counts := c.counts(t)
				if state != "verified" || counts != ([3]int{}) {
					t.Fatal("audit failure split binding/session/source result")
				}
				if _, e = c.f.f.PostgreSQL.Exec(c.f.ctx, `DROP TRIGGER saml_completion_audit_refusal ON audit_events`); e != nil {
					t.Fatal(e)
				}
				if _, e = c.finish(c.f.ctx); e != nil {
					t.Fatal("explicit completion retry after audit repair", e)
				}
				return
			}
			type result struct {
				out identity.IssuedSession
				e   error
			}
			done := make(chan result, 2)
			begin := make(chan struct{})
			for range 2 {
				go func() { <-begin; out, e := c.finish(c.f.ctx); done <- result{out, e} }()
			}
			close(begin)
			wins, refused := 0, 0
			for range 2 {
				select {
				case r := <-done:
					if r.e == nil && r.out.Token != "" {
						wins++
					} else if r.e != nil && r.out == (identity.IssuedSession{}) {
						refused++
					} else {
						t.Fatal("invalid actual finish result")
					}
				case <-time.After(8 * time.Second):
					t.Fatal("concurrent finish did not drain")
				}
			}
			state, counts := c.counts(t)
			if wins != 1 || refused != 1 || state != "succeeded" || counts != ([3]int{1, 1, 1}) {
				t.Fatal("concurrent finish issued multiple sessions")
			}
		})
	}
}
func TestSAMLCompletionProbeCommitLoss(t *testing.T) {
	for _, mode := range []commitfault.Mode{commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
		name := "before_commit"
		committed := false
		if mode == commitfault.DropCommitAcknowledgement {
			name = "after_server_commit"
			committed = true
		}
		t.Run(name, func(t *testing.T) {
			c := samlCompletionFixtureFor(t, false)
			store, proxy := connectorFaultStore(t, c.f)
			originalStore, originalDB := c.f.accounts.Storage, c.f.accounts.DB
			defer func() { c.f.accounts.Storage, c.f.accounts.DB = originalStore, originalDB }()
			c.f.accounts.Storage, c.f.accounts.DB = store, nil
			if e := proxy.Arm(mode); e != nil {
				t.Fatal(e)
			}
			out, e := c.finish(c.f.ctx)
			c.f.accounts.Storage, c.f.accounts.DB = originalStore, originalDB
			if e == nil || e.Error() != string(transaction.CommitUnknown) || out != (identity.IssuedSession{}) {
				t.Fatal("uncertain completion released a token", e)
			}
			select {
			case seen := <-proxy.Outcomes():
				if seen.Mode != mode || !seen.Suppressed || seen.Forwarded != committed || seen.ServerOK != committed {
					t.Fatal("wrong completion wire observation")
				}
			case <-c.f.ctx.Done():
				t.Fatal("completion wire observation absent")
			}
			if proxy.Commits() != 1 {
				t.Fatal("completion automatically resent after unknown COMMIT")
			}
			state, counts := c.counts(t)
			if committed && (state != "succeeded" || counts != ([3]int{1, 1, 1})) || !committed && (state != "verified" || counts != ([3]int{})) {
				t.Fatal("actual completion atomic readback failed")
			}
		})
	}
}
func TestSAMLCompletionProbeSourceParentDeletionRace(t *testing.T) {
	for _, kind := range []string{"saml", "legacy_gitea"} {
		t.Run(kind, func(t *testing.T) {
			c := samlCompletionFixtureFor(t, false)
			observer := samlProbeConnection(t, c.f)
			holder := samlProbeConnection(t, c.f)
			barrier, e := holder.Begin(c.f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer barrier.Rollback(context.Background())
			const key int64 = 8421104
			if _, e = barrier.Exec(c.f.ctx, `SELECT pg_advisory_xact_lock($1)`, key); e != nil {
				t.Fatal(e)
			}
			var blocker int
			if e = barrier.QueryRow(c.f.ctx, `SELECT pg_backend_pid()`).Scan(&blocker); e != nil {
				t.Fatal(e)
			}
			if _, e = c.f.f.PostgreSQL.Exec(c.f.ctx, `CREATE FUNCTION saml_completion_audit_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='auth.sso_login_succeeded' THEN PERFORM pg_advisory_xact_lock(8421104); END IF; RETURN NEW; END; $$; CREATE TRIGGER saml_completion_audit_barrier BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION saml_completion_audit_barrier()`); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(c.f.ctx, 10*time.Second)
			defer cancel()
			issued := make(chan error, 1)
			deleted := make(chan error, 1)
			go func() {
				if kind == "saml" {
					_, e := c.finish(ctx)
					issued <- e
					return
				}
				issued <- c.f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
					_, e := c.f.accounts.IssueSessionWithin(ctx, q, c.f.f.User, 1, "admin", "", uuid.NewString(), &c.f.source)
					return e
				})
			}()
			wait := func(pid int) int {
				t.Helper()
				until := time.Now().Add(4 * time.Second)
				for time.Now().Before(until) {
					var waiting int
					e := observer.QueryRow(ctx, `SELECT COALESCE(min(pid),0) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, pid).Scan(&waiting)
					if e != nil {
						t.Fatal(e)
					}
					if waiting != 0 {
						return waiting
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("actual PostgreSQL source-parent wait was not observed")
				return 0
			}
			writer := wait(blocker)
			table, id := "sso_saml_requests", c.claim.id
			if kind == "legacy_gitea" {
				table, id = "sso_flows", c.f.source.FlowID
			}
			deleter := samlProbeConnection(t, c.f)
			go func() {
				_, e := deleter.Exec(ctx, `DELETE FROM `+table+` WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, id)
				deleted <- e
			}()
			wait(writer)
			if e = barrier.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			select {
			case e = <-issued:
				if e != nil {
					t.Fatal("actual source session issuance", e)
				}
			case <-ctx.Done():
				t.Fatal("issuer did not drain")
			}
			select {
			case e = <-deleted:
				var pg *pgconn.PgError
				if !errors.As(e, &pg) || pg.Code != "23503" {
					t.Fatal("parent deletion orphaned a concurrently committed source session", e)
				}
			case <-ctx.Done():
				t.Fatal("source deleter did not drain")
			}
		})
	}
}

func TestSAMLCompletionProbeTimestampCodec(t *testing.T) {
	c := samlCompletionFixtureFor(t, false)
	configuration := c.f.f.PostgreSQL.Config().Copy()
	previousHook := configuration.AfterConnect
	configuration.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if previousHook != nil {
			if e := previousHook(ctx, conn); e != nil {
				return e
			}
		}
		conn.TypeMap().RegisterType(&pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: time.FixedZone("private-non-UTC-codec", 9*3600)}})
		return nil
	}
	pool, e := pgxpool.NewWithConfig(c.f.ctx, configuration)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	oldDB, oldStorage := c.f.accounts.DB, c.f.accounts.Storage
	defer func() { c.f.accounts.DB, c.f.accounts.Storage = oldDB, oldStorage }()
	c.f.accounts.DB, c.f.accounts.Storage = pool, nil
	issued, e := c.finish(c.f.ctx)
	if e != nil {
		t.Fatal("actual non-UTC PostgreSQL codec source completion", e)
	}
	_, offset := issued.ExpiresAt.Zone()
	if offset != 0 || issued.ExpiresAt.Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatal("source deadline postimage lost its canonical UTC domain")
	}
	if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e != nil {
		t.Fatal("source session rejected after authoritative timestamp normalization", e)
	}
}

func TestSAMLCompletionProbeProtocolIsolation(t *testing.T) {
	c := samlCompletionFixtureFor(t, false)
	admin := catalogAuth(t, c.f, c.f.f.User, "admin", nil)
	listed, e := c.f.s.ListConnectors(c.f.ctx, admin, 10, "")
	if e != nil {
		t.Fatal("legacy catalog read with actual SAML records", e)
	}
	for _, item := range listed.Items {
		if item.Provider != ProviderGitea || item.ID == c.claim.connector {
			t.Fatal("Gitea contract exposed another protocol")
		}
	}
	public, e := c.f.s.PublicConnectors(c.f.ctx)
	if e != nil {
		t.Fatal("legacy public providers with actual SAML records", e)
	}
	for _, item := range public {
		if item.Provider != ProviderGitea || item.ID == c.claim.connector {
			t.Fatal("Gitea discovery exposed another protocol")
		}
	}
	if _, e = c.f.s.UpdateConnector(c.f.ctx, admin, c.claim.connector, UpdateConnector{Revision: 1, Name: "Wrong protocol update", Enabled: false, Reason: "Explicit negative protocol-boundary case"}, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("Gitea adapter modified SAML authority")
	}
	restarted, e := New(c.f.accounts)
	if e != nil {
		t.Fatal("legacy secret inventory interpreted SAML as OAuth credentials", e)
	}
	t.Cleanup(restarted.Close)
	issued, e := c.finish(c.f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	var local, legacy identity.IssuedSession
	for _, source := range []*identity.SessionSource{nil, &c.f.source} {
		var out identity.IssuedSession
		e = c.f.f.Store.Within(c.f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			out, e = c.f.accounts.IssueSessionWithin(ctx, q, c.f.f.User, 1, "admin", "", uuid.NewString(), source)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		if source == nil {
			local = out
		} else {
			legacy = out
		}
	}
	if _, e = c.f.f.PostgreSQL.Exec(c.f.ctx, `ALTER TABLE sso_saml_requests RENAME TO sso_saml_requests_unavailable`); e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, e := c.f.f.PostgreSQL.Exec(ctx, `ALTER TABLE sso_saml_requests_unavailable RENAME TO sso_saml_requests`); e != nil {
			t.Error("restore private source table", e)
		}
	}()
	configuration := c.f.f.PostgreSQL.Config().Copy()
	freshPool, e := pgxpool.NewWithConfig(c.f.ctx, configuration)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(freshPool.Close)
	oldDB, oldStorage := c.f.accounts.DB, c.f.accounts.Storage
	defer func() { c.f.accounts.DB, c.f.accounts.Storage = oldDB, oldStorage }()
	c.f.accounts.DB, c.f.accounts.Storage = freshPool, nil
	if owner, e := NewSAMLCompletion(c.f.ctx, c.f.s); e == nil || owner != nil {
		t.Fatal("partial schema admitted a new SAML owner")
	}
	if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e == nil {
		t.Fatal("missing SAML storage fell back to another source owner")
	}
	for _, token := range []string{local.Token, legacy.Token} {
		if _, e = c.f.accounts.Authenticate(c.f.ctx, token, "admin"); e != nil {
			t.Fatal("independent local/Gitea authority required SAML storage", e)
		}
	}
	flow := connectorFlow(t, c.f, c.f.source.ConnectorID, "admin", "succeeded")
	flow.BindingID = c.f.source.BindingID
	var fresh identity.IssuedSession
	e = c.f.f.Store.Within(c.f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		fresh, e = c.f.accounts.IssueSessionWithin(ctx, q, c.f.f.User, 1, "admin", "", uuid.NewString(), &flow)
		return e
	})
	if e != nil || fresh.Token == "" {
		t.Fatal("new Gitea issuance required unavailable SAML storage", e)
	}
}

func TestSAMLCompletionProbeNoOAuthDependency(t *testing.T) {
	c := samlCompletionFixtureFor(t, false)
	c.f.s.enabled = false
	c.f.s.keys = nil
	c.f.s.approved = nil
	issued, e := c.finish(c.f.ctx)
	if e != nil || issued.Token == "" {
		t.Fatal("SAML completion required a Gitea keyring or OAuth provider", e)
	}
	if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e != nil {
		t.Fatal("actual SAML source required disabled OAuth authority", e)
	}
}
