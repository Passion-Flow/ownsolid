//go:build cgo && odbc_manager && dameng_sso_integration

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
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Called by the exclusive native fixture after bootstrap. All statements use the
// actual admitted driver/transactions. Gates pause execution; they never replace
// a successful SQL result, inject a successful commit or inspect source text.
func damengSSOLockOrderFixture(t *testing.T, parent context.Context, store transaction.Store, base *Service, admin identity.Auth, template Connector) {
	t.Helper()
	accounts := *base.Identity
	accounts.Storage = store
	s, e := New(&accounts)
	if e != nil {
		t.Fatal("native lock fixture startup", e)
	}
	defer s.Close()
	accounts.Sources = s
	must := func(t *testing.T, e error) {
		t.Helper()
		if e != nil {
			t.Fatal("native lock fixture", e)
		}
	}
	connector := func(t *testing.T) Connector {
		t.Helper()
		c, e := s.CreateConnector(parent, admin, CreateConnector{ID: uuid.NewString(), Name: "Lock fixture", Provider: ProviderGitea, Origin: template.Origin, ClientID: uuid.NewString(), ClientSecret: "synthetic-lock-" + uuid.NewString(), Enabled: true, Reason: "Synthetic lock-order fixture"}, uuid.NewString(), uuid.NewString())
		must(t, e)
		return c
	}
	bind := func(t *testing.T, c Connector) {
		t.Helper()
		must(t, store.Within(parent, false, func(ctx context.Context, q transaction.Executor) error {
			var tenant string
			if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT TENANT_ID FROM INSTALLATION_STATE WHERE ID=1")).Scan(&tenant); e != nil {
				return e
			}
			n, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "INSERT INTO SSO_BINDINGS(ID,TENANT_ID,CONNECTOR_ID,SUBJECT,USER_ID) VALUES(?,?,?,?,?)", uuid.NewString(), tenant, c.ID, "901", admin.Me.User.ID))
			if e == nil && n != 1 {
				return errors.New("unconfirmed synthetic binding")
			}
			return e
		}))
	}
	local := func(t *testing.T) (identity.Auth, string) {
		t.Helper()
		var issued identity.IssuedSession
		must(t, store.Within(parent, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = accounts.IssueSessionWithin(ctx, q, admin.Me.User.ID, admin.Epoch, "portal", "", uuid.NewString(), nil)
			return e
		}))
		a, e := accounts.Authenticate(parent, issued.Token, "portal")
		must(t, e)
		return a, issued.Token
	}
	start := func(t *testing.T, c Connector, previous string) (StartResult, string) {
		t.Helper()
		v, e := s.Start(parent, c.ID, "portal", previous, nil, StartInput{Mode: ModeLogin}, uuid.NewString())
		must(t, e)
		u, e := url.Parse(v.AuthorizationURL)
		must(t, e)
		return v, u.Query().Get("state")
	}
	sourced := func(t *testing.T, c Connector) (identity.Auth, string) {
		t.Helper()
		bind(t, c)
		v, state := start(t, c, "")
		f, e := s.claim(parent, c.ID, "portal", state, v.BrowserToken, "")
		must(t, e)
		issued, e := s.commitFlow(parent, f, "901", "", uuid.NewString())
		must(t, e)
		a, e := accounts.Authenticate(parent, issued.Token, "portal")
		must(t, e)
		return a, issued.Token
	}

	t.Run("ordinary_recheck_allows_native_bulk_revocation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		c := connector(t)
		a, _ := sourced(t, c)
		entering := make(chan struct{})
		accounts.Sources = &nativeSSOSourceGate{SourceGuard: s, entering: entering}
		t.Cleanup(func() { accounts.Sources = s })
		held, result := nativeSSORevoker(ctx, store, c.ID, a.SessionID, entering)
		nativeSSOJoinOnCleanup(t, cancel, result)
		must(t, nativeSSOAwait(ctx, held))
		e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error { return accounts.RecheckWithin(ctx, q, a) })
		revocation := nativeSSOJoin(ctx, result)
		if revocation != nil || e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("connector/session lock cycle or stale authority", revocation, e)
		}
	})

	t.Run("claim_waits_for_previous_user_before_completion", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		first, second := connector(t), connector(t)
		bind(t, second)
		a, previous := local(t)
		pending, state := start(t, first, previous)
		completing, completingState := start(t, second, previous)
		f, e := s.claim(parent, second.ID, "portal", completingState, completing.BrowserToken, previous)
		must(t, e)
		claimReady, startClaim, installationHeld, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		firstLock := make(chan bool, 1)
		gate := &nativeSSOGateStore{Store: store}
		gate.before = func(ctx context.Context) error {
			if ctx.Value(nativeSSORole{}) == "claim" {
				close(claimReady)
				return nativeSSOAwait(ctx, startClaim)
			}
			return nil
		}
		gate.wrap = func(ctx context.Context, q transaction.Executor) transaction.Executor {
			switch ctx.Value(nativeSSORole{}) {
			case "complete":
				gate := &nativeSSOGateExecutor{Executor: q, observedSession: a.SessionID, match: transaction.SQL(transaction.Dameng, "SELECT TENANT_ID,BOOTSTRAP_COMPLETE FROM INSTALLATION_STATE WHERE ID=1 FOR UPDATE OF ID")}
				gate.after = func() error {
					close(installationHeld)
					if e := nativeSSOAwait(ctx, finish); e != nil {
						return e
					}
					if !gate.sessionSeen {
						return errors.New("completion locked installation before exact previous session")
					}
					return nil
				}
				return gate
			case "claim":
				return &nativeSSOGateExecutor{Executor: q, first: true, before: func(statement transaction.Statement) error {
					ordered := reflect.DeepEqual(statement, transaction.SQL(transaction.Dameng, "SELECT ID FROM USERS WHERE ID=? FOR UPDATE", a.Me.User.ID))
					firstLock <- ordered
					if !ordered {
						return errors.New("claim acquired connector/session before previous user")
					}
					return nil
				}}
			}
			return q
		}
		accounts.Storage = gate
		t.Cleanup(func() { accounts.Storage = store })
		claimResult := nativeSSORun(func() error {
			v, e := s.claim(context.WithValue(ctx, nativeSSORole{}, "claim"), first.ID, "portal", state, pending.BrowserToken, previous)
			if !reflect.DeepEqual(v, claimedFlow{}) {
				e = errors.New("losing claim released credentials")
			}
			return e
		})
		nativeSSOJoinOnCleanup(t, cancel, claimResult)
		must(t, nativeSSOAwait(ctx, claimReady))
		var issued identity.IssuedSession
		completionResult := nativeSSORun(func() error {
			var e error
			issued, e = s.commitFlow(context.WithValue(ctx, nativeSSORole{}, "complete"), f, "901", previous, uuid.NewString())
			return e
		})
		nativeSSOJoinOnCleanup(t, cancel, completionResult)
		must(t, nativeSSOAwait(ctx, installationHeld))
		close(startClaim)
		ordered := false
		select {
		case ordered = <-firstLock:
		case <-ctx.Done():
		}
		close(finish)
		completionError, claimError := nativeSSOJoin(ctx, completionResult), nativeSSOJoin(ctx, claimResult)
		if !ordered || completionError != nil || claimError == nil || claimError.Error() != "SSO_BROWSER_CHANGED" {
			t.Fatal("claim/completion ordering failed", ordered, completionError, claimError)
		}
		// Independent reads establish the losing flow is still pending, the old
		// browser is revoked and the winner actually issued an ordinary session.
		var pendingCount int
		must(t, store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM SSO_FLOWS WHERE CONNECTOR_ID=? AND STATE='pending' AND VERIFIER_CIPHERTEXT IS NOT NULL", first.ID)).Scan(&pendingCount))
		if pendingCount != 1 {
			t.Fatal("losing claim changed durable flow authority")
		}
		if _, e := accounts.Authenticate(ctx, previous, "portal"); e == nil {
			t.Fatal("winner did not rotate browser session")
		}
		if _, e := accounts.Authenticate(ctx, issued.Token, "portal"); e != nil {
			t.Fatal("winner session unavailable", e)
		}
	})

	t.Run("claim_locks_different_source_before_browser_session", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		source, target := connector(t), connector(t)
		// Force the source to sort after the target, exercising the full set.
		if source.ID < target.ID {
			source, target = target, source
		}
		a, previous := sourced(t, source)
		pending, state := start(t, target, previous)
		claimReady, startClaim, enteringSource := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var tenant string
		must(t, store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT TENANT_ID FROM INSTALLATION_STATE WHERE ID=1")).Scan(&tenant))
		gate := &nativeSSOGateStore{Store: store, before: func(ctx context.Context) error { close(claimReady); return nativeSSOAwait(ctx, startClaim) }}
		gate.wrap = func(ctx context.Context, q transaction.Executor) transaction.Executor {
			return &nativeSSOGateExecutor{Executor: q, match: transaction.SQL(transaction.Dameng, "SELECT ID FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=? FOR UPDATE OF ID", tenant, source.ID), before: func(transaction.Statement) error { close(enteringSource); return nil }}
		}
		accounts.Storage = gate
		t.Cleanup(func() { accounts.Storage = store })
		claimResult := nativeSSORun(func() error {
			v, e := s.claim(ctx, target.ID, "portal", state, pending.BrowserToken, previous)
			if !reflect.DeepEqual(v, claimedFlow{}) {
				e = errors.New("revoked claim released credentials")
			}
			return e
		})
		nativeSSOJoinOnCleanup(t, cancel, claimResult)
		must(t, nativeSSOAwait(ctx, claimReady))
		held, revocationResult := nativeSSORevoker(ctx, store, source.ID, a.SessionID, enteringSource)
		nativeSSOJoinOnCleanup(t, cancel, revocationResult)
		must(t, nativeSSOAwait(ctx, held))
		close(startClaim)
		revocationError, claimError := nativeSSOJoin(ctx, revocationResult), nativeSSOJoin(ctx, claimResult)
		if revocationError != nil || claimError == nil || claimError.Error() != "SSO_BROWSER_CHANGED" {
			t.Fatal("cross-connector claim/revocation ordering failed", revocationError, claimError)
		}
		var pendingCount int
		must(t, store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM SSO_FLOWS WHERE CONNECTOR_ID=? AND STATE='pending' AND VERIFIER_CIPHERTEXT IS NOT NULL", target.ID)).Scan(&pendingCount))
		if pendingCount != 1 {
			t.Fatal("revoked claim changed its target flow")
		}
		if _, e := accounts.Authenticate(ctx, previous, "portal"); e == nil {
			t.Fatal("source revocation left previous browser usable")
		}
	})
}

