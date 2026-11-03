//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

const modelInferenceInsertModel = `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic scope model','qwen3-0.6b',?,JSON_OBJECT(),'ready',?)`
const modelInferenceInsertImport = `INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,state,requested_by,requested_epoch,idempotency_key,fingerprint,reason,model_id) VALUES(?,?,?,'Synthetic scope import',?,16,?,?,1,?,?,'Synthetic schema acceptance',?)`
const modelInferenceInsertDeployment = `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,created_by,removed_at) VALUES(?,?,?,?,?,?,'running',1,?,?)`
const modelInferenceInsertLease = `INSERT INTO gpu_leases(tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES(?,?,?,?,?,1,'held')`

type modelInferenceForeignScope struct{ tenant, workspace, node, model, task string }

func modelInferenceForeign(t *testing.T, s *mysqlstore.Store, r modelInferenceSchemaRows, tenant string) modelInferenceForeignScope {
	t.Helper()
	f := modelInferenceForeignScope{tenant: tenant, workspace: uuid.NewString(), node: uuid.NewString(), model: uuid.NewString(), task: uuid.NewString()}
	migrationExec(t, s, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')`, f.workspace, f.tenant, "Synthetic scope "+f.workspace)
	migrationExec(t, s, nodeSchemaInsertNode, f.node, f.tenant, f.workspace, "Synthetic scope node "+f.node, []byte(uuid.NewString())[:32])
	migrationExec(t, s, modelInferenceInsertModel, f.model, f.tenant, f.workspace, strings.Repeat("b", 64), r.user)
	migrationExec(t, s, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,'collect_facts.v1','pending',?,?,TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)),JSON_OBJECT(),?)`, f.task, f.tenant, f.workspace, f.node, uuid.NewString(), r.user, make([]byte, 32))
	return f
}

func TestActualMySQLModelInferenceSchemaForeignScopes(t *testing.T) {
	s, c, r := modelInferenceReady(t)
	same := modelInferenceForeign(t, s, r, r.tenant)
	tenant := uuid.NewString()
	migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign tenant','active')`, tenant)
	foreign := modelInferenceForeign(t, s, r, tenant)
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"model_tenant_workspace", modelInferenceInsertModel, []any{uuid.NewString(), foreign.tenant, r.workspace, strings.Repeat("c", 64), r.user}, 1452},
		{"model_workspace_tenant", modelInferenceInsertModel, []any{uuid.NewString(), r.tenant, foreign.workspace, strings.Repeat("c", 64), r.user}, 1452},
		{"import_foreign_workspace_model", modelInferenceInsertImport, []any{uuid.NewString(), r.tenant, same.workspace, strings.Repeat("c", 64), "cancelled", r.user, uuid.NewString(), make([]byte, 32), r.model}, 1452},
		{"import_foreign_tenant_model", modelInferenceInsertImport, []any{uuid.NewString(), foreign.tenant, foreign.workspace, strings.Repeat("c", 64), "cancelled", r.user, uuid.NewString(), make([]byte, 32), r.model}, 1452},
		{"inference_foreign_workspace_node", `UPDATE inference_nodes SET node_id=? WHERE node_id=?`, []any{same.node, r.node}, 1452},
		{"inference_foreign_tenant_node", `UPDATE inference_nodes SET node_id=? WHERE node_id=?`, []any{foreign.node, r.node}, 1452},
		{"deployment_foreign_workspace_model", modelInferenceInsertDeployment, []any{uuid.NewString(), r.tenant, r.workspace, r.node, same.model, r.gpu, r.user, r.heartbeat}, 1452},
		{"deployment_foreign_tenant_model", modelInferenceInsertDeployment, []any{uuid.NewString(), r.tenant, r.workspace, r.node, foreign.model, r.gpu, r.user, r.heartbeat}, 1452},
		{"deployment_foreign_workspace_node", modelInferenceInsertDeployment, []any{uuid.NewString(), r.tenant, r.workspace, same.node, r.model, r.gpu, r.user, r.heartbeat}, 1452},
		{"deployment_foreign_tenant_node", modelInferenceInsertDeployment, []any{uuid.NewString(), r.tenant, r.workspace, foreign.node, r.model, r.gpu, r.user, r.heartbeat}, 1452},
		{"deployment_foreign_workspace_task", `UPDATE deployments SET last_task_id=? WHERE id=?`, []any{same.task, r.deployment}, 1452},
		{"deployment_foreign_tenant_task", `UPDATE deployments SET last_task_id=? WHERE id=?`, []any{foreign.task, r.deployment}, 1452},
		{"lease_foreign_workspace_deployment", `UPDATE gpu_leases SET node_id=? WHERE deployment_id=?`, []any{same.node, r.deployment}, 1452},
		{"lease_foreign_tenant_deployment", `UPDATE gpu_leases SET tenant_id=?,workspace_id=?,node_id=? WHERE deployment_id=?`, []any{foreign.tenant, foreign.workspace, foreign.node, r.deployment}, 1452},
		{"referenced_model_delete", `DELETE FROM model_versions WHERE id=?`, []any{r.model}, 1451},
		{"referenced_import_delete", `DELETE FROM model_imports WHERE id=?`, []any{r.importing}, 1451},
		{"referenced_node_delete", `DELETE FROM managed_nodes WHERE id=?`, []any{r.node}, 1451},
		{"referenced_deployment_delete", `DELETE FROM deployments WHERE id=?`, []any{r.deployment}, 1451},
	} {
		t.Run(item.name, func(t *testing.T) { modelInferenceRefuses(t, s, item.number, item.query, item.args...) })
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLModelInferenceSchemaReservationLifecycle(t *testing.T) {
	s, c, r := modelInferenceReady(t)
	digest := strings.Repeat("a", 64)
	t.Run("ready_digest_reuse_and_binary_case", func(t *testing.T) {
		args := []any{uuid.NewString(), r.tenant, r.workspace, digest, r.user}
		modelInferenceRefuses(t, s, 1062, modelInferenceInsertModel, args...)
		migrationExec(t, s, `UPDATE model_versions SET state='deleted',deleted_at=UTC_TIMESTAMP(6) WHERE id=?`, r.model)
		migrationExec(t, s, modelInferenceInsertModel, args...)
		// Names preserve binary/case/trailing Unicode content; digest remains a fixed lower-hex identity.
		for _, name := range []string{"Synthetic model \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "synthetic model \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "Synthetic model \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629 ", strings.Repeat("\u4e16", 100)} {
			migrationExec(t, s, `UPDATE model_versions SET name=? WHERE id=?`, name, r.model)
			var got string
			if e := s.Pool().QueryRowContext(t.Context(), `SELECT name FROM model_versions WHERE id=?`, r.model).Scan(&got); e != nil || got != name {
				t.Fatal("binary Unicode name round trip", mysqlstore.SafeError(e))
			}
		}
	})
	t.Run("failed_import_retains_until_cancelled", func(t *testing.T) {
		migrationExec(t, s, `UPDATE model_imports SET state='failed' WHERE id=?`, r.importing)
		args := []any{uuid.NewString(), r.tenant, r.workspace, digest, "uploading", r.user, uuid.NewString(), make([]byte, 32), nil}
		modelInferenceRefuses(t, s, 1062, modelInferenceInsertImport, args...)
		migrationExec(t, s, `UPDATE model_imports SET state='cancelled' WHERE id=?`, r.importing)
		migrationExec(t, s, modelInferenceInsertImport, args...)
		migrationExec(t, s, `UPDATE model_imports SET state='ready' WHERE id=?`, args[0])
		next := append([]any{}, args...)
		next[0], next[6] = uuid.NewString(), uuid.NewString()
		migrationExec(t, s, modelInferenceInsertImport, next...)
		// Completion/cancellation releases only active index, never scoped idempotency.
		sameKey := append([]any{}, args...)
		sameKey[0], sameKey[4] = uuid.NewString(), "cancelled"
		modelInferenceRefuses(t, s, 1062, modelInferenceInsertImport, sameKey...)
	})
	t.Run("chunk_duplicate_and_exact_boundary", func(t *testing.T) {
		modelInferenceRefuses(t, s, 1062, `INSERT INTO model_import_chunks VALUES(?,0,1,?)`, r.importing, digest)
		migrationExec(t, s, `INSERT INTO model_import_chunks VALUES(?,8388608,8388608,?)`, r.importing, digest)
	})
	t.Run("retained_deployment_release", func(t *testing.T) {
		args := []any{uuid.NewString(), r.tenant, r.workspace, r.node, r.model, r.gpu, r.user, nil}
		modelInferenceRefuses(t, s, 1062, modelInferenceInsertDeployment, args...)
		migrationExec(t, s, `UPDATE deployments SET desired_state='removed',removed_at=UTC_TIMESTAMP(6) WHERE id=?`, r.deployment)
		migrationExec(t, s, modelInferenceInsertDeployment, args...)
	})
	t.Run("uncertain_GPU_retains_global_and_node_exclusivity", func(t *testing.T) {
		migrationExec(t, s, `UPDATE gpu_leases SET state='recovery_required' WHERE deployment_id=?`, r.deployment)
		replacement := uuid.NewString()
		// A removed synthetic deployment row supplies a distinct scoped lease owner;
		// this case does not depend on another subtest releasing a retained index.
		migrationExec(t, s, modelInferenceInsertDeployment, replacement, r.tenant, r.workspace, r.node, r.model, r.gpu, r.user, r.heartbeat)
		modelInferenceRefuses(t, s, 1062, modelInferenceInsertLease, r.tenant, r.workspace, r.node, "GPU-"+uuid.NewString(), replacement)
		peer := modelInferenceForeign(t, s, r, r.tenant)
		deployment := uuid.NewString()
		migrationExec(t, s, modelInferenceInsertDeployment, deployment, peer.tenant, peer.workspace, peer.node, peer.model, r.gpu, r.user, nil)
		modelInferenceRefuses(t, s, 1062, modelInferenceInsertLease, peer.tenant, peer.workspace, peer.node, r.gpu, deployment)
		migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE deployment_id=?`, r.deployment)
		var reservedNode, reservedGPU sql.NullString
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT reserved_node_id,reserved_gpu_uuid FROM gpu_leases WHERE deployment_id=?`, r.deployment).Scan(&reservedNode, &reservedGPU); e != nil || reservedNode.Valid || reservedGPU.Valid {
			t.Fatal("released index reservation retained", mysqlstore.SafeError(e))
		}
		migrationExec(t, s, modelInferenceInsertLease, peer.tenant, peer.workspace, peer.node, r.gpu, deployment)
		// Same node becomes available only after explicit release; old retained row still exists.
		migrationExec(t, s, modelInferenceInsertLease, r.tenant, r.workspace, r.node, "GPU-"+uuid.NewString(), replacement)
	})
	migrationRun(t, s, c, Readiness)
}

