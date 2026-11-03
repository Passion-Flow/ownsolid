//go:build repository_integration

package sso

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func maintenanceFixture(t *testing.T, batch, days int) sourceRepositoryFixture {
	t.Helper()
	f := catalogRepositoryFixture(t)
	policy := f.accounts.Config.Policy()
	policy.SSO.CleanupBatchSize, policy.SSO.FlowRetentionDays = batch, days
	f.accounts.Config.Runtime = &policy
	service, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	f.s, f.accounts.Sources = service, service
	return f
}

func maintenanceNow(t *testing.T, f sourceRepositoryFixture) time.Time {
	t.Helper()
	var now time.Time
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT clock_timestamp()`, `SELECT UTC_TIMESTAMP(6)`)).Scan(&now); e != nil {
		t.Fatal(e)
	}
	return now.UTC().Truncate(time.Microsecond)
}

// Privileged durable prior-state inputs are synthetic. Encryption,SQL constraints,
// maintenance and session issuance below use the real implementation and engine.
func maintenanceFlow(t *testing.T, f sourceRepositoryFixture, state string, created, expiry, finished time.Time) string {
	t.Helper()
	id := uuid.NewString()
	sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, f.source.ConnectorID, id, flowVerifierPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	// Both actual databases enforce pending as the only initial state. Historical
	// timestamps are declared prior-state inputs;all transitions retain guards.
	f.exec(t, f.f.Statement(
		`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES($1,$2,$3,1,1,'portal','login',$4,$5,$6,$7,$8,$9,$10,$11)`,
		`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,1,1,'portal','login',?,?,?,?,?,?,?,?)`,
		id, f.f.Tenant, f.source.ConnectorID, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest("anonymous"), sealed.keyID, sealed.nonce, sealed.ciphertext, created, expiry))
	switch state {
	case flowPending:
	case flowClaimed, "succeeded":
		f.exec(t, f.f.Statement(`UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2`, `UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?`, uuid.NewString(), id))
		if state == "succeeded" {
			f.exec(t, f.f.Statement(`UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=$2 WHERE id=$3`, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=? WHERE id=?`, f.f.User, finished, id))
		}
	case "failed", "expired":
		code := "SSO_FLOW_UNUSABLE"
		if state == "expired" {
			code = "SSO_FLOW_EXPIRED"
		}
		f.exec(t, f.f.Statement(`UPDATE sso_flows SET state=$1,error_code=$2,finished_at=$3,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$4`, `UPDATE sso_flows SET state=?,error_code=?,finished_at=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?`, state, code, finished, id))
	default:
		t.Fatal("invalid synthetic durable flow state")
	}
	return id
}

func maintenanceAuditCount(t *testing.T, f sourceRepositoryFixture) int {
	t.Helper()
	var count int
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE action='sso.flow_cleanup' AND audience='system' AND outcome='success'`, `SELECT COUNT(*) FROM audit_events WHERE action='sso.flow_cleanup' AND audience='system' AND outcome='success'`)).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}

func maintenanceState(t *testing.T, f sourceRepositoryFixture, id string) (string, bool, string) {
	t.Helper()
	var state, code string
	var erased bool
	e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL),COALESCE(error_code,'') FROM sso_flows WHERE id=$1`, `SELECT state,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL),COALESCE(error_code,'') FROM sso_flows WHERE id=?`, id)).Scan(&state, &erased, &code)
	if errors.Is(e, sql.ErrNoRows) {
		return "absent", false, ""
	}
	if e != nil {
		t.Fatal(e)
	}
	return state, erased, code
}

func maintenanceAssert(t *testing.T, f sourceRepositoryFixture, id, want string) {
	t.Helper()
	state, erased, code := maintenanceState(t, f, id)
	if state != want || want == "expired" && (!erased || code != "SSO_FLOW_EXPIRED") || want == flowPending && erased {
		t.Fatal("actual maintenance state/verifier/error mismatch", state, want)
	}
}