type nativeSSORole struct{}
type nativeSSOGateStore struct {
	transaction.Store
	before func(context.Context) error
	wrap   func(context.Context, transaction.Executor) transaction.Executor
}

func (s *nativeSSOGateStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	if s.before != nil {
		if e := s.before(ctx); e != nil {
			return e
		}
	}
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error { return fn(ctx, s.wrap(ctx, q)) })
}

type nativeSSOGateExecutor struct {
	transaction.Executor
	match           transaction.Statement
	first, used     bool
	observedSession string
	sessionSeen     bool
	before          func(transaction.Statement) error
	after           func() error
}

func (q *nativeSSOGateExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	if !q.used && (q.first || reflect.DeepEqual(statement, q.match)) {
		q.used = true
		if q.before != nil {
			if e := q.before(statement); e != nil {
				return nativeSSOGateRow{err: e}
			}
		}
		return nativeSSOGateRow{Row: q.Executor.QueryRow(ctx, statement), observe: q.observeSession, after: q.after}
	}
	return nativeSSOGateRow{Row: q.Executor.QueryRow(ctx, statement), observe: q.observeSession}
}
func (q *nativeSSOGateExecutor) observeSession(dest []any) {
	if q.observedSession != "" && len(dest) == 1 {
		if id, ok := dest[0].(*string); ok && *id == q.observedSession {
			q.sessionSeen = true
		}
	}
}

