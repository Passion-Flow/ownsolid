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

type connectorOutcome struct {
	value Connector
	err   error
}

func connectorResult(t *testing.T, result <-chan connectorOutcome) connectorOutcome {
	t.Helper()
	select {
	case out := <-result:
		return out
	case <-time.After(6 * time.Second):
		t.Fatal("bounded actual connector writer deadline")
		return connectorOutcome{}
	}
}

func TestActualSSOConnectorConcurrentIdenticalIntent(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "admin", nil)
	input, key := connectorInput(f), uuid.NewString()
	start, results := make(chan struct{}), make(chan connectorOutcome, 2)
	for range 2 {
		go func() {
			<-start
			value, e := f.s.CreateConnector(f.ctx, a, input, key, uuid.NewString())
			results <- connectorOutcome{value, e}
		}()
	}
	close(start)
	first, second := connectorResult(t, results), connectorResult(t, results)
	if first.err != nil || second.err != nil || !sameConnector(first.value, second.value) || connectorCounts(t, f, input.ID, key) != ([3]int{1, 1, 1}) {
		t.Fatal("concurrent identical create must commit exactly one resource/intent/audit", first.err, second.err)
	}
}

func TestActualSSOConnectorCapacityAcrossActors(t *testing.T) {
	f := catalogRepositoryFixture(t)
	policy := f.accounts.Config.Policy()
	policy.SSO.MaximumConnectors = 2
	f.accounts.Config.Runtime = &policy
	service, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	f.s, f.accounts.Sources = service, service
	user := uuid.NewString()
	email := "synthetic+" + user + "@example.invalid"
	f.exec(t, f.f.Statement(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Second capacity actor','en','active',true)`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Second capacity actor','en','active',true)`, user, email, email))
	actors := []identity.Auth{catalogAuth(t, f, f.f.User, "admin", nil), catalogAuth(t, f, user, "admin", nil)}
	start, results := make(chan struct{}), make(chan connectorOutcome, 2)
	for _, actor := range actors {
		input := connectorInput(f)
		go func() {
			<-start
			value, e := f.s.CreateConnector(f.ctx, actor, input, uuid.NewString(), uuid.NewString())
			results <- connectorOutcome{value, e}
		}()
	}
	close(start)
	success, limited := 0, 0
	for range 2 {
		out := connectorResult(t, results)
		switch {
		case out.err == nil && out.value.ID != "":
			success++
		case out.err != nil && out.err.Error() == "SSO_CONNECTOR_LIMIT" && out.value == (Connector{}):
			limited++
		default:
			t.Fatal("unexpected concurrent capacity outcome", out.err)
		}
	}
	var counts [3]int
	for i, table := range []string{"audit_events", "sso_mutation_intents", "sso_connectors"} {
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE tenant_id=$1`
		my := `SELECT COUNT(*) FROM ` + table + ` WHERE tenant_id=?`
		if table == "audit_events" {
			query += ` AND action='sso.connector_created' AND outcome='success'`
			my += ` AND action='sso.connector_created' AND outcome='success'`
		}
		if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(query, my, f.f.Tenant)).Scan(&counts[i]); e != nil {
			t.Fatal("actual current-tenant capacity inventory", e)
		}
	}
	if success != 1 || limited != 1 || counts[0] != 1 || counts[1] != 1 || counts[2] != policy.SSO.MaximumConnectors {
		t.Fatal("distinct actor locks must not permit capacity overflow", counts)
	}
}

// This explicit test-only fault delegates all prior SQL to the actual database.
// It fails the selected audit attempt;it never fabricates rows or commit success.
type connectorAuditFaultStore struct {
	transaction.Store
	failAt int
}
type connectorAuditFaultExecutor struct {
	transaction.Executor
	failAt, executed int
}

func (s connectorAuditFaultStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &connectorAuditFaultExecutor{Executor: q, failAt: s.failAt})
	})
}
func (q *connectorAuditFaultExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	q.executed++
	if q.executed == q.failAt {
		return 0, errors.New("SYNTHETIC_CONNECTOR_AUDIT_FAILURE")
	}
	return q.Executor.Exec(ctx, statement)
}

