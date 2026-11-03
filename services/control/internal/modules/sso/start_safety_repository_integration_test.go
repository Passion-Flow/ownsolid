//go:build repository_integration

package sso

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type startOutcome struct {
	value StartResult
	err   error
}

func startResult(t *testing.T, results <-chan startOutcome) startOutcome {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(6 * time.Second):
		t.Fatal("bounded actual start deadline")
		return startOutcome{}
	}
}

func startPolicy(t *testing.T, f sourceRepositoryFixture, maximum int) sourceRepositoryFixture {
	t.Helper()
	policy := f.accounts.Config.Policy()
	policy.SSO.MaximumPendingFlows = maximum
	f.accounts.Config.Runtime = &policy
	service, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	f.s, f.accounts.Sources = service, service
	return f
}

func TestActualSSOStartConcurrentCapacityAndReleasedStates(t *testing.T) {
	for _, scenario := range []string{"anonymous_distinct_connectors", "distinct_current_actors", "expired_and_terminal_release_claimed_consumes"} {
		t.Run(scenario, func(t *testing.T) {
			f := startPolicy(t, catalogRepositoryFixture(t), 1)
			second := catalogSeedConnector(t, f, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, "Second pending admission target", true, time.Now().UTC().Truncate(time.Microsecond))
			if scenario == "expired_and_terminal_release_claimed_consumes" {
				// Privileged expired initial state;immutable fields are never updated.
				id := uuid.NewString()
				sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, f.source.ConnectorID, id, flowVerifierPurpose}, []byte(security.Token()))
				if e != nil {
					t.Fatal(e)
				}
				f.exec(t, f.f.Statement(`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES($1,$2,$3,1,1,'portal','login',$4,$5,$6,$7,$8,$9,clock_timestamp()-interval '2 minutes',clock_timestamp()-interval '1 minute')`, `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,1,1,'portal','login',?,?,?,?,?,?,TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(6)),TIMESTAMPADD(MINUTE,-1,UTC_TIMESTAMP(6)))`, id, f.f.Tenant, f.source.ConnectorID, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest(security.Token()), sealed.keyID, sealed.nonce, sealed.ciphertext))
				flow := connectorFlow(t, f, second, "portal", "claimed")
				before := startCounts(t, f)
				if out, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString()); e == nil || e.Error() != "SSO_CAPACITY_EXCEEDED" || out != (StartResult{}) || startCounts(t, f) != before {
					t.Fatal("claimed flow must consume quota", e)
				}
				f.exec(t, f.f.Statement(`UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=clock_timestamp() WHERE id=$1`, `UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=UTC_TIMESTAMP(6) WHERE id=?`, flow.FlowID))
				if out, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString()); e != nil || out.BrowserToken == "" {
					t.Fatal("expired/terminal flows must release admission", e)
				}
				return
			}
			previous := []string{"", ""}
			if scenario == "distinct_current_actors" {
				for i, user := range []string{f.f.User, bindingOtherUser(t, f)} {
					var issued identity.IssuedSession
					if e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
						var e error
						issued, e = f.accounts.IssueSessionWithin(ctx, q, user, 1, "portal", "", uuid.NewString(), nil)
						return e
					}); e != nil {
						t.Fatal("actual distinct actor issuance", e)
					}
					previous[i] = issued.Token
				}
			}
			before := startCounts(t, f)
			start, results := make(chan struct{}), make(chan startOutcome, 2)
			for i, target := range []string{f.source.ConnectorID, second} {
				go func() {
					<-start
					out, e := f.s.Start(f.ctx, target, "portal", previous[i], nil, StartInput{Mode: ModeLogin}, uuid.NewString())
					results <- startOutcome{out, e}
				}()
			}
			close(start)
			success, limited := 0, 0
			for range 2 {
				out := startResult(t, results)
				if out.err == nil && out.value.BrowserToken != "" {
					success++
				} else if out.err != nil && out.err.Error() == "SSO_CAPACITY_EXCEEDED" && out.value == (StartResult{}) {
					limited++
				} else {
					t.Fatal("unexpected pending admission outcome", out.err)
				}
			}
			if success != 1 || limited != 1 || startCounts(t, f) != ([2]int{before[0] + 1, before[1] + 1}) {
				t.Fatal("concurrent distinct target/actor starts exceeded tenant cap")
			}
		})
	}
}

func TestActualSSOStartAuditRollbackAndConnectorRefusals(t *testing.T) {
	for _, fault := range []string{"audit_failure", "missing_connector", "disabled_connector", "foreign_connector", "wrong_password", "stale_link_actor"} {
		t.Run(fault, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			target := f.source.ConnectorID
			var actor *identity.Auth
			previous := ""
			input := StartInput{Mode: ModeLogin}
			code := "SSO_CONNECTOR_NOT_FOUND"
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			switch fault {
			case "audit_failure":
				f.accounts.Storage, f.accounts.DB = connectorAuditFaultStore{f.f.Store, 2}, nil
				code = "SYNTHETIC_CONNECTOR_AUDIT_FAILURE"
			case "missing_connector":
				target = uuid.NewString()
			case "disabled_connector":
				target = catalogSeedConnector(t, f, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, "Disabled start target", false, time.Now().UTC().Truncate(time.Microsecond))
				code = "SSO_CONNECTOR_DISABLED"
			case "foreign_connector":
				tenant := uuid.NewString()
				f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign start tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign start tenant','active')`, tenant))
				target = catalogSeedConnector(t, f, tenant, f.accounts.Config.SSO.Providers[0].Origin, "Foreign start target", true, time.Now().UTC().Truncate(time.Microsecond))
			case "wrong_password", "stale_link_actor":
				a, token := startActor(t, f, password, "portal", false)
				actor, previous = &a, token
				reason := "Explicit synthetic denied link"
				input = StartInput{Mode: ModeLink, Password: &password, Reason: &reason}
				if fault == "wrong_password" {
					wrong := "Synthetic incorrect password"
					input.Password = &wrong
					code = "SSO_PASSWORD_REQUIRED"
				} else {
					actor.Epoch++
					code = "AUTH_REQUIRED"
				}
			}
			before := startCounts(t, f)
			out, e := f.s.Start(f.ctx, target, "portal", previous, actor, input, uuid.NewString())
			if e == nil || e.Error() != code || out != (StartResult{}) || startCounts(t, f) != before {
				t.Fatal("refused/rolled back start leaked flow/audit/URL", e)
			}
		})
	}
}

