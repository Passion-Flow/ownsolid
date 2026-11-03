package deployments_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"testing"
	"time"
)

// Deterministically reproduce the observed workspace -> deployment FK and user ->
// workspace inversion. Protocol hardware reports remain explicit synthetic fixtures.
func TestM10ActorFencePermitsDeploymentForeignKeyAndStillFencesRevocation(t *testing.T) {
	c := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d, e := c.s.Create(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"current-actor-recheck", "session-user-lock"} {
		t.Run(kind, func(t *testing.T) {
			worker, e := c.s.Nodes.DB.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer worker.Rollback(context.Background())
			human, e := c.s.Nodes.DB.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer human.Rollback(context.Background())
			var workspace string
			if e = worker.QueryRow(ctx, "SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", c.a.Me.Workspace.ID).Scan(&workspace); e != nil {
				t.Fatal(e)
			}
			if kind == "current-actor-recheck" {
				e = c.s.Nodes.Identity.Recheck(ctx, human, c.a)
			} else {
				e = c.s.Nodes.Identity.LockSessionUsers(ctx, human, c.a.Me.User.ID, "", "admin")
			}
			if e != nil {
				t.Fatal(e)
			}
			var pid int
			if e = human.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); e != nil {
				t.Fatal(e)
			}
			// A real status/epoch write must remain blocked by the current actor fence.
			revoke, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			_, e = c.s.Nodes.DB.Exec(revoke, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", c.a.Me.User.ID)
			stop()
			if e == nil || !errors.Is(e, context.DeadlineExceeded) {
				t.Fatal("actor revocation was not fenced", e)
			}
			waiting := make(chan error, 1)
			go func() {
				var id string
				waiting <- human.QueryRow(ctx, "SELECT id::text FROM workspaces WHERE id=$1 FOR SHARE", workspace).Scan(&id)
			}()
			// Wait for the actual server lock dependency, not a scheduler timing assumption.
			blocked := false
			deadline := time.Now().Add(2 * time.Second)
			for !blocked && time.Now().Before(deadline) {
				if e = c.s.Nodes.DB.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1))>0", pid).Scan(&blocked); e != nil {
					t.Fatal(e)
				}
				if !blocked {
					time.Sleep(time.Millisecond)
				}
			}
			if !blocked {
				worker.Rollback(ctx)
				<-waiting
				t.Fatal("human workspace dependency was not observed")
			}
			bounded, finish := context.WithTimeout(ctx, 2*time.Second)
			_, e = worker.Exec(bounded, "UPDATE deployments SET removed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", d.ID)
			finish()
			// Roll back this SQL lock probe; the real owning task result runs below.
			rollback := worker.Rollback(ctx)
			humanResult := <-waiting
			if e != nil || rollback != nil || humanResult != nil {
				t.Fatal("foreign key completion deadlocked with current actor", e, rollback, humanResult)
			}
			if e = human.Rollback(ctx); e != nil {
				t.Fatal(e)
			}
		})
	}
	lease := c.claim(t)
	c.complete(t, lease, "running")
	for _, action := range []string{"stop", "remove"} {
		d, e = c.s.Get(ctx, c.a, c.a.Me.Workspace.ID, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		d, e = c.s.Action(ctx, c.a, d.ID, deployments.ActionRequest{Action: action, Revision: d.Revision, Reason: "Exact synthetic lock-regression owner cleanup"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		lease = c.claim(t)
		state := "stopped"
		if action == "remove" {
			state = "not_installed"
		}
		c.complete(t, lease, state)
	}
	d, e = c.s.Get(ctx, c.a, c.a.Me.Workspace.ID, d.ID)
	if e != nil || d.RemovedAt == nil || d.ResourceState != "released" {
		t.Fatal("actual owning result did not finish", e)
	}
}
