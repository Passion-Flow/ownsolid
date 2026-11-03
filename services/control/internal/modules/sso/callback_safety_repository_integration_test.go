//go:build repository_integration

package sso

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func callbackAuthorityFault(t *testing.T, f sourceRepositoryFixture, previous, kind string) {
	t.Helper()
	switch kind {
	case "credential":
		f.exec(t, f.f.Statement(`UPDATE password_credentials SET version=version+1 WHERE user_id=$1`, `UPDATE password_credentials SET version=version+1 WHERE user_id=?`, f.f.User))
	case "logout":
		if e := f.accounts.Logout(f.ctx, previous, "portal", uuid.NewString()); e != nil {
			t.Fatal("actual callback logout", e)
		}
	case "user":
		f.exec(t, f.f.Statement(`UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, f.f.User))
	case "connector":
		a := catalogAuth(t, f, f.f.User, "admin", nil)
		if _, e := f.s.UpdateConnector(f.ctx, a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Current callback authority changed", Enabled: true, Reason: "Synthetic intervening connector mutation"}, uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	case "tenant":
		// Privileged isolated scope fault,not a production tenant-cutover API.
		tenant, workspace := uuid.NewString(), uuid.NewString()
		f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Replacement callback tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Replacement callback tenant','active')`, tenant))
		f.exec(t, f.f.Statement(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Replacement callback Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Replacement callback Workspace','active')`, workspace, tenant))
		f.exec(t, f.f.Statement(`UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1`, `UPDATE installation_state SET tenant_id=?,default_workspace_id=? WHERE id=1`, tenant, workspace))
	}
}

func TestActualSSOCallbackCurrentAuthorityBeforeAndAfterClaim(t *testing.T) {
	for _, phase := range []string{"before_claim", "before_completion"} {
		for _, fault := range []string{"credential", "logout", "user", "connector", "tenant"} {
			t.Run(phase+"_"+fault, func(t *testing.T) {
				f, password := bindingRepositoryFixture(t)
				start, previous := callbackStart(t, f, password, "portal", ModeLink, "local")
				authorization, _ := url.Parse(start.AuthorizationURL)
				var claimed claimedFlow
				if phase == "before_completion" {
					claimed = callbackClaim(t, f, start, "portal", previous)
				}
				before := callbackAudits(t, f)
				callbackAuthorityFault(t, f, previous, fault)
				code := "SSO_FLOW_UNUSABLE"
				if fault == "credential" {
					code = "SSO_PASSWORD_CHANGED"
				}
				if fault == "logout" || fault == "user" {
					code = "AUTH_SESSION_EXPIRED"
					if phase == "before_claim" {
						code = "SSO_BROWSER_CHANGED"
					}
				}
				if phase == "before_claim" {
					out, e := f.s.claim(f.ctx, f.source.ConnectorID, "portal", authorization.Query().Get("state"), start.BrowserToken, previous)
					if e == nil || e.Error() != code || !reflect.DeepEqual(out, claimedFlow{}) {
						t.Fatal("claim ignored current authority or leaked credentials", e)
					}
				} else {
					out, e := f.s.commitFlow(f.ctx, claimed, "9007199254741003", previous, uuid.NewString())
					if e == nil || e.Error() != code || out != (identity.IssuedSession{}) {
						t.Fatal("completion ignored current authority or leaked session", e)
					}
				}
				if callbackAudits(t, f) != before {
					t.Fatal("refused current-authority callback persisted success/failure audit")
				}
				var count int
				if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=$1 AND connector_id=$2 AND subject='9007199254741003'`, `SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=? AND connector_id=? AND subject='9007199254741003'`, f.f.Tenant, f.source.ConnectorID)).Scan(&count); e != nil || count != 0 {
					t.Fatal("authority refusal leaked new binding", e)
				}
			})
		}
	}
}

func TestActualSSOCallbackConcurrentSingleUse(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	start, previous := callbackStart(t, f, password, "portal", ModeLogin, "anonymous")
	authorization, _ := url.Parse(start.AuthorizationURL)
	state := authorization.Query().Get("state")
	begin := make(chan struct{})
	results := make(chan struct {
		flow claimedFlow
		err  error
	}, 2)
	for range 2 {
		go func() {
			<-begin
			out, e := f.s.claim(f.ctx, f.source.ConnectorID, "portal", state, start.BrowserToken, previous)
			results <- struct {
				flow claimedFlow
				err  error
			}{out, e}
		}()
	}
	close(begin)
	wins, rejected := 0, 0
	for range 2 {
		select {
		case out := <-results:
			if out.err == nil && validClaimedFlow(out.flow) {
				wins++
			} else if out.err != nil && out.err.Error() == "SSO_FLOW_UNUSABLE" && reflect.DeepEqual(out.flow, claimedFlow{}) {
				rejected++
			} else {
				t.Fatal("unexpected actual concurrent claim result", out.err)
			}
		case <-f.ctx.Done():
			t.Fatal("bounded concurrent callback claim deadline")
		}
	}
	if wins != 1 || rejected != 1 {
		t.Fatal("concurrent callback verifier ownership was not single-use")
	}
}

func TestActualSSOCallbackAtomicAuditAndClaimRollback(t *testing.T) {
	for _, stage := range []string{"claim_DML", "completion_link_audit", "failure_audit"} {
		t.Run(stage, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			start, previous := callbackStart(t, f, password, "portal", ModeLink, "local")
			authorization, _ := url.Parse(start.AuthorizationURL)
			var claimed claimedFlow
			if stage != "claim_DML" {
				claimed = callbackClaim(t, f, start, "portal", previous)
			}
			before := callbackAudits(t, f)
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			at := 1
			if stage == "completion_link_audit" {
				at = 6
			} else if stage == "failure_audit" {
				at = 2
			}
			// This identified test double rejects only the selected real DML/audit.
			// All preceding binding/session/flow operations reach the actual DB.
			f.accounts.Storage, f.accounts.DB = connectorAuditFaultStore{f.f.Store, at}, nil
			if stage == "claim_DML" {
				out, e := f.s.claim(f.ctx, f.source.ConnectorID, "portal", authorization.Query().Get("state"), start.BrowserToken, previous)
				if e == nil || e.Error() != "SYNTHETIC_CONNECTOR_AUDIT_FAILURE" || !reflect.DeepEqual(out, claimedFlow{}) {
					t.Fatal("claim write failure exposed decrypted credentials", e)
				}
			} else if stage == "completion_link_audit" {
				out, e := f.s.commitFlow(f.ctx, claimed, "9007199254741003", previous, uuid.NewString())
				if e == nil || e.Error() != "SYNTHETIC_CONNECTOR_AUDIT_FAILURE" || out != (identity.IssuedSession{}) {
					t.Fatal("last link audit failure leaked issued session", e)
				}
			} else {
				if e := f.s.failFlow(f.ctx, claimed, "SSO_PROVIDER_REJECTED", uuid.NewString()); e == nil || e.Error() != "SYNTHETIC_CONNECTOR_AUDIT_FAILURE" {
					t.Fatal("failure audit rejection was hidden", e)
				}
			}
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			if callbackAudits(t, f) != before {
				t.Fatal("rejected audit/claim committed partial events")
			}
			if _, e := f.accounts.Authenticate(f.ctx, previous, "portal"); e != nil {
				t.Fatal("session rotation escaped rollback", e)
			}
			var state string
			var present bool
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,verifier_ciphertext IS NOT NULL FROM sso_flows WHERE tenant_id=$1 AND state_hash=$2`, `SELECT state,verifier_ciphertext IS NOT NULL FROM sso_flows WHERE tenant_id=? AND state_hash=?`, f.f.Tenant, security.Digest(authorization.Query().Get("state")))).Scan(&state, &present); e != nil {
				t.Fatal(e)
			}
			if stage == "claim_DML" {
				if state != flowPending || !present {
					t.Fatal("claim rollback destroyed pending verifier")
				}
				claimed = callbackClaim(t, f, start, "portal", previous)
			} else if state != flowClaimed || present {
				t.Fatal("audit rollback changed consumed claim outcome")
			}
			var bindings int
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=$1 AND connector_id=$2 AND subject='9007199254741003'`, `SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=? AND connector_id=? AND subject='9007199254741003'`, f.f.Tenant, f.source.ConnectorID)).Scan(&bindings); e != nil || bindings != 0 {
				t.Fatal("binding escaped audit rollback", e)
			}
			if e := f.s.failFlow(f.ctx, claimed, "SSO_PROVIDER_REJECTED", uuid.NewString()); e != nil {
				t.Fatal("fresh failure recovery after rollback", e)
			}
			state, user, session := callbackStoredState(t, f, claimed.id)
			if state != "failed" || user.Valid || session.Valid {
				t.Fatal("rollback recovery did not retain failed consumed history")
			}
		})
	}
}

func TestActualSSOCallbackCancellationAndFreshRecovery(t *testing.T) {
	for _, stage := range []string{"claim_flow", "completion_user"} {
		t.Run(stage, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			start, previous := callbackStart(t, f, password, "portal", ModeLink, "local")
			authorization, _ := url.Parse(start.AuthorizationURL)
			var claimed claimedFlow
			var id string
			if stage == "completion_user" {
				claimed = callbackClaim(t, f, start, "portal", previous)
				id = f.f.User
			} else {
				if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT id::text FROM sso_flows WHERE state_hash=$1`, `SELECT id FROM sso_flows WHERE state_hash=?`, security.Digest(authorization.Query().Get("state")))).Scan(&id); e != nil {
					t.Fatal(e)
				}
			}
			ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			holder, stop := context.WithTimeout(f.ctx, 5*time.Second)
			defer stop()
			go func() {
				done <- f.f.Store.Within(holder, false, func(ctx context.Context, q transaction.Executor) error {
					var found string
					query := f.f.Statement(`SELECT id::text FROM sso_flows WHERE id=$1 FOR UPDATE`, `SELECT id FROM sso_flows WHERE id=? FOR UPDATE`, id)
					if stage == "completion_user" {
						query = f.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, id)
					}
					if e := q.QueryRow(ctx, query).Scan(&found); e != nil {
						return e
					}
					close(ready)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			select {
			case <-ready:
			case e := <-done:
				t.Fatal("callback held row", e)
			case <-time.After(2 * time.Second):
				t.Fatal("callback row holder deadline")
			}
			at := 6
			if stage == "completion_user" {
				at = 2
			}
			observed := &catalogObservedStore{Store: f.f.Store, at: at, ready: make(chan struct{}, 1)}
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			f.accounts.Storage, f.accounts.DB = observed, nil
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			call, cancel := context.WithCancel(f.ctx)
			defer cancel()
			results := make(chan error, 1)
			go func() {
				if stage == "claim_flow" {
					out, e := f.s.claim(call, f.source.ConnectorID, "portal", authorization.Query().Get("state"), start.BrowserToken, previous)
					if !reflect.DeepEqual(out, claimedFlow{}) {
						e = errors.New("CANCELLED_CLAIM_EXPOSED_CREDENTIALS")
					}
					results <- e
				} else {
					out, e := f.s.commitFlow(call, claimed, "9007199254741003", previous, uuid.NewString())
					if out != (identity.IssuedSession{}) {
						e = errors.New("CANCELLED_COMPLETION_EXPOSED_SESSION")
					}
					results <- e
				}
			}()
			select {
			case <-observed.ready:
			case e := <-results:
				t.Fatal("callback returned before held-row attempt", e)
			case <-time.After(2 * time.Second):
				t.Fatal("callback row attempt deadline")
			}
			select {
			case e := <-results:
				t.Fatal("callback bypassed held row", e)
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case e := <-results:
				if !errors.Is(e, context.Canceled) {
					t.Fatal("cancelled callback did not fail closed", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("callback cancellation deadline")
			}
			unlock()
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("holder cleanup deadline")
			}
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			fresh, token := callbackStart(t, f, password, "portal", ModeLink, "local")
			next := callbackClaim(t, f, fresh, "portal", token)
			if !validClaimedFlow(next) {
				t.Fatal("fresh callback after cancellation failed")
			}
		})
	}
}

func TestActualSSOCallbackClaimLocksUserBeforeConnectorAndReleasesBeforeIO(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	start, previous := callbackStart(t, f, password, "portal", ModeLink, "local")
	authorization, _ := url.Parse(start.AuthorizationURL)
	observed := &callbackLockStore{Store: f.f.Store, unlinkReady: make(chan struct{}, 1), claimReady: make(chan struct{}, 1), release: make(chan struct{}), authenticated: make(chan struct{}, 1), begin: make(chan struct{})}
	originalStore, originalDB := f.accounts.Storage, f.accounts.DB
	f.accounts.Storage, f.accounts.DB = observed, nil
	defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
	ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
	defer cancel()
	var beginOnce sync.Once
	begin := func() { beginOnce.Do(func() { close(observed.begin) }) }
	defer begin()
	claims := make(chan struct {
		flow claimedFlow
		err  error
	}, 1)
	go func() {
		out, e := f.s.claim(context.WithValue(ctx, callbackLockRole{}, "claim"), f.source.ConnectorID, "portal", authorization.Query().Get("state"), start.BrowserToken, previous)
		claims <- struct {
			flow claimedFlow
			err  error
		}{out, e}
	}()
	select {
	case <-observed.authenticated:
	case out := <-claims:
		t.Fatal("claim authentication boundary", out.err)
	case <-ctx.Done():
		t.Fatal("claim authentication deadline")
	}
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	go func() {
		done <- f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, f.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, f.f.User)).Scan(&id); e != nil {
				return e
			}
			close(ready)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal("actual user lock holder", e)
	case <-ctx.Done():
		t.Fatal("user holder deadline")
	}
	begin()
	select {
	case <-observed.claimReady:
	case <-ctx.Done():
		t.Fatal("claim did not attempt its first user fence")
	}
	select {
	case out := <-claims:
		t.Fatal("claim bypassed the held current user fence", out.err)
	case <-time.After(150 * time.Millisecond):
	}
	unlock()
	select {
	case out := <-claims:
		if out.err != nil || !validClaimedFlow(out.flow) {
			t.Fatal("claim failed after earlier user fence released", out.err)
		}
	case <-ctx.Done():
		t.Fatal("claim/user fence lock cycle")
	}
	// A claimed result is external-IO input, never a retained SQL transaction.
	if e := f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var id string
		return q.QueryRow(ctx, f.f.Statement("SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", f.f.User)).Scan(&id)
	}); e != nil {
		t.Fatal("claim retained a user lock across external IO", e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("user holder cleanup deadline")
	}
}