func TestActualSSOStartWaitingAuthorityCancellationAndRecovery(t *testing.T) {
	for _, scenario := range []string{"user_revoked", "credential_changed", "source_revoked", "cancel_user"} {
		t.Run(scenario, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			actor, previous := startActor(t, f, password, "admin", scenario == "source_revoked")
			ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			ctx, stop := context.WithTimeout(f.ctx, 5*time.Second)
			defer stop()
			go func() {
				done <- f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
					var id string
					query := f.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, f.f.User)
					if scenario == "source_revoked" {
						query = f.f.Statement(`SELECT id::text FROM sso_connectors WHERE id=$1 FOR UPDATE`, `SELECT id FROM sso_connectors WHERE id=? FOR UPDATE`, f.source.ConnectorID)
					}
					if e := q.QueryRow(ctx, query).Scan(&id); e != nil {
						return e
					}
					close(ready)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					switch scenario {
					case "user_revoked":
						_, e := q.Exec(ctx, f.f.Statement(`UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, f.f.User))
						return e
					case "credential_changed":
						_, e := q.Exec(ctx, f.f.Statement(`UPDATE password_credentials SET version=version+1 WHERE user_id=$1`, `UPDATE password_credentials SET version=version+1 WHERE user_id=?`, f.f.User))
						return e
					case "source_revoked":
						if _, e := q.Exec(ctx, f.f.Statement(`UPDATE sso_connectors SET enabled=false,auth_version=auth_version+1,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE sso_connectors SET enabled=false,auth_version=auth_version+1,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, f.source.ConnectorID)); e != nil {
							return e
						}
						_, e := q.Exec(ctx, f.f.Statement(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='sso_connector_changed' WHERE sso_tenant_id=$1 AND sso_connector_id=$2 AND revoked_at IS NULL`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='sso_connector_changed' WHERE sso_tenant_id=? AND sso_connector_id=? AND revoked_at IS NULL`, f.f.Tenant, f.source.ConnectorID))
						return e
					}
					return nil
				})
			}()
			select {
			case <-ready:
			case e := <-done:
				t.Fatal("start row holder", e)
			case <-time.After(2 * time.Second):
				t.Fatal("start holder deadline")
			}
			at := 1
			if scenario == "source_revoked" {
				at = 4
			}
			observed := &catalogObservedStore{Store: f.f.Store, at: at, ready: make(chan struct{}, 1)}
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			f.accounts.Storage, f.accounts.DB = observed, nil
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			before := startCounts(t, f)
			reason := "Explicit synthetic queued link"
			input := StartInput{Mode: ModeLink, Password: &password, Reason: &reason}
			results := make(chan startOutcome, 1)
			call, cancel := context.WithCancel(f.ctx)
			defer cancel()
			go func() {
				out, e := f.s.Start(call, f.source.ConnectorID, "admin", previous, &actor, input, uuid.NewString())
				results <- startOutcome{out, e}
			}()
			select {
			case <-observed.ready:
			case out := <-results:
				t.Fatal("start returned before lock attempt", out.err)
			case <-time.After(2 * time.Second):
				t.Fatal("start lock attempt deadline")
			}
			select {
			case out := <-results:
				t.Fatal("start bypassed held row", out.err)
			case <-time.After(100 * time.Millisecond):
			}
			if scenario == "cancel_user" {
				cancel()
			} else {
				unlock()
			}
			out := startResult(t, results)
			if scenario == "cancel_user" {
				if !errors.Is(out.err, context.Canceled) || out.value != (StartResult{}) {
					t.Fatal("cancelled start leaked success", out.err)
				}
				unlock()
			} else {
				code := "AUTH_SESSION_EXPIRED"
				if scenario == "credential_changed" {
					code = "SSO_PASSWORD_CHANGED"
				}
				if out.err == nil || out.err.Error() != code || out.value != (StartResult{}) {
					t.Fatal("queued start ignored current authority", out.err)
				}
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal("start holder/source revocation", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("start source/session cycle")
			}
			if startCounts(t, f) != before {
				t.Fatal("queued denied start left flow/audit")
			}
			if scenario == "cancel_user" {
				f.accounts.Storage, f.accounts.DB = originalStore, originalDB
				if out, e := f.s.Start(f.ctx, f.source.ConnectorID, "admin", previous, &actor, input, uuid.NewString()); e != nil || out.BrowserToken == "" {
					t.Fatal("fresh start after cancellation", e)
				}
			}
		})
	}
}
