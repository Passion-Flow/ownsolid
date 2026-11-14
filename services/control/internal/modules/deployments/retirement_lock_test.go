package deployments_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const retirementTestLockNamespace int32 = 1297040453

// Hold the first writer after it has taken the workspace lock. These triggers
// exist only inside the test schema. Observe both actual blockers before release.
func holdRetirementRace(t *testing.T, ctx context.Context, db *pgxpool.Pool, createFirst bool) (pgx.Tx, func() int) {
	t.Helper()
	id := uuid.New()
	key := int32(binary.BigEndian.Uint32(id[:4]) & 0x7fffffff)
	exec := func(query string) {
		t.Helper()
		if _, e := db.Exec(ctx, query); e != nil {
			t.Fatal(e)
		}
	}
	exec(`CREATE FUNCTION hold_model_retirement_race() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(TG_ARGV[0]::integer,TG_ARGV[1]::integer); RETURN NEW; END $$`)
	event := "UPDATE OF state ON model_versions"
	if createFirst {
		event = "INSERT ON deployments"
	}
	exec(fmt.Sprintf(`CREATE TRIGGER hold_model_retirement_race BEFORE %s FOR EACH ROW
		EXECUTE FUNCTION hold_model_retirement_race('%d','%d')`, event, retirementTestLockNamespace, key))
	lock, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = lock.Rollback(context.Background()) })
	var owner int
	e = lock.QueryRow(ctx, `SELECT pg_backend_pid() FROM pg_advisory_xact_lock($1::integer,$2::integer)`,
		retirementTestLockNamespace, key).Scan(&owner)
	if e != nil {
		t.Fatal(e)
	}
	return lock, func() int {
		return waitRetirementBlocker(t, ctx, func(ctx context.Context) (int, error) {
			var pid int
			e := db.QueryRow(ctx, `SELECT pid FROM pg_locks WHERE locktype='advisory' AND objsubid=2
				AND classid=$1::oid AND objid=$2::oid AND NOT granted AND $3=ANY(pg_blocking_pids(pid))`,
				uint32(retirementTestLockNamespace), uint32(key), owner).Scan(&pid)
			return pid, e
		})
	}
}

func waitRetirementBlocker(t *testing.T, ctx context.Context, find func(context.Context) (int, error)) int {
	t.Helper()
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		pid, e := find(ctx)
		if e == nil {
			return pid
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			t.Fatalf("read PostgreSQL blocker: %v", e)
		}
		select {
		case <-ctx.Done():
			t.Fatal("PostgreSQL did not observe the expected lock wait")
		case <-tick.C:
		}
	}
}

func TestActualDeploymentCreationAndModelRetirementSerialize(t *testing.T) {
	for _, tc := range []struct {
		name        string
		createFirst bool
	}{
		{name: "deployment commits first", createFirst: true},
		{name: "retirement commits first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := setup(t)
			ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
			defer stop()
			db := c.s.Nodes.DB
			lock, firstPID := holdRetirementRace(t, ctx, db, tc.createFirst)
			type created struct {
				value deployments.Deployment
				err   error
			}
			type retired struct {
				value models.Retirement
				err   error
			}
			creates, retires := make(chan created, 1), make(chan retired, 1)
			create := func() {
				v, e := c.s.Create(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
				creates <- created{v, e}
			}
			retire := func() {
				v, e := c.s.Models.Retire(ctx, c.a, c.model, "Retire unused model", uuid.NewString())
				retires <- retired{v, e}
			}
			first, second := retire, create
			if tc.createFirst {
				first, second = create, retire
			}
			go first()
			blocker := firstPID()
			go second()
			waitRetirementBlocker(t, ctx, func(ctx context.Context) (int, error) {
				var pid int
				e := db.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database()
					AND $1=ANY(pg_blocking_pids(pid))`, blocker).Scan(&pid)
				return pid, e
			})
			if e := lock.Commit(ctx); e != nil {
				t.Fatal(e)
			}

			d, r := <-creates, <-retires
			var deploymentsCount, leaseCount, taskCount, retireCount int
			e := db.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM deployments WHERE model_id=$1),
				(SELECT count(*) FROM gpu_leases WHERE node_id=$2),
				(SELECT count(*) FROM node_tasks WHERE node_id=$2 AND operation='inference.deploy.v1'),
				(SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='model.version_retired')`,
				c.model, c.node.NodeID).Scan(&deploymentsCount, &leaseCount, &taskCount, &retireCount)
			if e != nil {
				t.Fatal(e)
			}
			if !tc.createFirst {
				check(t, d.err, "RESOURCE_NOT_FOUND")
				if d.value.ID != "" || r.err != nil || r.value.State != "deleted" ||
					deploymentsCount != 0 || leaseCount != 0 || taskCount != 0 || retireCount != 1 {
					t.Fatalf("retirement did not prevent deployment side effects: deployment=%+v retirement=%+v durable=%d/%d/%d/%d",
						d, r, deploymentsCount, leaseCount, taskCount, retireCount)
				}
				return
			}
			check(t, r.err, "MODEL_VERSION_REFERENCED")
			if r.value.ID != "" || d.err != nil || d.value.ID == "" ||
				deploymentsCount != 1 || leaseCount != 1 || taskCount != 1 || retireCount != 0 {
				t.Fatalf("deployment did not prevent retirement: deployment=%+v retirement=%+v durable=%d/%d/%d/%d",
					d, r, deploymentsCount, leaseCount, taskCount, retireCount)
			}

			c.complete(t, c.claim(t), "running")
			for _, action := range []string{"stop", "remove"} {
				v, e := c.s.Get(ctx, c.a, c.a.Me.Workspace.ID, d.value.ID)
				if e != nil {
					t.Fatal(e)
				}
				_, e = c.s.Action(ctx, c.a, v.ID, deployments.ActionRequest{
					Action: action, Revision: v.Revision, Reason: "Confirm deployment release before retirement",
				}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				state := "stopped"
				if action == "remove" {
					state = "not_installed"
				}
				c.complete(t, c.claim(t), state)
			}
			v, e := c.s.Models.Retire(ctx, c.a, c.model, "Retire after confirmed deployment removal", uuid.NewString())
			if e != nil || v.State != "deleted" {
				t.Fatalf("confirmed removal did not allow retirement: %+v %v", v, e)
			}
		})
	}
}
