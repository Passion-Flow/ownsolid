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
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type bindingOutcome struct {
	value Binding
	err   error
}

func bindingResult(t *testing.T, results <-chan bindingOutcome) bindingOutcome {
	t.Helper()
	select {
	case out := <-results:
		return out
	case <-time.After(6 * time.Second):
		t.Fatal("bounded actual binding operation deadline")
		return bindingOutcome{}
	}
}

func TestActualSSOBindingRollbackBoundsAndCorruptIntent(t *testing.T) {
	for _, fault := range []string{"audit_failure", "revision_exhaustion", "corrupt_intent", "foreign_binding"} {
		t.Run(fault, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			a := catalogAuth(t, f, f.f.User, "portal", nil)
			id, key := f.source.BindingID, uuid.NewString()
			input := UnlinkBinding{Revision: 1, Password: password, Reason: "Synthetic integrity refusal"}
			code := "SSO_BINDING_NOT_FOUND"
			sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
			pending := bindingLinkFlow(t, f, a, "pending")
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			switch fault {
			case "audit_failure":
				// The fifth DML is the actual audit attempt. Earlier binding,
				// session,flow and intent DML all reach the real transaction.
				f.accounts.Storage, f.accounts.DB = connectorAuditFaultStore{f.f.Store, 5}, nil
				code = "SYNTHETIC_CONNECTOR_AUDIT_FAILURE"
			case "revision_exhaustion":
				id = uuid.NewString()
				f.exec(t, f.f.Statement(`INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id,revision) VALUES($1,$2,$3,'9007199254741007',$4,$5)`, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id,revision) VALUES(?,?,?,'9007199254741007',?,?)`, id, f.f.Tenant, f.source.ConnectorID, f.f.User, identity.MaximumAuthVersion))
				input.Revision = identity.MaximumAuthVersion - 1
				code = "SSO_REVISION_CONFLICT"
			case "corrupt_intent":
				fingerprint, e := f.s.fingerprint(unlinkBindingOperation, id, input)
				if e != nil {
					t.Fatal(e)
				}
				// Privileged corrupt INSERT preserves immutable intent triggers.
				f.exec(t, f.f.Statement(`INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES($1,$2,$3,$4,$5,$6,'{}')`, `INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES(?,?,?,?,?,?,'{}')`, f.f.Tenant, f.f.User, key, unlinkBindingOperation, id, fingerprint))
				code = "STORAGE_UNAVAILABLE"
			case "foreign_binding":
				id = bindingSeed(t, f, f.f.Tenant, f.source.ConnectorID, bindingOtherUser(t, f), "9007199254741009", true, time.Now().UTC().Truncate(time.Microsecond))
			}
			before := catalogReadCounts(t, f)
			out, e := f.s.Unlink(f.ctx, a, id, input, key, uuid.NewString())
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			if e == nil || e.Error() != code || out != (Binding{}) || catalogReadCounts(t, f) != before {
				t.Fatal("actual failed unlink leaked resource/intent/audit", e)
			}
			stored, e := selectBindingWithin(f.ctx, f.f.Store, f.f.Tenant, f.f.User, f.source.BindingID)
			if e != nil || !stored.Active || stored.Revision != 1 {
				t.Fatal("failed unlink changed original binding", e)
			}
			var state string
			var present bool
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,verifier_ciphertext IS NOT NULL FROM sso_flows WHERE id=$1`, `SELECT state,verifier_ciphertext IS NOT NULL FROM sso_flows WHERE id=?`, pending)).Scan(&state, &present); e != nil || state != "pending" || !present {
				t.Fatal("failed unlink destroyed verifier", e)
			}
			assertConnectorAuth(t, f, sourced, true)
		})
	}
}