func TestActualSSOConnectorAtomicRollbackAndVersionBounds(t *testing.T) {
	for _, operation := range []string{"create_audit_failure", "disable_audit_failure", "version_exhaustion"} {
		t.Run(operation, func(t *testing.T) {
			f := catalogRepositoryFixture(t)
			a := catalogAuth(t, f, f.f.User, "admin", nil)
			key := uuid.NewString()
			if operation == "version_exhaustion" {
				for _, versions := range [][2]int64{{identity.MaximumAuthVersion, 1}, {1, identity.MaximumAuthVersion}} {
					input := connectorInput(f)
					sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, input.ID, input.ID, clientSecretPurpose}, []byte(input.ClientSecret))
					if e != nil {
						t.Fatal(e)
					}
					// Seed initial bounded versions in this disposable database. Existing
					// revision triggers remain enabled and are never bypassed by UPDATE.
					f.exec(t, f.f.Statement(`INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by,revision,auth_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,true,$10,$11,$12)`, `INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by,revision,auth_version) VALUES(?,?,?,?,?,?,?,?,?,true,?,?,?)`, input.ID, f.f.Tenant, input.Name, input.Provider, input.Origin, input.ClientID, sealed.keyID, sealed.nonce, sealed.ciphertext, f.f.User, versions[0], versions[1]))
					out, e := f.s.UpdateConnector(f.ctx, a, input.ID, UpdateConnector{Revision: versions[0], Name: input.Name, Reason: input.Reason}, key, uuid.NewString())
					code := "SSO_REVISION_CONFLICT"
					if versions[0] == identity.MaximumAuthVersion {
						code = "VALIDATION_FAILED"
					}
					if e == nil || e.Error() != code || out != (Connector{}) || connectorCounts(t, f, input.ID, key) != ([3]int{1, 0, 0}) {
						t.Fatal("exhausted connector authority/revision must not wrap or leave intent/audit", e)
					}
				}
				return
			}
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			if operation == "create_audit_failure" {
				input := connectorInput(f)
				f.accounts.Storage, f.accounts.DB = connectorAuditFaultStore{f.f.Store, 3}, nil
				out, e := f.s.CreateConnector(f.ctx, a, input, key, uuid.NewString())
				if e == nil || e.Error() != "SYNTHETIC_CONNECTOR_AUDIT_FAILURE" || out != (Connector{}) || connectorCounts(t, f, input.ID, key) != ([3]int{}) {
					t.Fatal("audit failure must roll back preceding real create and intent", e)
				}
				return
			}
			sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
			flow := connectorFlow(t, f, f.source.ConnectorID, "portal", "pending")
			f.accounts.Storage, f.accounts.DB = connectorAuditFaultStore{f.f.Store, 5}, nil
			out, e := f.s.UpdateConnector(f.ctx, a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Atomic disable", Reason: "Synthetic failure"}, key, uuid.NewString())
			if e == nil || e.Error() != "SYNTHETIC_CONNECTOR_AUDIT_FAILURE" || out != (Connector{}) || connectorCounts(t, f, f.source.ConnectorID, key) != ([3]int{1, 0, 0}) {
				t.Fatal("audit failure must roll back actual disable/revocation/flow/intent", e)
			}
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			var enabled, verifierPresent bool
			var revision, authVersion int64
			var state string
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT enabled,revision,auth_version FROM sso_connectors WHERE id=$1`, `SELECT enabled,revision,auth_version FROM sso_connectors WHERE id=?`, f.source.ConnectorID)).Scan(&enabled, &revision, &authVersion); e != nil || !enabled || revision != 1 || authVersion != 1 {
				t.Fatal("failed disable changed real connector", e)
			}
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,verifier_ciphertext IS NOT NULL FROM sso_flows WHERE id=$1`, `SELECT state,verifier_ciphertext IS NOT NULL FROM sso_flows WHERE id=?`, flow.FlowID)).Scan(&state, &verifierPresent); e != nil || state != "pending" || !verifierPresent {
				t.Fatal("failed disable destroyed real pending verifier", e)
			}
			assertConnectorAuth(t, f, sourced, true)
		})
	}
}

