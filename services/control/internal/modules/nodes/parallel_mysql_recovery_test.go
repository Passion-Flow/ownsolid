//go:build repository_integration

package nodes_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

// Corrupt the actual product graph one owner at a time. The node must not be
// retired merely because an enabled owner's callback was missing or typed nil.
func TestActualParallelNodeRecoveryComposition(t *testing.T) {
	for _, missing := range []string{"components", "inference", "training", "preparations", "typed_nil_components", "typed_nil_inference", "typed_nil_training", "typed_nil_preparations"} {
		t.Run(missing, func(t *testing.T) {
			x := newNodeReadActual(t)
			x.s = nodeRecoveryProduction(t, x.owner)
			node := nodeUpdateCoreEnrolled(t, x, wire.DockerModelLifecycleProfile, "Synthetic original recovery graph")
			before, audits := nodeReadStoredDigest(t, x), x.audits(t)
			switch missing {
			case "components":
				x.s.Components = nil
			case "inference":
				x.s.Inference = nil
			case "training":
				x.s.Training = nil
			case "preparations":
				x.s.Preparations = nil
			case "typed_nil_components":
				x.s.Components = (*components.Service)(nil)
			case "typed_nil_inference":
				x.s.Inference = (*deployments.Service)(nil)
			case "typed_nil_training":
				x.s.Training = (*training.Service)(nil)
			case "typed_nil_preparations":
				x.s.Preparations = (*modelpreparations.Service)(nil)
			}
			if e := x.s.CheckRecoveryOwners(); e == nil {
				t.Fatal("incomplete production recovery graph accepted")
			}
			value, e := x.s.Update(x.ctx, x.admin, node.ID, node.Name, "removed", node.Version, "Explicit incomplete graph refusal", uuid.NewString())
			nodeReadWant(t, e, "STORAGE_UNAVAILABLE")
			if !reflect.DeepEqual(value, nodes.Node{}) || nodeReadStoredDigest(t, x) != before || x.audits(t) != audits {
				t.Fatal("incomplete graph changed node or audit authority")
			}
			x.s = nodeRecoveryProduction(t, x.owner)
			value, e = x.s.Update(x.ctx, x.admin, node.ID, node.Name, "removed", node.Version, "Explicit repaired graph removal", uuid.NewString())
			if e != nil || value.Status != "removed" {
				t.Fatal("original product graph could not recover", e)
			}
		})
	}
}

