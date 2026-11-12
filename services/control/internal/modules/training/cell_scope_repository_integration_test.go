//go:build repository_integration

package training_test

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func trainingCellCoordinates(t *testing.T, g trainingOperatorStory) (string, string) {
	t.Helper()
	var site, cell string
	if err := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT site_id,cell_id FROM training_jobs WHERE id=$1", g.job.ID)).Scan(&site, &cell); err != nil {
		t.Fatal(err)
	}
	return site, cell
}

func trainingSetCellCoordinates(t *testing.T, g trainingOperatorStory, site, cell string) {
	t.Helper()
	g.exec(t, "UPDATE training_jobs SET site_id=$1,cell_id=$2 WHERE id=$3", "", site, cell, g.job.ID)
}

// Observe original committed rows through the disposable fixture, independently
// of service filtering. Any revision, timestamp, resource or audit write changes it.
func trainingCellSnapshot(t *testing.T, g trainingOperatorStory) []byte {
	t.Helper()
	var raw []byte
	err := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT jsonb_build_object(
 'job',(SELECT to_jsonb(j) FROM training_jobs j WHERE j.id=$1),
 'attempts',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id),'[]'::jsonb) FROM training_attempts a WHERE a.job_id=$1),
 'leases',(SELECT coalesce(jsonb_agg(to_jsonb(l) ORDER BY l.id),'[]'::jsonb) FROM gpu_leases l WHERE l.training_job_id=$1),
 'tasks',(SELECT coalesce(jsonb_agg(to_jsonb(n) ORDER BY n.id),'[]'::jsonb) FROM node_tasks n WHERE n.id IN (SELECT task_id FROM training_attempts WHERE job_id=$1)),
 'audits',(SELECT coalesce(jsonb_agg(to_jsonb(e) ORDER BY e.id),'[]'::jsonb) FROM audit_events e WHERE e.target_id=$1::text)
)`, g.job.ID)).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func trainingCellUnchanged(t *testing.T, g trainingOperatorStory, before []byte) {
	t.Helper()
	if !bytes.Equal(before, trainingCellSnapshot(t, g)) {
		t.Fatal("foreign training owner, resources or audit changed")
	}
}

func TestTrainingLocalCellOwnerFence(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Skip("requires the real PostgreSQL cell catalog")
	}
	for _, coordinate := range []string{"site", "cell"} {
		t.Run(coordinate, func(t *testing.T) {
			foreign := func(site, cell string) (string, string) {
				if coordinate == "site" {
					return "retained-foreign-site", cell
				}
				return site, "retained-foreign-cell"
			}
			moveForeign := func(t *testing.T, g trainingOperatorStory, site, cell string) {
				t.Helper()
				foreignSite, foreignCell := foreign(site, cell)
				trainingSetCellCoordinates(t, g, foreignSite, foreignCell)
			}
			t.Run("queued_selectors_and_cancel_preserve_revoked_owner", func(t *testing.T) {
				g := dispatchSetup(t)
				site, cell := trainingCellCoordinates(t, g)
				moveForeign(t, g, site, cell)
				g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "", g.f.User)
				before := trainingCellSnapshot(t, g)
				dispatchOutcome(t, g, false, "")
				if worked, err := g.s.ReconcileNext(g.ctx); err != nil || worked {
					t.Fatal("foreign queued reconciliation", worked, err)
				}
				out, err := g.cancel(g.job.Revision)
				if err == nil || err.Error() != "AUTH_SESSION_EXPIRED" || !reflect.DeepEqual(out, training.Job{}) {
					t.Fatal("revoked cancellation", out, err)
				}
				trainingCellUnchanged(t, g, before)
				g.exec(t, "UPDATE users SET auth_epoch=$1 WHERE id=$2", "", g.a.Epoch, g.f.User)
				out, err = g.cancel(g.job.Revision)
				if err == nil || err.Error() != "TRAINING_NOT_FOUND" || !reflect.DeepEqual(out, training.Job{}) {
					t.Fatal("foreign owner cancellation", out, err)
				}
				trainingCellUnchanged(t, g, before)
				trainingSetCellCoordinates(t, g, site, cell)
				dispatchOutcome(t, g, true, "")
				dispatchWant(t, g, "preparing", "", 1, 1, 1)
				out, err = g.cancel(g.job.Revision + 1)
				if err != nil || out.State != "cancelling" {
					t.Fatal("restored local owner cancellation", out, err)
				}
			})
			for _, boundary := range []struct {
				name string
				at   int
			}{{"scope_changes_after_selection", 1}, {"scope_changes_before_final_admission", 2}} {
				t.Run(boundary.name, func(t *testing.T) {
					g := dispatchSetup(t)
					site, cell := trainingCellCoordinates(t, g)
					var before []byte
					x := trainingOperatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: boundary.at, before: func() {
						moveForeign(t, g, site, cell)
						before = trainingCellSnapshot(t, g)
					}})
					dispatchOutcome(t, x, false, "TRAINING_NOT_FOUND")
					if len(before) == 0 {
						t.Fatal("scope boundary was not reached")
					}
					trainingCellUnchanged(t, g, before)
					trainingSetCellCoordinates(t, g, site, cell)
					dispatchOutcome(t, g, true, "")
					dispatchWant(t, g, "preparing", "", 1, 1, 1)
				})
			}
			t.Run("active_reconciliation_preserves_attempt_and_physical_lease", func(t *testing.T) {
				g := dispatchSetup(t)
				dispatchOutcome(t, g, true, "")
				dispatchDue(t, g)
				site, cell := trainingCellCoordinates(t, g)
				moveForeign(t, g, site, cell)
				before := trainingCellSnapshot(t, g)
				if worked, err := g.s.ReconcileNext(g.ctx); err != nil || worked {
					t.Fatal("foreign active reconciliation", worked, err)
				}
				trainingCellUnchanged(t, g, before)
			})
		})
	}
}