func TestActualSSOCallbackExpiredPendingAndNewStartRecovery(t *testing.T) {
	f := catalogRepositoryFixture(t)
	id, state, browser, verifier := uuid.NewString(), security.Token(), security.Token(), security.Token()
	sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, f.source.ConnectorID, id, flowVerifierPurpose}, []byte(verifier))
	if e != nil {
		t.Fatal(e)
	}
	// Real durable expired input models a callback arriving after downtime. The
	// database clock evaluates freshness;no wall clock or immutable field changes.
	f.exec(t, f.f.Statement(`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES($1,$2,$3,1,1,'portal','login',$4,$5,$6,$7,$8,$9,clock_timestamp()-interval '2 minutes',clock_timestamp()-interval '1 minute')`, `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,1,1,'portal','login',?,?,?,?,?,?,TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(6)),TIMESTAMPADD(MINUTE,-1,UTC_TIMESTAMP(6)))`, id, f.f.Tenant, f.source.ConnectorID, security.Digest(state), f.s.browserProof(f.source.ConnectorID, "portal", browser), security.Digest("anonymous"), sealed.keyID, sealed.nonce, sealed.ciphertext))
	before := callbackAudits(t, f)
	out, e := f.s.claim(f.ctx, f.source.ConnectorID, "portal", state, browser, "")
	if e == nil || e.Error() != "SSO_FLOW_EXPIRED" || !reflect.DeepEqual(out, claimedFlow{}) || callbackAudits(t, f) != before {
		t.Fatal("expired callback exposed verifier or mutated outcome", e)
	}
	fresh, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString())
	if e != nil {
		t.Fatal("expired pending row must not block a new authorization", e)
	}
	next := callbackClaim(t, f, fresh, "portal", "")
	if !validClaimedFlow(next) {
		t.Fatal("fresh recovery after durable expiry failed")
	}
}