func maintenanceReference(t *testing.T, f sourceRepositoryFixture, flow string, revoke bool) identity.IssuedSession {
	t.Helper()
	source := f.source
	source.FlowID = flow
	var issued identity.IssuedSession
	e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var epoch int64
		if e := q.QueryRow(ctx, f.f.Statement(`SELECT auth_epoch FROM users WHERE id=$1`, `SELECT auth_epoch FROM users WHERE id=?`, f.f.User)).Scan(&epoch); e != nil {
			return e
		}
		var e error
		issued, e = f.accounts.IssueSessionWithin(ctx, q, f.f.User, epoch, "portal", "", uuid.NewString(), &source)
		return e
	})
	if e != nil {
		t.Fatal("actual retained source-session reference", e)
	}
	if revoke {
		if e := f.accounts.Logout(f.ctx, issued.Token, "portal", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	return issued
}

func TestActualSSOMaintenanceBoundedExpiryRetentionAndAudit(t *testing.T) {
	f := maintenanceFixture(t, 2, 3)
	now := maintenanceNow(t, f)
	var pending, terminal []string
	for i := range 3 {
		state := flowPending
		if i == 1 {
			state = flowClaimed
		}
		pending = append(pending, maintenanceFlow(t, f, state, now.Add(-4*time.Hour), now.Add(time.Duration(i-3)*time.Hour), time.Time{}))
	}
	for i := range 3 {
		terminal = append(terminal, maintenanceFlow(t, f, "failed", now.Add(-12*maintenanceRetentionDay), now.Add(-11*maintenanceRetentionDay), now.Add(-time.Duration(10-i)*maintenanceRetentionDay)))
	}
	active := maintenanceFlow(t, f, flowPending, now, now.Add(time.Hour), time.Time{})
	fresh := maintenanceFlow(t, f, "failed", now.Add(-2*time.Hour), now.Add(-time.Hour), now.Add(-time.Hour))
	referenced := maintenanceFlow(t, f, "succeeded", now.Add(-12*maintenanceRetentionDay), now.Add(-11*maintenanceRetentionDay), now.Add(-10*maintenanceRetentionDay))
	maintenanceReference(t, f, referenced, true)
	if e := f.s.Cleanup(f.ctx); e != nil {
		t.Fatal(e)
	}
	for _, id := range pending[:2] {
		maintenanceAssert(t, f, id, "expired")
	}
	maintenanceAssert(t, f, pending[2], flowPending)
	for _, id := range terminal[:2] {
		maintenanceAssert(t, f, id, "absent")
	}
	maintenanceAssert(t, f, terminal[2], "failed")
	maintenanceAssert(t, f, active, flowPending)
	maintenanceAssert(t, f, fresh, "failed")
	maintenanceAssert(t, f, referenced, "succeeded")
	if maintenanceAuditCount(t, f) != 1 {
		t.Fatal("changed maintenance cycle must have one atomic system audit")
	}
	if e := f.s.Cleanup(f.ctx); e != nil {
		t.Fatal(e)
	}
	maintenanceAssert(t, f, pending[2], "expired")
	maintenanceAssert(t, f, terminal[2], "absent")
	if maintenanceAuditCount(t, f) != 2 {
		t.Fatal("second bounded cycle audit missing")
	}
	if e := f.s.Cleanup(f.ctx); e != nil {
		t.Fatal(e)
	}
	if maintenanceAuditCount(t, f) != 2 {
		t.Fatal("no-change maintenance duplicated audit")
	}
}

// This coordination wrapper returns the actual server timestamp unchanged and
// pauses before the next real SQL statement;it is not a clock or DB result double.
type maintenanceClockStore struct {
	transaction.Store
	ready   chan time.Time
	release <-chan struct{}
}
type maintenanceClockExecutor struct {
	transaction.Executor
	owner    *maintenanceClockStore
	observed bool
}
type maintenanceClockRow struct {
	transaction.Row
	owner *maintenanceClockStore
	ctx   context.Context
}

func (s *maintenanceClockStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &maintenanceClockExecutor{Executor: q, owner: s})
	})
}
func (q *maintenanceClockExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	row := q.Executor.QueryRow(ctx, statement)
	if q.observed {
		return row
	}
	q.observed = true
	return maintenanceClockRow{row, q.owner, ctx}
}
func (r maintenanceClockRow) Scan(dest ...any) error {
	if e := r.Row.Scan(dest...); e != nil {
		return e
	}
	stamp, ok := dest[0].(*time.Time)
	if !ok {
		return errors.New("IDENTIFIED_MAINTENANCE_CLOCK_OBSERVER_SHAPE")
	}
	r.owner.ready <- stamp.UTC().Truncate(time.Microsecond)
	select {
	case <-r.owner.release:
		return nil
	case <-r.ctx.Done():
		return &transaction.Error{Kind: transaction.Timeout}
	}
}

