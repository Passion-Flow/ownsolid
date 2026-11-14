//go:build repository_integration

package sso

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/beevik/etree"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type samlCompletionFixture struct {
	f        sourceRepositoryFixture
	owner    samlClaimProbe
	claim    samlProbeClaim
	staged   samlStagedProbe
	previous string
}

func samlCompletionFixtureFor(t *testing.T, shortSession bool) samlCompletionFixture {
	t.Helper()
	f, password, p, crypto := samlStageFixture(t)
	start, previous, target := samlProbeStarted(t, f, password, p, "portal", false)
	claim, e := p.claim(f.ctx, target, "portal", start.relay, start.browser, uuid.NewString())
	if e != nil {
		t.Fatal("actual SAML request claim", e)
	}
	proof := samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), func(a *etree.Element) {
		if shortSession {
			a.SelectElement("AuthnStatement").CreateAttr("SessionNotOnOrAfter", crypto.now.Add(5*time.Second).Format(time.RFC3339))
		}
	})
	staged, e := p.stage(f.ctx, claim, proof, uuid.NewString())
	if e != nil {
		t.Fatal("actual signed result staged", e)
	}
	return samlCompletionFixture{f, p, claim, staged, previous}
}
func (c samlCompletionFixture) finish(ctx context.Context) (identity.IssuedSession, error) {
	return c.owner.complete(ctx, c.claim.connector, "portal", c.claim.id, c.staged.continuation, c.previous, uuid.NewString())
}
func (c samlCompletionFixture) counts(t *testing.T) (string, [3]int) {
	t.Helper()
	var state string
	var counts [3]int
	if e := c.f.f.PostgreSQL.QueryRow(c.f.ctx, `SELECT state FROM saml_claim_probe_flows WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.claim.id).Scan(&state); e != nil {
		t.Fatal(e)
	}
	for i, query := range []string{`SELECT count(*) FROM sessions WHERE sso_tenant_id=$1 AND sso_flow_id=$2`, `SELECT count(*) FROM saml_probe_verified_results WHERE tenant_id=$1 AND flow_id=$2 AND completed_binding_version IS NOT NULL`, `SELECT count(*) FROM saml_persistent_identities WHERE tenant_id=$1 AND id=(SELECT identity_id FROM saml_probe_verified_results WHERE tenant_id=$1 AND flow_id=$2) AND active`} {
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
	if e = c.f.f.PostgreSQL.QueryRow(c.f.ctx, `SELECT se.sso_tenant_id::text,se.sso_connector_id::text,se.sso_binding_id::text,se.sso_flow_id::text,se.sso_auth_version,r.source_until FROM sessions se JOIN saml_probe_verified_results r ON r.tenant_id=se.sso_tenant_id AND r.flow_id=se.sso_flow_id WHERE se.id=$1`, issued.SessionID).Scan(&stored.TenantID, &stored.ConnectorID, &stored.BindingID, &stored.FlowID, &stored.AuthVersion, &deadline); e != nil || stored.FlowID != c.claim.id || stored.BindingID != c.staged.identity || issued.ExpiresAt.After(deadline) {
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
	c.f.accounts.Sources = &samlCompletionSources{service: c.f.s, legacy: c.f.s}
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
			c := samlCompletionFixtureFor(t, false)
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
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE password_credentials SET version=version+1 WHERE user_id=$1`, c.f.f.User))
			case "account_disabled":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, c.f.f.User))
			case "profile_revision":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_identity_contexts SET revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, c.f.f.Tenant, connector))
			case "connector_disabled":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, connector))
			case "expired_result":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_probe_verified_results SET expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND flow_id=$2`, c.f.f.Tenant, c.claim.id))
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
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_persistent_identities SET bound_user_id=$1,active=true,binding_version=binding_version+1 WHERE tenant_id=$2 AND id=$3`, user, c.f.f.Tenant, c.staged.identity))
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
	for _, kind := range []string{"signed_session_end", "profile_disable", "connector_disable", "binding_revoke"} {
		t.Run(kind, func(t *testing.T) {
			c := samlCompletionFixtureFor(t, kind == "signed_session_end")
			issued, e := c.finish(c.f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "signed_session_end":
				if _, e = c.f.f.PostgreSQL.Exec(c.f.ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM $1::timestamptz-clock_timestamp())))`, issued.ExpiresAt.Add(20*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
			case "profile_disable":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_identity_contexts SET enabled=false,revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, c.f.f.Tenant, c.claim.connector))
			case "connector_disable":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.claim.connector))
			case "binding_revoke":
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_persistent_identities SET active=false,binding_version=binding_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.staged.identity))
			}
			if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e == nil {
				t.Fatal("actual sourced session outlived revoked or expired authority")
			}
			if kind == "binding_revoke" {
				c.f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_persistent_identities SET active=true,binding_version=binding_version+1 WHERE tenant_id=$1 AND id=$2`, c.f.f.Tenant, c.staged.identity))
				if _, e = c.f.accounts.Authenticate(c.f.ctx, issued.Token, "portal"); e == nil {
					t.Fatal("reactivating binding revived its previous source session")
				}
			}
		})
	}
}
func TestSAMLCompletionProbeConcurrentAndAudit(t *testing.T) {
	for _, kind := range []string{"two_finishers", "audit_rollback"} {
		t.Run(kind, func(t *testing.T) {
			c := samlCompletionFixtureFor(t, false)
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
			table, id := "saml_claim_probe_flows", c.claim.id
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