func TestActualSSOCallbackExpiryAfterSuccessfulClaim(t *testing.T) {
	f := catalogRepositoryFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Second)
	defer cancel()
	f.ctx = ctx
	policy := f.accounts.Config.Policy()
	policy.SSO.FlowSeconds = 60
	f.accounts.Config.Runtime = &policy
	service, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	f.s, f.accounts.Sources = service, service
	start, e := f.s.Start(ctx, f.source.ConnectorID, "portal", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claimed := callbackClaim(t, f, start, "portal", "")
	before := callbackAudits(t, f)
	// Exercise the real supported minimum TTL. Poll only the owned row and use
	// the actual server clock;never shorten the policy below its supported bound.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var expired bool
		if e := f.f.Store.QueryRow(ctx, f.f.Statement(`SELECT expires_at<=clock_timestamp() FROM sso_flows WHERE tenant_id=$1 AND id=$2`, `SELECT expires_at<=UTC_TIMESTAMP(6) FROM sso_flows WHERE tenant_id=? AND id=?`, f.f.Tenant, claimed.id)).Scan(&expired); e != nil {
			t.Fatal(e)
		}
		if expired {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("real server-clock flow expiry deadline")
		}
	}
	issued, e := f.s.commitFlow(ctx, claimed, "9007199254740993", "", uuid.NewString())
	if e == nil || e.Error() != "SSO_FLOW_EXPIRED" || issued != (identity.IssuedSession{}) || callbackAudits(t, f) != before {
		t.Fatal("expired consumed claim issued a session", e)
	}
	if e := f.s.failFlow(ctx, claimed, "SSO_FLOW_EXPIRED", uuid.NewString()); e != nil {
		t.Fatal("expired consumed claim recovery", e)
	}
	state, user, session := callbackStoredState(t, f, claimed.id)
	if state != "failed" || user.Valid || session.Valid {
		t.Fatal("expired claim recovery changed successful authority")
	}
}