func TestActualSSOMaintenanceExactCapturedClockRetentionBoundary(t *testing.T) {
	f := maintenanceFixture(t, 2, 3)
	release := make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	observed := &maintenanceClockStore{Store: f.f.Store, ready: make(chan time.Time, 1), release: release}
	originalStore, originalDB := f.accounts.Storage, f.accounts.DB
	f.accounts.Storage, f.accounts.DB = observed, nil
	defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
	done := make(chan error, 1)
	go func() { done <- f.s.Cleanup(f.ctx) }()
	var stamp time.Time
	select {
	case stamp = <-observed.ready:
	case e := <-done:
		t.Fatal("actual maintenance clock boundary", e)
	case <-f.ctx.Done():
		t.Fatal("clock observation deadline")
	}
	cutoff := stamp.Add(-3 * maintenanceRetentionDay)
	equal := maintenanceFlow(t, f, "failed", cutoff.Add(-2*time.Hour), cutoff.Add(-time.Hour), cutoff)
	older := maintenanceFlow(t, f, "failed", cutoff.Add(-2*time.Hour), cutoff.Add(-time.Hour), cutoff.Add(-time.Microsecond))
	unlock()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-f.ctx.Done():
		t.Fatal("captured clock cleanup deadline")
	}
	maintenanceAssert(t, f, equal, "failed")
	maintenanceAssert(t, f, older, "absent")
	if maintenanceAuditCount(t, f) != 1 {
		t.Fatal("strict captured clock delete audit")
	}
}

func maintenanceHold(t *testing.T, f sourceRepositoryFixture, statement transaction.Statement) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
	t.Cleanup(cancel)
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, statement).Scan(&id); e != nil {
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
	var once sync.Once
	unlock := func() {
		once.Do(func() {
			close(release)
			select {
			case e := <-done:
				if e != nil {
					t.Error("actual maintenance holder cleanup", e)
				}
			case <-ctx.Done():
				t.Error("maintenance holder cleanup deadline")
			}
		})
	}
	t.Cleanup(unlock)
	select {
	case <-ready:
	case e := <-done:
		t.Fatal("actual maintenance holder", e)
	case <-ctx.Done():
		t.Fatal("maintenance holder startup deadline")
	}
	return unlock
}

