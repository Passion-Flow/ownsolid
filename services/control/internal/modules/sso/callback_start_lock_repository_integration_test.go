//go:build repository_integration

package sso

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type callbackStartLockStore struct {
	transaction.Store
	startReady, claimReady, release, authenticated, begin chan struct{}
}
type callbackStartLockExecutor struct {
	transaction.Executor
	owner *callbackStartLockStore
	role  string
	rows  int
}

func (s *callbackStartLockStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
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
		return fn(ctx, &callbackStartLockExecutor{Executor: q, owner: s, role: role})
	})
}
func (q *callbackStartLockExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	q.rows++
	if q.role == "claim" && q.rows == 1 {
		select {
		case q.owner.claimReady <- struct{}{}:
		default:
		}
	}
	row := q.Executor.QueryRow(ctx, statement)
	if q.role == "start" && q.rows == 1 {
		return callbackLockRow{row, ctx, q.owner.startReady, q.owner.release}
	}
	return row
}

// This regression also exercises R19's exclusive admission fence. Claim must
// wait for the same current user before connector, session, installation or flow locks.
func TestActualSSOCallbackClaimVersusCurrentStart(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	actor, previous := startActor(t, f, password, "portal", false)
	reason := "Synthetic overlapping start and claim"
	input := StartInput{Mode: ModeLink, Password: &password, Reason: &reason}
	first, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", previous, &actor, input, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	observed := &callbackStartLockStore{Store: f.f.Store, startReady: make(chan struct{}, 1), claimReady: make(chan struct{}, 1), release: make(chan struct{}), authenticated: make(chan struct{}, 1), begin: make(chan struct{})}
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
		authorization, _ := url.Parse(first.AuthorizationURL)
		out, e := f.s.claim(context.WithValue(ctx, callbackLockRole{}, "claim"), f.source.ConnectorID, "portal", authorization.Query().Get("state"), first.BrowserToken, previous)
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
	starts := make(chan startOutcome, 1)
	go func() {
		out, e := f.s.Start(context.WithValue(ctx, callbackLockRole{}, "start"), f.source.ConnectorID, "portal", previous, &actor, input, uuid.NewString())
		starts <- startOutcome{out, e}
	}()
	select {
	case <-observed.startReady:
	case out := <-starts:
		t.Fatal("start ended before initial user fence", out.err)
	case <-ctx.Done():
		t.Fatal("start user fence deadline")
	}
	begin()
	select {
	case <-observed.claimReady:
	case out := <-claims:
		t.Fatal("claim ended before current session attempt", out.err)
	case <-ctx.Done():
		t.Fatal("claim session attempt deadline")
	}
	select {
	case out := <-claims:
		t.Fatal("claim bypassed held start session", out.err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	started := startResult(t, starts)
	select {
	case out := <-claims:
		if out.err != nil || !validClaimedFlow(out.flow) {
			t.Fatal("claim must finish after current start without admission/session cycle", out.err)
		}
	case <-ctx.Done():
		t.Fatal("claim/start lock cycle deadline")
	}
	if started.err != nil || started.value.BrowserToken == "" || started.value.BrowserToken == first.BrowserToken {
		t.Fatal("overlapping current start did not commit independently", started.err)
	}
}