func TestActualSSOConnectorWaitingAuthorityCancellationAndSourceOrder(t *testing.T) {
	for _, target := range []string{"current_user_loss", "cancel_user_wait", "source_revocation_without_session_cycle"} {
		t.Run(target, func(t *testing.T) {
			f := catalogRepositoryFixture(t)
			a := catalogAuth(t, f, f.f.User, "admin", nil)
			if target == "source_revocation_without_session_cycle" {
				a = catalogAuth(t, f, f.f.User, "admin", &f.source)
			}
			ready, release, holderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			holderContext, stopHolder := context.WithTimeout(f.ctx, 5*time.Second)
			defer stopHolder()
			go func() {
				holderDone <- f.f.Store.Within(holderContext, false, func(ctx context.Context, q transaction.Executor) error {
					var id string
					query := f.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, f.f.User)
					if target == "source_revocation_without_session_cycle" {
						query = f.f.Statement(`SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, `SELECT id FROM sso_connectors WHERE tenant_id=? AND id=? FOR UPDATE`, f.f.Tenant, f.source.ConnectorID)
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
					if target == "cancel_user_wait" {
						return nil
					}
					if target == "source_revocation_without_session_cycle" {
						if _, e := q.Exec(ctx, f.f.Statement(`UPDATE sso_connectors SET enabled=false,auth_version=auth_version+1,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE sso_connectors SET enabled=false,auth_version=auth_version+1,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, f.source.ConnectorID)); e != nil {
							return e
						}
						_, e := q.Exec(ctx, f.f.Statement(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='sso_connector_changed' WHERE sso_tenant_id=$1 AND sso_connector_id=$2 AND revoked_at IS NULL`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='sso_connector_changed' WHERE sso_tenant_id=? AND sso_connector_id=? AND revoked_at IS NULL`, f.f.Tenant, f.source.ConnectorID))
						return e
					}
					_, e := q.Exec(ctx, f.f.Statement(`UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, f.f.User))
					return e
				})
			}()
			select {
			case <-ready:
			case e := <-holderDone:
				t.Fatal("actual connector holder failed", e)
			case <-time.After(2 * time.Second):
				t.Fatal("actual connector holder deadline")
			}
			observed := &catalogObservedStore{Store: f.f.Store, at: 1, ready: make(chan struct{}, 1)}
			if target == "source_revocation_without_session_cycle" {
				observed.at = 4 // user,plain source-session peek,installation peek,source row
			}
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			f.accounts.Storage, f.accounts.DB = observed, nil
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			writerContext, cancelWriter := context.WithCancel(f.ctx)
			defer cancelWriter()
			results := make(chan connectorOutcome, 1)
			key := uuid.NewString()
			go func() {
				value, e := f.s.UpdateConnector(writerContext, a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Waiting mutation", Enabled: true, Reason: "Synthetic waiting authority"}, key, uuid.NewString())
				results <- connectorOutcome{value, e}
			}()
			select {
			case <-observed.ready:
			case out := <-results:
				t.Fatal("writer returned before owned lock attempt", out.err)
			case <-time.After(2 * time.Second):
				t.Fatal("actual writer attempt deadline")
			}
			select {
			case out := <-results:
				t.Fatal("writer bypassed a held real row lock", out.err)
			case <-time.After(100 * time.Millisecond):
			}
			if target == "cancel_user_wait" {
				cancelWriter()
				out := connectorResult(t, results)
				if out.err == nil || !errors.Is(out.err, context.Canceled) || out.value != (Connector{}) {
					t.Fatal("cancelled locked writer leaked success", out.err)
				}
				unlock()
			} else {
				unlock()
				out := connectorResult(t, results)
				if out.err == nil || out.err.Error() != "AUTH_SESSION_EXPIRED" || out.value != (Connector{}) {
					t.Fatal("queued writer ignored current user/source revocation", out.err)
				}
			}
			select {
			case e := <-holderDone:
				if e != nil {
					t.Fatal("source bulk revocation must finish without waiting on writer session lock", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("actual holder/session lock cycle")
			}
			if connectorCounts(t, f, f.source.ConnectorID, key) != ([3]int{1, 0, 0}) {
				t.Fatal("failed queued write left intent/audit")
			}
			if target == "cancel_user_wait" {
				f.accounts.Storage, f.accounts.DB = originalStore, originalDB
				out, e := f.s.UpdateConnector(f.ctx, a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Recovered after cancellation", Enabled: true, Reason: "Synthetic independent recovery"}, uuid.NewString(), uuid.NewString())
				if e != nil || out.Revision != 2 || out.AuthVersion != 1 {
					t.Fatal("released connection could not perform fresh write", e)
				}
			}
		})
	}
}

func TestActualSSOConnectorCurrentRoleTenantAndForeignScope(t *testing.T) {
	for _, fault := range []string{"stored_admin_loss", "inactive_tenant", "foreign_target"} {
		t.Run(fault, func(t *testing.T) {
			f := catalogRepositoryFixture(t)
			a := catalogAuth(t, f, f.f.User, "admin", nil)
			target, code := f.source.ConnectorID, "AUTH_SESSION_EXPIRED"
			switch fault {
			case "stored_admin_loss":
				// The in-memory Auth still carries Admin and the same auth_epoch.
				f.exec(t, f.f.Statement(`UPDATE users SET is_platform_admin=false WHERE id=$1`, `UPDATE users SET is_platform_admin=false WHERE id=?`, f.f.User))
			case "inactive_tenant":
				f.exec(t, f.f.Statement(`UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, f.f.Tenant))
				code = "SSO_DISABLED"
			case "foreign_target":
				tenant := uuid.NewString()
				f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign write tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign write tenant','active')`, tenant))
				target = catalogSeedConnector(t, f, tenant, f.accounts.Config.SSO.Providers[0].Origin, "Foreign write connector", true, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))
				code = "SSO_CONNECTOR_NOT_FOUND"
			}
			before := catalogReadCounts(t, f)
			out, e := f.s.UpdateConnector(f.ctx, a, target, UpdateConnector{Revision: 1, Name: "Denied actual update", Enabled: true, Reason: "Synthetic authority fault"}, uuid.NewString(), uuid.NewString())
			if e == nil || e.Error() != code || out != (Connector{}) || catalogReadCounts(t, f) != before {
				t.Fatal("current role/tenant/foreign-scope denial returned data or left state", e)
			}
		})
	}
}