func TestActualSSOMaintenanceSkipLockedAndConcurrent(t *testing.T) {
	for _, kind := range []string{"expired_row", "terminal_row", "referenced_session"} {
		t.Run(kind, func(t *testing.T) {
			f := maintenanceFixture(t, 1, 3)
			now := maintenanceNow(t, f)
			state, created, expiry, finished := flowPending, now.Add(-3*time.Hour), now.Add(-2*time.Hour), time.Time{}
			if kind != "expired_row" {
				state, created, expiry, finished = "failed", now.Add(-12*maintenanceRetentionDay), now.Add(-11*maintenanceRetentionDay), now.Add(-10*maintenanceRetentionDay)
			}
			if kind == "referenced_session" {
				state = "succeeded"
			}
			held := maintenanceFlow(t, f, state, created, expiry, finished)
			other := maintenanceFlow(t, f, state, created, expiry.Add(time.Minute), finished)
			statement := f.f.Statement(`SELECT id::text FROM sso_flows WHERE id=$1 FOR UPDATE`, `SELECT id FROM sso_flows WHERE id=? FOR UPDATE`, held)
			if kind == "referenced_session" {
				issued := maintenanceReference(t, f, held, false)
				statement = f.f.Statement(`SELECT id::text FROM sessions WHERE id=$1 FOR UPDATE`, `SELECT id FROM sessions WHERE id=? FOR UPDATE`, issued.SessionID)
			}
			unlock := maintenanceHold(t, f, statement)
			ctx, cancel := context.WithTimeout(f.ctx, time.Second)
			defer cancel()
			if e := f.s.Cleanup(ctx); e != nil {
				t.Fatal("skip-locked/unrelated session work blocked", e)
			}
			want := "absent"
			if kind == "expired_row" {
				want = "expired"
			}
			maintenanceAssert(t, f, other, want)
			maintenanceAssert(t, f, held, state)
			if maintenanceAuditCount(t, f) != 1 {
				t.Fatal("unrelated eligible maintenance audit missing")
			}
			unlock()
			if e := f.s.Cleanup(f.ctx); e != nil {
				t.Fatal(e)
			}
			if kind == "referenced_session" {
				maintenanceAssert(t, f, held, "succeeded")
				if maintenanceAuditCount(t, f) != 1 {
					t.Fatal("held session reference produced duplicate cleanup")
				}
			} else {
				maintenanceAssert(t, f, held, want)
				if maintenanceAuditCount(t, f) != 2 {
					t.Fatal("released row fresh recovery missing")
				}
			}
		})
	}
	t.Run("concurrent_cycles", func(t *testing.T) {
		f := maintenanceFixture(t, 2, 3)
		now := maintenanceNow(t, f)
		var expired, terminal []string
		for range 4 {
			expired = append(expired, maintenanceFlow(t, f, flowPending, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{}))
			terminal = append(terminal, maintenanceFlow(t, f, "failed", now.Add(-12*maintenanceRetentionDay), now.Add(-11*maintenanceRetentionDay), now.Add(-10*maintenanceRetentionDay)))
		}
		release := make(chan struct{})
		var once sync.Once
		unlock := func() { once.Do(func() { close(release) }) }
		defer unlock()
		observed := &maintenanceClockStore{Store: f.f.Store, ready: make(chan time.Time, 2), release: release}
		originalStore, originalDB := f.accounts.Storage, f.accounts.DB
		f.accounts.Storage, f.accounts.DB = observed, nil
		defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
		done := make(chan error, 2)
		for range 2 {
			go func() { done <- f.s.Cleanup(f.ctx) }()
		}
		for range 2 {
			select {
			case <-observed.ready:
			case e := <-done:
				t.Fatal("two actual concurrent maintenance transactions", e)
			case <-f.ctx.Done():
				t.Fatal("concurrent clock boundary deadline")
			}
		}
		unlock()
		for range 2 {
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-f.ctx.Done():
				t.Fatal("concurrent maintenance deadline")
			}
		}
		f.accounts.Storage, f.accounts.DB = originalStore, originalDB
		// SKIP LOCKED is queue work,not a promise that both transactions fill
		// their batches. MySQL may lock rows for sorting before applying LIMIT.
		// Independently read the bounded outcome,then recover remaining work.
		expiredCount, deletedCount := 0, 0
		for _, id := range expired {
			state, _, _ := maintenanceState(t, f, id)
			if state == "expired" {
				expiredCount++
			} else if state != flowPending {
				t.Fatal("concurrent expiry changed a flow to an invalid state")
			}
		}
		for _, id := range terminal {
			state, _, _ := maintenanceState(t, f, id)
			if state == "absent" {
				deletedCount++
			} else if state != "failed" {
				t.Fatal("concurrent retention changed a terminal state")
			}
		}
		audits := maintenanceAuditCount(t, f)
		if expiredCount < 2 || deletedCount < 2 || audits < 1 || audits > 2 {
			t.Fatal("concurrent bounded work lost progress or duplicated audit")
		}
		for range 2 {
			before := maintenanceAuditCount(t, f)
			if e := f.s.Cleanup(f.ctx); e != nil {
				t.Fatal("fresh recovery after concurrent skipped rows", e)
			}
			after := maintenanceAuditCount(t, f)
			if after < before || after > before+1 {
				t.Fatal("one recovery cycle duplicated audit")
			}
		}
		for _, id := range expired {
			maintenanceAssert(t, f, id, "expired")
		}
		for _, id := range terminal {
			maintenanceAssert(t, f, id, "absent")
		}
		before := maintenanceAuditCount(t, f)
		if e := f.s.Cleanup(f.ctx); e != nil || maintenanceAuditCount(t, f) != before {
			t.Fatal("completed concurrent recovery duplicated no-change audit", e)
		}
		t.Logf("actual concurrent initial_expired=%d initial_deleted=%d initial_audits=%d all_remaining_recovered=1 no_change_audit=0", expiredCount, deletedCount, audits)
	})
}

