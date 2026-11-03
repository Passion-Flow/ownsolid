//go:build repository_integration

package sso

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualSSOStartLateInstallationReplacement(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		name := "anonymous"
		if authenticated {
			name = "current_link"
		}
		t.Run(name, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			var actor *identity.Auth
			previous := ""
			input := StartInput{Mode: ModeLogin}
			at := 2
			if authenticated {
				a, token := startActor(t, f, password, "portal", false)
				actor, previous = &a, token
				reason := "Synthetic late start scope fence"
				input = StartInput{Mode: ModeLink, Password: &password, Reason: &reason}
				at = 4
			}
			tenant, workspace := uuid.NewString(), uuid.NewString()
			f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Replacement start tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Replacement start tenant','active')`, tenant))
			f.exec(t, f.f.Statement(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Replacement start Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Replacement start Workspace','active')`, workspace, tenant))
			ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			ctx, stop := context.WithTimeout(f.ctx, 5*time.Second)
			defer stop()
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
					// Isolated privileged fault;never a customer production cutover.
					_, e := q.Exec(ctx, f.f.Statement(`UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1`, `UPDATE installation_state SET tenant_id=?,default_workspace_id=? WHERE id=1`, tenant, workspace))
					return e
				})
			}()
			select {
			case <-ready:
			case e := <-done:
				t.Fatal("start installation holder", e)
			case <-time.After(2 * time.Second):
				t.Fatal("start installation holder deadline")
			}
			observed := &catalogObservedStore{Store: f.f.Store, at: at, ready: make(chan struct{}, 1)}
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			f.accounts.Storage, f.accounts.DB = observed, nil
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			before := startCounts(t, f)
			results := make(chan startOutcome, 1)
			go func() {
				out, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", previous, actor, input, uuid.NewString())
				results <- startOutcome{out, e}
			}()
			select {
			case <-observed.ready:
			case out := <-results:
				t.Fatal("start ended before old-scope target", out.err)
			case <-time.After(2 * time.Second):
				t.Fatal("start target attempt deadline")
			}
			unlock()
			out := startResult(t, results)
			if out.err == nil || out.err.Error() != "SSO_REVISION_CONFLICT" || out.value != (StartResult{}) || startCounts(t, f) != before {
				t.Fatal("late installation replacement moved start into new tenant", out.err)
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal("actual installation replacement", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("start replacement deadline")
			}
		})
	}
}