func TestActualSSOConnectorLateInstallationFence(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "admin", nil)
	tenant, workspace := uuid.NewString(), uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Replacement write tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Replacement write tenant','active')`, tenant))
	f.exec(t, f.f.Statement(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Replacement write Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Replacement write Workspace','active')`, workspace, tenant))
	ready, replace, holderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(replace) }) }
	defer unlock()
	holderContext, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	go func() {
		holderDone <- f.f.Store.Within(holderContext, false, func(ctx context.Context, q transaction.Executor) error {
			var id int
			if e := q.QueryRow(ctx, f.f.Statement(`SELECT id FROM installation_state WHERE id=1 FOR UPDATE`, `SELECT id FROM installation_state WHERE id=1 FOR UPDATE`)).Scan(&id); e != nil {
				return e
			}
			close(ready)
			select {
			case <-replace:
			case <-ctx.Done():
				return ctx.Err()
			}
			// Privileged disposable fixture fault,not a customer tenant-cutover API.
			_, e := q.Exec(ctx, f.f.Statement(`UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1`, `UPDATE installation_state SET tenant_id=?,default_workspace_id=? WHERE id=1`, tenant, workspace))
			return e
		})
	}()
	select {
	case <-ready:
	case e := <-holderDone:
		t.Fatal("installation holder", e)
	case <-time.After(2 * time.Second):
		t.Fatal("installation holder deadline")
	}
	observed := &catalogObservedStore{Store: f.f.Store, at: 4, ready: make(chan struct{}, 1)}
	originalStore, originalDB := f.accounts.Storage, f.accounts.DB
	f.accounts.Storage, f.accounts.DB = observed, nil
	defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
	input, key := connectorInput(f), uuid.NewString()
	results := make(chan connectorOutcome, 1)
	go func() {
		value, e := f.s.CreateConnector(f.ctx, a, input, key, uuid.NewString())
		results <- connectorOutcome{value, e}
	}()
	select {
	case <-observed.ready: // The nonlocking installation peek has completed.
	case out := <-results:
		t.Fatal("create completed before old-scope target lock", out.err)
	case <-time.After(2 * time.Second):
		t.Fatal("old-scope query attempt deadline")
	}
	unlock()
	out := connectorResult(t, results)
	if out.err == nil || out.err.Error() != "SSO_REVISION_CONFLICT" || out.value != (Connector{}) || connectorCounts(t, f, input.ID, key) != ([3]int{}) {
		t.Fatal("late installation replacement must not move a mutation into another tenant", out.err)
	}
	if e := <-holderDone; e != nil {
		t.Fatal("isolated installation replacement", e)
	}
	var count int
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT COUNT(*) FROM sso_connectors WHERE id=$1`, `SELECT COUNT(*) FROM sso_connectors WHERE id=?`, input.ID)).Scan(&count); e != nil || count != 0 {
		t.Fatal("late fence failure leaked a resource into either scope", e)
	}
}