func TestActualSSOMaintenanceAtomicSQLAndAuditRollback(t *testing.T) {
	for index, name := range []string{"first_expiry_write", "terminal_delete", "last_cleanup_audit"} {
		t.Run(name, func(t *testing.T) {
			f := maintenanceFixture(t, 2, 3)
			now := maintenanceNow(t, f)
			expired := maintenanceFlow(t, f, flowPending, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{})
			terminal := maintenanceFlow(t, f, "failed", now.Add(-12*maintenanceRetentionDay), now.Add(-11*maintenanceRetentionDay), now.Add(-10*maintenanceRetentionDay))
			// Identified test-only fault delegates all earlier SQL to the real DB.
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			f.accounts.Storage, f.accounts.DB = connectorAuditFaultStore{Store: f.f.Store, failAt: index + 1}, nil
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			if e := f.s.Cleanup(f.ctx); e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
				t.Fatal("maintenance SQL/audit failure hidden", e)
			}
			maintenanceAssert(t, f, expired, flowPending)
			maintenanceAssert(t, f, terminal, "failed")
			if maintenanceAuditCount(t, f) != 0 {
				t.Fatal("maintenance audit escaped rollback")
			}
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			if e := f.s.Cleanup(f.ctx); e != nil {
				t.Fatal("fresh actual recovery", e)
			}
			maintenanceAssert(t, f, expired, "expired")
			maintenanceAssert(t, f, terminal, "absent")
			if maintenanceAuditCount(t, f) != 1 {
				t.Fatal("recovered maintenance audit missing")
			}
		})
	}
}

type maintenanceAuditObserveStore struct {
	transaction.Store
	ready chan struct{}
}
type maintenanceAuditObserveExecutor struct {
	transaction.Executor
	ready  chan struct{}
	writes int
}

func (s maintenanceAuditObserveStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &maintenanceAuditObserveExecutor{Executor: q, ready: s.ready})
	})
}
func (q *maintenanceAuditObserveExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	q.writes++
	if q.writes == 3 {
		q.ready <- struct{}{}
	}
	return q.Executor.Exec(ctx, statement)
}

