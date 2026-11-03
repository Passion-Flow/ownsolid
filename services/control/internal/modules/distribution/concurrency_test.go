package distribution_test

import (
	"context"
	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"sync"
	"testing"
	"time"
)

func (c scenario) waitForDatabaseLock(t *testing.T, pattern string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if e := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1", pattern).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("controlled database lock boundary was not observed")
}
func TestActualAdmissionAndKeyRevocationSerializeInBothOrders(t *testing.T) {
	for _, revokeFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "revoke_before_admission", false: "admission_before_revoke"}[revokeFirst], func(t *testing.T) {
			c := setup(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			access := c.access(t)
			input := access.input()
			barrier, e := c.s.DB.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var workers sync.WaitGroup
			defer func() { cancel(); _ = barrier.Rollback(context.WithoutCancel(ctx)); workers.Wait() }()
			// KEY SHARE permits the current read-only scope preflight, but blocks the
			// exclusive mutation fence after ordered user locks have been acquired.
			if _, e = barrier.Exec(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR KEY SHARE", access.alias.WorkspaceID); e != nil {
				t.Fatal(e)
			}
			revokeDone := make(chan error, 1)
			type admissionResult struct {
				value wire.Admission
				err   error
			}
			admitDone := make(chan admissionResult, 1)
			revoke := func() {
				workers.Add(1)
				go func() {
					defer workers.Done()
					_, e := c.s.RevokeKey(ctx, c.a, access.alias.WorkspaceID, access.key.Key.ID, "Synthetic concurrent revoke", uuid.NewString())
					revokeDone <- e
				}()
			}
			admit := func() {
				workers.Add(1)
				go func() {
					defer workers.Done()
					value, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString())
					admitDone <- admissionResult{value, e}
				}()
			}
			if revokeFirst {
				revoke()
				c.waitForDatabaseLock(t, "%FOR UPDATE OF w%")
				admit()
				c.waitForDatabaseLock(t, "SELECT auth_epoch,status FROM users%FOR UPDATE%")
			} else {
				admit()
				c.waitForDatabaseLock(t, "%FOR UPDATE OF w%")
				revoke()
				c.waitForDatabaseLock(t, "SELECT id::text FROM users WHERE id=%FOR UPDATE%")
			}
			if e = barrier.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			if e = <-revokeDone; e != nil {
				t.Fatal(e)
			}
			result := <-admitDone
			if revokeFirst {
				check(t, result.err, "API_KEY_INVALID")
			} else {
				if result.err != nil || result.value.RequestID != input.RequestID {
					t.Fatal("earlier admission was incorrectly revoked", result.err)
				}
				if _, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			}
			_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, access.input(), uuid.NewString())
			check(t, e, "API_KEY_INVALID")
		})
	}
}