func modelInferenceOneWinner(t *testing.T, s *mysqlstore.Store, query string, left, right []any) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Reserve two distinct real driver connections before releasing both contenders.
	a, e := s.Pool().Conn(ctx)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer a.Close()
	b, e := s.Pool().Conn(ctx)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer b.Close()
	start, results := make(chan struct{}), make(chan error, 2)
	for i, conn := range []*sql.Conn{a, b} {
		args := [][]any{left, right}[i]
		go func() { <-start; _, e := conn.ExecContext(ctx, query, args...); results <- e }()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			successes++
			continue
		}
		var safe *mysqlstore.Error
		if errors.As(mysqlstore.SafeError(e), &safe) && safe.Number == 1062 {
			conflicts++
		} else {
			t.Fatal("unexpected competing reservation error", mysqlstore.SafeError(e))
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("actual concurrent unique reservation needs one durable winner", successes, conflicts)
	}
}

func TestActualMySQLModelInferenceSchemaConcurrentReservations(t *testing.T) {
	s, c, r := modelInferenceReady(t)
	digest := strings.Repeat("b", 64)
	t.Run("ready_model", func(t *testing.T) {
		modelInferenceOneWinner(t, s, modelInferenceInsertModel, []any{uuid.NewString(), r.tenant, r.workspace, digest, r.user}, []any{uuid.NewString(), r.tenant, r.workspace, digest, r.user})
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_versions WHERE catalog_digest=? AND state='ready'`, digest).Scan(&count); e != nil || count != 1 {
			t.Fatal("model winner readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("active_import", func(t *testing.T) {
		migrationExec(t, s, `UPDATE model_imports SET state='cancelled' WHERE id=?`, r.importing)
		modelInferenceOneWinner(t, s, modelInferenceInsertImport, []any{uuid.NewString(), r.tenant, r.workspace, digest, "uploading", r.user, uuid.NewString(), make([]byte, 32), nil}, []any{uuid.NewString(), r.tenant, r.workspace, digest, "queued", r.user, uuid.NewString(), make([]byte, 32), nil})
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_imports WHERE active_workspace_id=?`, r.workspace).Scan(&count); e != nil || count != 1 {
			t.Fatal("import winner readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("retained_deployment", func(t *testing.T) {
		migrationExec(t, s, `UPDATE deployments SET removed_at=UTC_TIMESTAMP(6) WHERE id=?`, r.deployment)
		modelInferenceOneWinner(t, s, modelInferenceInsertDeployment, []any{uuid.NewString(), r.tenant, r.workspace, r.node, r.model, r.gpu, r.user, nil}, []any{uuid.NewString(), r.tenant, r.workspace, r.node, r.model, r.gpu, r.user, nil})
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deployments WHERE retained_node_id=?`, r.node).Scan(&count); e != nil || count != 1 {
			t.Fatal("deployment winner readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("physical_GPU_across_scopes", func(t *testing.T) {
		left, right := modelInferenceForeign(t, s, r, r.tenant), modelInferenceForeign(t, s, r, r.tenant)
		a, b, gpu := uuid.NewString(), uuid.NewString(), "GPU-"+uuid.NewString()
		for _, item := range []struct {
			scope      modelInferenceForeignScope
			deployment string
		}{{left, a}, {right, b}} {
			f := item.scope
			migrationExec(t, s, modelInferenceInsertDeployment, item.deployment, f.tenant, f.workspace, f.node, f.model, gpu, r.user, nil)
		}
		modelInferenceOneWinner(t, s, modelInferenceInsertLease, []any{left.tenant, left.workspace, left.node, gpu, a}, []any{right.tenant, right.workspace, right.node, gpu, b})
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM gpu_leases WHERE reserved_gpu_uuid=?`, gpu).Scan(&count); e != nil || count != 1 {
			t.Fatal("physical GPU winner readback", mysqlstore.SafeError(e))
		}
	})
	migrationRun(t, s, c, Readiness)
}