func TestActualSSOMaintenanceCancellationRollsBackAndRecovers(t *testing.T) {
	f := maintenanceFixture(t, 2, 3)
	now := maintenanceNow(t, f)
	expired := maintenanceFlow(t, f, flowPending, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{})
	terminal := maintenanceFlow(t, f, "failed", now.Add(-12*maintenanceRetentionDay), now.Add(-11*maintenanceRetentionDay), now.Add(-10*maintenanceRetentionDay))
	unlock := maintenanceHold(t, f, f.f.Statement(`SELECT id::text FROM tenants WHERE id=$1 FOR UPDATE`, `SELECT id FROM tenants WHERE id=? FOR UPDATE`, f.f.Tenant))
	originalStore, originalDB := f.accounts.Storage, f.accounts.DB
	ready := make(chan struct{}, 1)
	f.accounts.Storage, f.accounts.DB = maintenanceAuditObserveStore{Store: f.f.Store, ready: ready}, nil
	defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
	ctx, cancel := context.WithTimeout(f.ctx, 750*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.s.Cleanup(ctx) }()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal("actual expiry/delete did not reach held tenant audit FK", e)
	case <-f.ctx.Done():
		t.Fatal("audit observation deadline")
	}
	select {
	case e := <-done:
		if e == nil || e.Error() != string(transaction.Timeout) {
			t.Fatal("actual held-audit cancellation outcome", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance cancellation deadline")
	}
	maintenanceAssert(t, f, expired, flowPending)
	maintenanceAssert(t, f, terminal, "failed")
	if maintenanceAuditCount(t, f) != 0 {
		t.Fatal("cancelled maintenance committed audit")
	}
	unlock()
	f.accounts.Storage, f.accounts.DB = originalStore, originalDB
	if e := f.s.Cleanup(f.ctx); e != nil {
		t.Fatal(e)
	}
	maintenanceAssert(t, f, expired, "expired")
	maintenanceAssert(t, f, terminal, "absent")
	if maintenanceAuditCount(t, f) != 1 {
		t.Fatal("fresh maintenance recovery after actual cancellation")
	}
}

func TestActualSSOMaintenanceDisabledKeylessRetainedTenantScope(t *testing.T) {
	f := maintenanceFixture(t, 2, 3)
	now := maintenanceNow(t, f)
	own := maintenanceFlow(t, f, flowPending, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{})
	otherTenant := uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Retained synthetic tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Retained synthetic tenant','active')`, otherTenant))
	connector := catalogSeedConnector(t, f, otherTenant, f.accounts.Config.SSO.Providers[0].Origin, "Retained foreign maintenance source", true, now)
	foreign := f
	foreign.f.Tenant = otherTenant
	foreign.source.TenantID, foreign.source.ConnectorID = otherTenant, connector
	retained := maintenanceFlow(t, foreign, flowPending, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{})
	f.accounts.Config.SSO.Enabled, f.accounts.Config.SSO.KeyringFile = false, "/missing-synthetic-maintenance-keyring"
	recovery, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer recovery.Close()
	f.accounts.Sources = recovery
	if recovery.keys != nil || len(recovery.clients) != 0 {
		t.Fatal("disabled maintenance opened key/provider state")
	}
	if e := recovery.Cleanup(f.ctx); e != nil {
		t.Fatal("disabled retained-scope cleanup", e)
	}
	maintenanceAssert(t, f, own, "expired")
	maintenanceAssert(t, foreign, retained, "expired")
	if maintenanceAuditCount(t, f) != 1 {
		t.Fatal("global trusted cleanup lost atomic system audit")
	}
	var tenant string
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT tenant_id::text FROM audit_events WHERE action='sso.flow_cleanup'`, `SELECT tenant_id FROM audit_events WHERE action='sso.flow_cleanup'`)).Scan(&tenant); e != nil || tenant != f.f.Tenant {
		t.Fatal("maintenance audit escaped current installation scope", e)
	}
}

func TestActualSSOMaintenanceBorrowedInvalidationAndCallerRollback(t *testing.T) {
	f := maintenanceFixture(t, 2, 3)
	now := maintenanceNow(t, f)
	pending := maintenanceFlow(t, f, flowPending, now, now.Add(time.Hour), time.Time{})
	claimed := maintenanceFlow(t, f, flowClaimed, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{})
	terminal := maintenanceFlow(t, f, "expired", now.Add(-4*time.Hour), now.Add(-3*time.Hour), now.Add(-3*time.Hour))
	before := connectorCipher(t, f, f.source.ConnectorID)
	var claim string
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT claim_id::text FROM sso_flows WHERE id=$1`, `SELECT claim_id FROM sso_flows WHERE id=?`, claimed)).Scan(&claim); e != nil {
		t.Fatal(e)
	}
	abort := errors.New("IDENTIFIED_CALLER_RESTORE_ROLLBACK")
	e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := InvalidateAuthoritiesWithin(ctx, q); e != nil {
			return e
		}
		return abort
	})
	if !errors.Is(e, abort) {
		t.Fatal("borrowed invalidation gained independent commit authority", e)
	}
	maintenanceAssert(t, f, pending, flowPending)
	maintenanceAssert(t, f, claimed, flowClaimed)
	maintenanceAssert(t, f, terminal, "expired")
	if e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q) }); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{pending, claimed} {
		state, erased, code := maintenanceState(t, f, id)
		if state != "failed" || !erased || code != "SSO_FLOW_UNUSABLE" {
			t.Fatal("borrowed restore did not invalidate actual pending/claimed verifier")
		}
	}
	var retainedClaim string
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT claim_id::text FROM sso_flows WHERE id=$1`, `SELECT claim_id FROM sso_flows WHERE id=?`, claimed)).Scan(&retainedClaim); e != nil || retainedClaim != claim {
		t.Fatal("restore rewrote immutable claim", e)
	}
	maintenanceAssert(t, f, terminal, "expired")
	maintenanceAssert(t, f, f.source.FlowID, "succeeded")
	after := connectorCipher(t, f, f.source.ConnectorID)
	if before.keyID != after.keyID || !bytes.Equal(before.nonce, after.nonce) || !bytes.Equal(before.ciphertext, after.ciphertext) || maintenanceAuditCount(t, f) != 0 {
		t.Fatal("borrowed invalidation changed connector crypto/history or created independent audit")
	}
	var bindings int
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT COUNT(*) FROM sso_bindings`, `SELECT COUNT(*) FROM sso_bindings`)).Scan(&bindings); e != nil || bindings != 1 {
		t.Fatal("restore destroyed retained binding history", e)
	}
}

