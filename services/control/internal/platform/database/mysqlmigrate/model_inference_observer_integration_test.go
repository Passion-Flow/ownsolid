//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type modelInferenceObserverFixture struct {
	store      *mysqlstore.Store
	catalog    Catalog
	rows       modelInferenceSchemaRows
	service    *nodes.Service
	actor      identity.Auth
	node       nodes.Node
	context    context.Context
	leaseState string
}

func modelInferenceObserverReady(t *testing.T, profile, state string) modelInferenceObserverFixture {
	t.Helper()
	s, c, r := modelInferenceReady(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	repository, e := transaction.FromMySQL(s)
	if e != nil {
		t.Fatal(e)
	}
	owner := &identity.Service{Storage: repository}
	var issued identity.IssuedSession
	if e = repository.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, r.user, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal("actual observer session issuance", e)
	}
	actor, e := owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal("actual observer session authentication", e)
	}
	migrationExec(t, s, `UPDATE managed_nodes SET execution_profile=? WHERE id=?`, profile, r.node)
	migrationExec(t, s, `UPDATE gpu_leases SET state=? WHERE deployment_id=?`, state, r.deployment)
	service := &nodes.Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	// Attach the real production implementation, not a success adapter. Training
	// and preparation observers remain explicitly absent: their schemas/ports
	// are later dependencies, not evidence of full production orphan recovery.
	service.Inference = &deployments.Service{Nodes: service}
	n, e := service.Get(ctx, actor, "", r.node)
	if e != nil {
		t.Fatal("actual observer stored node lookup", e)
	}
	return modelInferenceObserverFixture{s, c, r, service, actor, n, ctx, state}
}

func modelInferenceObserverDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return hash(modelInferenceRecoveryDigest(t, s) + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) FROM audit_events ORDER BY id`))
}

func modelInferenceObserverRemove(t *testing.T, x modelInferenceObserverFixture) {
	t.Helper()
	request := uuid.NewString()
	result, e := x.service.Update(x.context, x.actor, x.node.ID, x.node.Name, "removed", x.node.Version, "Explicit synthetic observer recovery", request)
	if e != nil || result.Status != "removed" || result.Version != x.node.Version+1 || result.ID != x.node.ID {
		t.Fatal("actual production node/inference removal", e)
	}
	var credential, observed, stamped bool
	var lease, state string
	var revision int64
	expectedLease := "recovery_required"
	if x.leaseState == "released" {
		expectedLease = "released"
	}
	if e = x.store.Pool().QueryRowContext(x.context, `SELECT credential_hash IS NULL,inference_fenced_at IS NOT NULL FROM managed_nodes WHERE id=?`, x.node.ID).Scan(&credential, &stamped); e != nil || !credential || !stamped {
		t.Fatal("actual removal credential/profile fence", mysqlstore.SafeError(e))
	}
	if e = x.store.Pool().QueryRowContext(x.context, `SELECT state,revision FROM gpu_leases WHERE deployment_id=?`, x.rows.deployment).Scan(&lease, &revision); e != nil || revision != 1 || lease != expectedLease {
		t.Fatal("actual uncertain physical reservation incorrectly released", mysqlstore.SafeError(e))
	}
	if e = x.store.Pool().QueryRowContext(x.context, `SELECT observed_state,observation IS NULL AND observed_at IS NULL AND removed_at IS NULL FROM deployments WHERE id=?`, x.rows.deployment).Scan(&state, &observed); e != nil || state != "unknown" || !observed {
		t.Fatal("actual deployment uncertainty not preserved", mysqlstore.SafeError(e))
	}
	var count int
	if e = x.store.Pool().QueryRowContext(x.context, `SELECT COUNT(*) FROM audit_events WHERE actor_user_id=? AND tenant_id=? AND workspace_id=? AND audience='admin' AND action='node.updated' AND outcome='success' AND target_type='node' AND target_id=? AND reason='Explicit synthetic observer recovery' AND request_id=?`, x.rows.user, x.rows.tenant, x.rows.workspace, x.node.ID, request).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual node/inference atomic Admin audit", mysqlstore.SafeError(e))
	}
	if e = x.store.Pool().QueryRowContext(x.context, `SELECT COUNT(*) FROM node_tasks WHERE id=? AND state='cancelled' AND error_code='NODE_UNAVAILABLE' AND completed_at IS NOT NULL AND lease_token_hash IS NULL AND lease_expires_at IS NULL`, x.rows.task).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual node/inference task cancellation", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLModelInferenceSchemaObserverLifecycle(t *testing.T) {
	// Each case has an isolated database via --leaf selection. The Go parent
	// uses one schema, so only one profile/state scenario is selected per process.
	for _, item := range []struct{ name, profile, state string }{
		{"inference_held", wire.DockerInferenceProfile, "held"},
		{"AI_recovery_required", wire.DockerAIProfile, "recovery_required"},
		{"lifecycle_released", wire.DockerModelLifecycleProfile, "released"},
	} {
		t.Run(item.name, func(t *testing.T) {
			x := modelInferenceObserverReady(t, item.profile, item.state)
			foreign := modelInferenceForeign(t, x.store, x.rows, x.rows.tenant)
			foreignDeployment := uuid.NewString()
			foreignGPU := "GPU-" + uuid.NewString()
			migrationExec(t, x.store, modelInferenceInsertDeployment, foreignDeployment, foreign.tenant, foreign.workspace, foreign.node, foreign.model, foreignGPU, x.rows.user, nil)
			migrationExec(t, x.store, modelInferenceInsertLease, foreign.tenant, foreign.workspace, foreign.node, foreignGPU, foreignDeployment)
			before := licenseSchemaQueryDigest(t, x.store, `SELECT JSON_ARRAY(d.id,d.observed_state,d.updated_at,g.state,g.updated_at) FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id WHERE d.id='`+foreignDeployment+`'`)
			modelInferenceObserverRemove(t, x)
			var state string
			if e := x.store.Pool().QueryRowContext(x.context, `SELECT state FROM gpu_leases WHERE deployment_id=?`, x.rows.deployment).Scan(&state); e != nil || state != map[bool]string{true: "released", false: "recovery_required"}[item.state == "released"] {
				t.Fatal("actual lease lifecycle state", mysqlstore.SafeError(e))
			}
			after := licenseSchemaQueryDigest(t, x.store, `SELECT JSON_ARRAY(d.id,d.observed_state,d.updated_at,g.state,g.updated_at) FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id WHERE d.id='`+foreignDeployment+`'`)
			if after != before {
				t.Fatal("actual production observer changed foreign Workspace reservation")
			}
			migrationRun(t, x.store, x.catalog, Readiness)
		})
	}
}

func TestActualMySQLModelInferenceSchemaObserverRollback(t *testing.T) {
	for _, item := range []struct{ name, table, event string }{
		{"lease_failure", "gpu_leases", "UPDATE"},
		{"deployment_failure", "deployments", "UPDATE"},
		{"audit_failure", "audit_events", "INSERT"},
	} {
		t.Run(item.name, func(t *testing.T) {
			x := modelInferenceObserverReady(t, wire.DockerInferenceProfile, "held")
			// Fixed owned trigger identifiers and table/event allowlist above. The
			// fixture injects an actual server refusal, never a SQL success double.
			migrationExec(t, x.store, "CREATE TRIGGER mg08_observer_failure BEFORE "+item.event+" ON "+item.table+" FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Synthetic observer rollback failure'")
			before := modelInferenceObserverDigest(t, x.store)
			result, e := x.service.Update(x.context, x.actor, x.node.ID, x.node.Name, "removed", x.node.Version, "Explicit synthetic observer recovery", uuid.NewString())
			if e == nil || !reflect.DeepEqual(result, nodes.Node{}) || modelInferenceObserverDigest(t, x.store) != before {
				t.Fatal("failed production observer/audit leaked DTO or partial node/task/reservation/audit effect", e)
			}
			migrationExec(t, x.store, `DROP TRIGGER mg08_observer_failure`)
			modelInferenceObserverRemove(t, x)
			migrationRun(t, x.store, x.catalog, Readiness)
		})
	}
}