func TestActualSSOBindingWaitingAuthorityCancellationAndRecovery(t *testing.T) {
	for _, target := range []string{"user_revoked", "credential_changed", "cancel_user", "cancel_binding", "source_unlink", "source_inventory", "source_catalog"} {
		t.Run(target, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			a := catalogAuth(t, f, f.f.User, "admin", nil)
			if target == "source_unlink" || target == "source_inventory" || target == "source_catalog" {
				a = catalogAuth(t, f, f.f.User, "admin", &f.source)
			}
			ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			holderContext, stop := context.WithTimeout(f.ctx, 5*time.Second)
			defer stop()
			go func() {
				done <- f.f.Store.Within(holderContext, false, func(ctx context.Context, q transaction.Executor) error {
					var id string
					statement := f.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, f.f.User)
					if target == "cancel_binding" {
						statement = f.f.Statement(`SELECT id::text FROM sso_bindings WHERE id=$1 FOR UPDATE`, `SELECT id FROM sso_bindings WHERE id=? FOR UPDATE`, f.source.BindingID)
					}
					if target == "source_unlink" || target == "source_inventory" || target == "source_catalog" {
						statement = f.f.Statement(`SELECT id::text FROM sso_connectors WHERE id=$1 FOR UPDATE`, `SELECT id FROM sso_connectors WHERE id=? FOR UPDATE`, f.source.ConnectorID)
					}
					if e := q.QueryRow(ctx, statement).Scan(&id); e != nil {
						return e
					}
					close(ready)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if target == "credential_changed" {
						_, e := q.Exec(ctx, f.f.Statement(`UPDATE password_credentials SET version=version+1 WHERE user_id=$1`, `UPDATE password_credentials SET version=version+1 WHERE user_id=?`, f.f.User))
						return e
					}
					if target == "user_revoked" {
						_, e := q.Exec(ctx, f.f.Statement(`UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, f.f.User))
						return e
					}
					if target == "source_unlink" || target == "source_inventory" || target == "source_catalog" {
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
				t.Fatal("actual row holder", e)
			case <-time.After(2 * time.Second):
				t.Fatal("holder deadline")
			}
			at := 1
			if target == "source_unlink" {
				at = 5
			}
			if target == "source_inventory" || target == "source_catalog" {
				at = 4
			}
			// Local unlink:frame10,recheck user/session/source3,credential1,
			// intent1,then the exact binding FOR UPDATE (query16).
			if target == "cancel_binding" {
				at = 16
			}
			observed := &catalogObservedStore{Store: f.f.Store, at: at, ready: make(chan struct{}, 1)}
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			f.accounts.Storage, f.accounts.DB = observed, nil
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			results := make(chan bindingOutcome, 1)
			key := uuid.NewString()
			input := UnlinkBinding{Revision: 1, Password: password, Reason: "Synthetic queued account recovery"}
			go func() {
				if target == "source_inventory" {
					items, e := f.s.ListBindings(ctx, a)
					if e != nil && len(items) != 0 {
						e = errors.New("PARTIAL_BINDING_DISCLOSURE")
					}
					results <- bindingOutcome{err: e}
					return
				}
				if target == "source_catalog" {
					page, e := f.s.ListConnectors(ctx, a, 1, "")
					if e != nil && (len(page.Items) != 0 || page.NextCursor != nil) {
						e = errors.New("PARTIAL_CATALOG_DISCLOSURE")
					}
					results <- bindingOutcome{err: e}
					return
				}
				value, e := f.s.Unlink(ctx, a, f.source.BindingID, input, key, uuid.NewString())
				results <- bindingOutcome{value, e}
			}()
			select {
			case <-observed.ready:
			case out := <-results:
				t.Fatal("operation ended before exact lock attempt", out.err)
			case <-time.After(2 * time.Second):
				t.Fatal("actual lock attempt deadline")
			}
			select {
			case out := <-results:
				t.Fatal("operation bypassed held row", out.err)
			case <-time.After(100 * time.Millisecond):
			}
			cancelled := target == "cancel_user" || target == "cancel_binding"
			if cancelled {
				cancel()
			} else {
				unlock()
			}
			out := bindingResult(t, results)
			if cancelled {
				if !errors.Is(out.err, context.Canceled) || out.value != (Binding{}) {
					t.Fatal("cancelled operation leaked success", out.err)
				}
				unlock()
			} else {
				code := "AUTH_SESSION_EXPIRED"
				if target == "credential_changed" {
					code = "SSO_PASSWORD_CHANGED"
				}
				if out.err == nil || out.err.Error() != code || out.value != (Binding{}) {
					t.Fatal("queued operation ignored current authority/credential", out.err)
				}
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal("bulk revocation/holder failed", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("source/session lock cycle")
			}
			if bindingCounts(t, f, f.source.BindingID, key) != ([3]int{1, 0, 0}) {
				t.Fatal("queued denial left intent/audit")
			}
			if cancelled {
				f.accounts.Storage, f.accounts.DB = originalStore, originalDB
				value, e := f.s.Unlink(f.ctx, a, f.source.BindingID, input, key, uuid.NewString())
				if e != nil || value.Active || value.Revision != 2 {
					t.Fatal("fresh recovery after cancellation", e)
				}
			}
		})
	}
}