func TestActualSSOMaintenanceConfiguredRunTickAndJoinedShutdown(t *testing.T) {
	f := maintenanceFixture(t, 1, 3)
	policy := f.accounts.Config.Policy()
	policy.SSO.CleanupSeconds = 10
	f.accounts.Config.Runtime = &policy
	service, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	now := maintenanceNow(t, f)
	id := maintenanceFlow(t, f, flowPending, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{})
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan struct{})
	reports := make(chan error, 1)
	go func() {
		service.Run(ctx, func(e error) {
			select {
			case reports <- e:
			default:
			}
		})
		close(done)
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(14 * time.Second)
	defer deadline.Stop()
	for {
		state, _, _ := maintenanceState(t, f, id)
		if state == "expired" {
			break
		}
		select {
		case <-ticker.C:
		case e := <-reports:
			t.Fatal("configured real maintenance tick failed", e)
		case <-deadline.C:
			t.Fatal("actual configured10s maintenance tick missing")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance shutdown did not join")
	}
	select {
	case e := <-reports:
		t.Fatal("normal shutdown reported a failure", e)
	default:
	}
	maintenanceAssert(t, f, id, "expired")
	if maintenanceAuditCount(t, f) != 1 {
		t.Fatal("configured actual tick audit missing or duplicated")
	}
}