// Persist a constrained synthetic deployment and reservation in the original
// schema. These rows prove recovery retention, not physical inference execution.
func nodeRecoveryInferenceRows(t *testing.T, x nodeReadActual, node nodes.Node) (string, string) {
	t.Helper()
	model, deployment, lease, gpu := uuid.NewString(), uuid.NewString(), uuid.NewString(), "GPU-"+uuid.NewString()
	manifest, e := json.Marshal(wire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	exec := func(pg, my string, args ...any) {
		if _, e := x.f.Store.Exec(x.ctx, x.f.Statement(pg, my, args...)); e != nil {
			t.Fatal("constrained recovery rows", e)
		}
	}
	exec(`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic recovery model','qwen3-0.6b',$4,$5::jsonb,'ready',$6)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic recovery model','qwen3-0.6b',?,CAST(? AS JSON),'ready',?)`, model, x.f.Tenant, x.f.Workspace, wire.ModelCatalogDigest(), manifest, x.f.User)
	exec(`INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,created_by) VALUES($1,$2,$3,$4,$5,$6,'running','running',1,'{}',CURRENT_TIMESTAMP,$7)`, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,created_by) VALUES(?,?,?,?,?,?,'running','running',1,JSON_OBJECT(),UTC_TIMESTAMP(6),?)`, deployment, x.f.Tenant, x.f.Workspace, node.ID, model, gpu, x.f.User)
	exec(`INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES($1,$2,$3,$4,$5,$6,1,'held')`, `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES(?,?,?,?,?,?,1,'held')`, lease, x.f.Tenant, x.f.Workspace, node.ID, gpu, deployment)
	return deployment, lease
}

func TestActualParallelNodeRecoveryReservation(t *testing.T) {
	for _, fault := range []struct {
		name      string
		mode      commitfault.Mode
		committed bool
	}{
		{"acknowledged", commitfault.Pass, true},
		{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
		{"server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
	} {
		t.Run(fault.name, func(t *testing.T) {
			x := newNodeReadActual(t)
			x.s = nodeRecoveryProduction(t, x.owner)
			node := nodeUpdateCoreEnrolled(t, x, wire.DockerModelLifecycleProfile, "Synthetic reservation recovery")
			deployment, lease := nodeRecoveryInferenceRows(t, x, node)
			owner, proxy := nodeRecoveryFaultOwner(t, x)
			mutator := nodeRecoveryProduction(t, owner)
			if fault.mode != commitfault.Pass {
				if e := proxy.Arm(fault.mode); e != nil {
					t.Fatal(e)
				}
			}
			value, e := mutator.Update(x.ctx, x.admin, node.ID, node.Name, "removed", node.Version, "Explicit reservation preserving retirement", uuid.NewString())
			if fault.mode == commitfault.Pass {
				if e != nil || value.Status != "removed" {
					t.Fatal(e)
				}
			} else {
				nodeReadWant(t, e, string(transaction.CommitUnknown))
				if !reflect.DeepEqual(value, nodes.Node{}) {
					t.Fatal("uncertain removal published node")
				}
				select {
				case actual := <-proxy.Outcomes():
					if actual.Mode != fault.mode || actual.Forwarded != fault.committed || actual.ServerOK != fault.committed || !actual.Suppressed {
						t.Fatal("wrong real COMMIT fault")
					}
				case <-x.ctx.Done():
					t.Fatal("missing real COMMIT observation")
				}
			}
			if proxy.Commits() != 1 {
				t.Fatal("retirement replayed COMMIT")
			}
			var observed, reserved string
			var cleared bool
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT observed_state,observation IS NULL AND observed_at IS NULL FROM deployments WHERE id=$1`, `SELECT observed_state,observation IS NULL AND observed_at IS NULL FROM deployments WHERE id=?`, deployment)).Scan(&observed, &cleared); e != nil {
				t.Fatal(e)
			}
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT state FROM gpu_leases WHERE id=$1`, `SELECT state FROM gpu_leases WHERE id=?`, lease)).Scan(&reserved); e != nil {
				t.Fatal(e)
			}
			wantObserved, wantReserved := "running", "held"
			if fault.committed {
				wantObserved, wantReserved = "unknown", "recovery_required"
			}
			if observed != wantObserved || reserved != wantReserved || cleared != fault.committed {
				t.Fatal("original inference owner did not atomically retain physical reservation", observed, reserved, cleared)
			}
		})
	}
}

// Force the last audit write to fail after the original domain fences execute.
// Every node and reservation change must roll back, then a deliberate repaired
// action must succeed in a new transaction.
func TestActualParallelNodeRecoveryAuditRollback(t *testing.T) {
	x := newNodeReadActual(t)
	x.s = nodeRecoveryProduction(t, x.owner)
	node := nodeUpdateCoreEnrolled(t, x, wire.DockerModelLifecycleProfile, "Synthetic atomic recovery rollback")
	deployment, lease := nodeRecoveryInferenceRows(t, x, node)
	before, audits := nodeReadStoredDigest(t, x), x.audits(t)
	exec := func(pg, my string) {
		if _, e := x.f.Store.Exec(x.ctx, x.f.Statement(pg, my)); e != nil {
			t.Fatal(e)
		}
	}
	if x.f.Store.Dialect() == transaction.PostgreSQL {
		exec(`CREATE FUNCTION parallel_node_recovery_reject() RETURNS trigger LANGUAGE plpgsql AS $body$ BEGIN RAISE EXCEPTION 'synthetic recovery audit failure'; END $body$`, "")
	}
	exec(`CREATE TRIGGER parallel_node_recovery_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION parallel_node_recovery_reject()`, `CREATE TRIGGER parallel_node_recovery_audit BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic recovery audit failure'`)
	value, e := x.s.Update(x.ctx, x.admin, node.ID, node.Name, "removed", node.Version, "Explicit atomic rollback control", uuid.NewString())
	nodeReadWant(t, e, "STORAGE_UNAVAILABLE")
	if !reflect.DeepEqual(value, nodes.Node{}) || nodeReadStoredDigest(t, x) != before || x.audits(t) != audits {
		t.Fatal("failed audit exposed DTO or persisted node/task authority")
	}
	var observed, reserved string
	var retained bool
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT observed_state,observation IS NOT NULL AND observed_at IS NOT NULL FROM deployments WHERE id=$1`, `SELECT observed_state,observation IS NOT NULL AND observed_at IS NOT NULL FROM deployments WHERE id=?`, deployment)).Scan(&observed, &retained); e != nil {
		t.Fatal(e)
	}
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT state FROM gpu_leases WHERE id=$1`, `SELECT state FROM gpu_leases WHERE id=?`, lease)).Scan(&reserved); e != nil {
		t.Fatal(e)
	}
	if observed != "running" || reserved != "held" || !retained {
		t.Fatal("audit failure committed original owner fences")
	}
	exec(`DROP TRIGGER parallel_node_recovery_audit ON audit_events`, `DROP TRIGGER parallel_node_recovery_audit`)
	value, e = x.s.Update(x.ctx, x.admin, node.ID, node.Name, "removed", node.Version, "Explicit removal after inspected rollback", uuid.NewString())
	if e != nil || value.Status != "removed" {
		t.Fatal("deliberate repaired action failed", e)
	}
}
