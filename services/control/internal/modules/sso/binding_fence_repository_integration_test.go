//go:build repository_integration

package sso

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualSSOBindingConcurrentIdenticalIntent(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "portal", nil)
	input, key := UnlinkBinding{Revision: 1, Password: password, Reason: "Synthetic concurrent identical unlink"}, uuid.NewString()
	start, results := make(chan struct{}), make(chan bindingOutcome, 2)
	for range 2 {
		go func() {
			<-start
			value, e := f.s.Unlink(f.ctx, a, f.source.BindingID, input, key, uuid.NewString())
			results <- bindingOutcome{value, e}
		}()
	}
	close(start)
	first, second := bindingResult(t, results), bindingResult(t, results)
	if first.err != nil || second.err != nil || !sameBinding(first.value, second.value) || first.value.Active || first.value.Revision != 2 || bindingCounts(t, f, f.source.BindingID, key) != ([3]int{1, 1, 1}) {
		t.Fatal("concurrent identical unlink must accept one revision/intent/audit", first.err, second.err)
	}
}

func TestActualSSOBindingLateInstallationReplacement(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "portal", nil)
	tenant, workspace := uuid.NewString(), uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Replacement binding tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Replacement binding tenant','active')`, tenant))
	f.exec(t, f.f.Statement(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Replacement binding Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Replacement binding Workspace','active')`, workspace, tenant))
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	go func() {
		done <- f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id int
			if e := q.QueryRow(ctx, f.f.Statement(`SELECT id FROM installation_state WHERE id=1 FOR UPDATE`, `SELECT id FROM installation_state WHERE id=1 FOR UPDATE`)).Scan(&id); e != nil {
				return e
			}
			close(ready)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			// This privileged isolated fault is not a customer tenant-cutover API.
			_, e := q.Exec(ctx, f.f.Statement(`UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1`, `UPDATE installation_state SET tenant_id=?,default_workspace_id=? WHERE id=1`, tenant, workspace))
			return e
		})
	}()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal("installation holder", e)
	case <-time.After(2 * time.Second):
		t.Fatal("installation holder deadline")
	}
	observed := &catalogObservedStore{Store: f.f.Store, at: 5, ready: make(chan struct{}, 1)}
	originalStore, originalDB := f.accounts.Storage, f.accounts.DB
	f.accounts.Storage, f.accounts.DB = observed, nil
	defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
	key := uuid.NewString()
	results := make(chan bindingOutcome, 1)
	go func() {
		value, e := f.s.Unlink(f.ctx, a, f.source.BindingID, UnlinkBinding{Revision: 1, Password: password, Reason: "Synthetic late account scope fence"}, key, uuid.NewString())
		results <- bindingOutcome{value, e}
	}()
	select {
	case <-observed.ready:
	case out := <-results:
		t.Fatal("operation ended before old-scope target attempt", out.err)
	case <-time.After(2 * time.Second):
		t.Fatal("old-scope target deadline")
	}
	unlock()
	out := bindingResult(t, results)
	if out.err == nil || out.err.Error() != "SSO_REVISION_CONFLICT" || out.value != (Binding{}) || bindingCounts(t, f, f.source.BindingID, key) != ([3]int{1, 0, 0}) {
		t.Fatal("late scope replacement moved account mutation", out.err)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal("actual replacement", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement deadline")
	}
	stored, e := selectBindingWithin(f.ctx, f.f.Store, f.f.Tenant, f.f.User, f.source.BindingID)
	if e != nil || !stored.Active || stored.Revision != 1 {
		t.Fatal("late fence failure changed old binding", e)
	}
}
