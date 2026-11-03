//go:build repository_integration

package sso

import (
	"context"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type callbackLockRole struct{}
type callbackLockStore struct {
	transaction.Store
	unlinkReady, claimReady, release, authenticated, begin chan struct{}
}
type callbackLockExecutor struct {
	transaction.Executor
	owner *callbackLockStore
	role  string
	rows  int
}
type callbackLockRow struct {
	transaction.Row
	ctx            context.Context
	ready, release chan struct{}
}

func (s *callbackLockStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	role, _ := ctx.Value(callbackLockRole{}).(string)
	if role == "claim" {
		select {
		case s.authenticated <- struct{}{}:
		default:
		}
		select {
		case <-s.begin:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &callbackLockExecutor{Executor: q, owner: s, role: role})
	})
}
func (q *callbackLockExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	q.rows++
	if q.role == "claim" && q.rows == 1 {
		select {
		case q.owner.claimReady <- struct{}{}:
		default:
		}
	}
	row := q.Executor.QueryRow(ctx, statement)
	if q.role == "unlink" && q.rows == 1 {
		return callbackLockRow{row, ctx, q.owner.unlinkReady, q.owner.release}
	}
	return row
}
func (r callbackLockRow) Scan(destinations ...any) error {
	if e := r.Row.Scan(destinations...); e != nil {
		return e
	}
	select {
	case r.ready <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
		return nil
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

// The observer forwards all rows/transactions to the actual database. It pauses
// only after unlink owns its real current user fence and observes claim's real
// initial user-fence attempt. No rows,authorities or commit are fabricated.
func TestActualSSOCallbackClaimVersusAccountUnlink(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	actor, previous := startActor(t, f, password, "portal", false)
	reason := "Synthetic overlapping claim and unlink"
	start, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", previous, &actor, StartInput{Mode: ModeLink, Password: &password, Reason: &reason}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	observed := &callbackLockStore{Store: f.f.Store, unlinkReady: make(chan struct{}, 1), claimReady: make(chan struct{}, 1), release: make(chan struct{}), authenticated: make(chan struct{}, 1), begin: make(chan struct{})}
	originalStore, originalDB := f.accounts.Storage, f.accounts.DB
	f.accounts.Storage, f.accounts.DB = observed, nil
	defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
	var once sync.Once
	release := func() { once.Do(func() { close(observed.release) }) }
	defer release()
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
		authorization, _ := url.Parse(start.AuthorizationURL)
		out, e := f.s.claim(context.WithValue(ctx, callbackLockRole{}, "claim"), f.source.ConnectorID, "portal", authorization.Query().Get("state"), start.BrowserToken, previous)
		claims <- struct {
			flow claimedFlow
			err  error
		}{out, e}
	}()
	select {
	case <-observed.authenticated:
	case out := <-claims:
		t.Fatal("claim failed before authenticated transaction boundary", out.err)
	case <-ctx.Done():
		t.Fatal("claim authentication boundary deadline")
	}
	unlink := make(chan bindingOutcome, 1)
	go func() {
		out, e := f.s.Unlink(context.WithValue(ctx, callbackLockRole{}, "unlink"), actor, f.source.BindingID, UnlinkBinding{Revision: 1, Password: password, Reason: reason}, uuid.NewString(), uuid.NewString())
		unlink <- bindingOutcome{out, e}
	}()
	select {
	case <-observed.unlinkReady:
	case out := <-unlink:
		t.Fatal("unlink ended before initial user fence", out.err)
	case <-ctx.Done():
		t.Fatal("unlink user fence deadline")
	}
	begin()
	select {
	case <-observed.claimReady:
	case out := <-claims:
		t.Fatal("claim ended before initial user-fence attempt", out.err)
	case <-ctx.Done():
		t.Fatal("claim session attempt deadline")
	}
	select {
	case out := <-claims:
		t.Fatal("claim bypassed held actual current session", out.err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	unlinked := bindingResult(t, unlink)
	select {
	case out := <-claims:
		if out.err == nil || out.err.Error() != "SSO_FLOW_UNUSABLE" || !reflect.DeepEqual(out.flow, claimedFlow{}) {
			t.Fatal("waiting claim must observe invalidated flow without credentials", out.err)
		}
	case <-ctx.Done():
		t.Fatal("claim/unlink lock cycle deadline")
	}
	if unlinked.err != nil || unlinked.value.Active || unlinked.value.Revision != 2 {
		t.Fatal("unlink must complete without a claim/session cycle", unlinked.err)
	}
}