type nativeSSOGateRow struct {
	transaction.Row
	err     error
	observe func([]any)
	after   func() error
}

func (r nativeSSOGateRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if e := r.Row.Scan(dest...); e != nil {
		return e
	}
	if r.observe != nil {
		r.observe(dest)
	}
	if r.after != nil {
		return r.after()
	}
	return nil
}

type nativeSSOSourceGate struct {
	identity.SourceGuard
	entering chan struct{}
	once     sync.Once
}

func (g *nativeSSOSourceGate) CheckSessionSourceWithin(ctx context.Context, q transaction.Executor, source identity.SessionSource, user string) error {
	g.once.Do(func() { close(g.entering) })
	return g.SourceGuard.CheckSessionSourceWithin(ctx, q, source, user)
}
func nativeSSOAwait(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type nativeSSOTask struct {
	done   chan struct{}
	result error
}

func nativeSSORun(fn func() error) *nativeSSOTask {
	task := &nativeSSOTask{done: make(chan struct{})}
	go func() { defer close(task.done); task.result = fn() }()
	return task
}
func nativeSSOJoin(ctx context.Context, task *nativeSSOTask) error {
	select {
	case <-task.done:
		return task.result
	case <-ctx.Done():
		select {
		case <-task.done:
			return task.result
		case <-time.After(20 * time.Second):
			return errors.New("native worker did not join; retain fixture and recovery state")
		}
	}
}
func nativeSSOJoinOnCleanup(t *testing.T, cancel context.CancelFunc, task *nativeSSOTask) {
	t.Helper()
	t.Cleanup(func() {
		cancel()
		select {
		case <-task.done:
		case <-time.After(20 * time.Second):
			t.Error("native worker still active; retain fixture and recovery state")
		}
	})
}

// Mirrors the production admin revoker's connector -> installation -> bulk
// session order, without taking the affected user's lock (the documented rule).
// It uses real DDL guards and exactly scoped synthetic connector/session rows.
func nativeSSORevoker(ctx context.Context, store transaction.Store, connector, session string, proceed <-chan struct{}) (<-chan struct{}, *nativeSSOTask) {
	held := make(chan struct{})
	result := nativeSSORun(func() error {
		return store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT ID FROM SSO_CONNECTORS WHERE ID=? FOR UPDATE OF ID", connector)).Scan(&id); e != nil {
				return e
			}
			if _, e := connectorTenant(ctx, q, connectorReadFence); e != nil {
				return e
			}
			close(held)
			if e := nativeSSOAwait(ctx, proceed); e != nil {
				return e
			}
			n, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE SSO_CONNECTORS SET ENABLED=0,REVISION=REVISION+1,AUTH_VERSION=AUTH_VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?", connector))
			if e != nil {
				return e
			}
			if n != 1 {
				return errors.New("unconfirmed connector revocation")
			}
			n, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON='sso_connector_changed' WHERE ID=? AND SSO_CONNECTOR_ID=? AND REVOKED_AT IS NULL", session, connector))
			if e == nil && n != 1 {
				return errors.New("unconfirmed session revocation")
			}
			return e
		})
	})
	return held, result
}
