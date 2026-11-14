//go:build mysql_integration

package mysqlmigrate

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Keep an actual held inference reservation and a separate recovery training
// reservation. These are synthetic SQL graph fixtures, not hardware evidence.
func holdoutSchemaReservationSeed(t *testing.T, s *mysqlstore.Store) trainingSchemaRows {
	t.Helper()
	old := datasetSchemaSeed(t, s, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))))
	old.node, old.gpu = uuid.NewString(), "GPU-"+uuid.NewString()
	migrationExec(t, s, nodeSchemaInsertNode, old.node, old.tenant, old.workspace, "Synthetic held-out training node "+old.node, []byte(uuid.NewString())[:32])
	r := trainingSchemaSeed(t, s, old)
	migrationExec(t, s, `UPDATE training_jobs SET state='recovery_required',finished_at=NULL,error_code='SYNTHETIC_HOLDOUT_RECOVERY' WHERE id=?`, r.training)
	migrationExec(t, s, `UPDATE training_attempts SET state='recovery_required',finished_at=NULL,error_code='SYNTHETIC_HOLDOUT_RECOVERY' WHERE id=?`, r.trainingAttempt)
	migrationExec(t, s, trainingSchemaInsertLease, trainingSchemaLeaseArgs(r, r.training, r.node, r.gpu, "recovery_required")...)
	return r
}

func holdoutSchemaAssertReservations(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	var inference, training, distinctNodes, distinctGPUs int
	query := `SELECT SUM(deployment_id IS NOT NULL AND training_job_id IS NULL AND state='held' AND reserved_node_id=node_id AND reserved_gpu_uuid=gpu_uuid),SUM(deployment_id IS NULL AND training_job_id IS NOT NULL AND state='recovery_required' AND reserved_node_id=node_id AND reserved_gpu_uuid=gpu_uuid),COUNT(DISTINCT reserved_node_id),COUNT(DISTINCT reserved_gpu_uuid) FROM gpu_leases`
	if e := s.Pool().QueryRowContext(t.Context(), query).Scan(&inference, &training, &distinctNodes, &distinctGPUs); e != nil || inference != 1 || training != 1 || distinctNodes != 2 || distinctGPUs != 2 {
		t.Fatal("independent inference/training reservations lost", mysqlstore.SafeError(e))
	}
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id AND a.job_id=j.id AND a.tenant_id=j.tenant_id AND a.workspace_id=j.workspace_id JOIN gpu_leases g ON g.training_job_id=j.id AND g.tenant_id=j.tenant_id AND g.workspace_id=j.workspace_id AND g.node_id=j.current_node_id AND g.gpu_uuid=j.gpu_uuid WHERE j.state='recovery_required' AND a.state='recovery_required' AND g.state='recovery_required'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("training recovery reservation lost actual owner", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLHoldoutSchemaUpgrade(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:85]
	migrationRun(t, s, prefix, Apply)
	r := holdoutSchemaReservationSeed(t, s)
	holdoutSchemaAssertReservations(t, s)
	before, identity := holdoutSchemaPrefixDigest(t, s), licenseSchemaDeployment(t, s)
	migrationRun(t, s, c, Apply)
	if holdoutSchemaPrefixDigest(t, s) != before || licenseSchemaDeployment(t, s) != identity {
		t.Fatal("85-to94 changed complete old rows/journal/identity")
	}
	holdoutSchemaAssertRows(t, s, holdoutSchemaSeed(t, s, r))
	holdoutSchemaAssertReservations(t, s)
	after := holdoutSchemaDataDigest(t, s)
	migrationRun(t, s, c, Apply)
	migrationRun(t, s, c, Readiness)
	if holdoutSchemaDataDigest(t, s) != after {
		t.Fatal("upgraded no-op changed persisted graph")
	}
	t.Log("HOLDOUT_SCHEMA_POPULATED85_UPGRADE_PRESERVED old_business_journal_sha256=" + before)
}

func TestActualMySQLHoldoutSchemaConcurrentRegistration(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, holdoutSchemaStepCount)
	before := holdoutSchemaPrefixDigest(t, s)
	recipe := "synthetic-concurrent-" + uuid.NewString()
	values := []map[string]any{holdoutSchemaValues(r.trainingSchemaRows), holdoutSchemaValues(r.trainingSchemaRows)}
	queries := make([]string, 2)
	arguments := make([][]any, 2)
	for i := range values {
		values[i]["recipe_id"] = recipe
		queries[i], arguments[i] = holdoutSchemaInsert(t, values[i])
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	results := make(chan error, 2)
	for i := range values {
		go func(i int) {
			tx, e := s.Pool().BeginTx(t.Context(), nil)
			ready <- struct{}{}
			<-start
			if e != nil {
				results <- e
				return
			}
			defer tx.Rollback()
			_, e = tx.ExecContext(t.Context(), queries[i], arguments[i]...)
			if e == nil {
				_, e = tx.ExecContext(t.Context(), `INSERT INTO model_holdout_sources(tenant_id,workspace_id,holdout_id,source_sha256) VALUES(?,?,?,?)`, r.tenant, r.workspace, values[i]["id"], []byte(strings.Repeat("c", 32)))
			}
			if e == nil {
				_, e = tx.ExecContext(t.Context(), `INSERT INTO model_holdout_prompts(tenant_id,workspace_id,holdout_id,prompt_sha256) VALUES(?,?,?,?)`, r.tenant, r.workspace, values[i]["id"], []byte(strings.Repeat("c", 32)))
			}
			if e == nil {
				e = tx.Commit()
			}
			results <- e
		}(i)
	}
	<-ready
	<-ready
	close(start)
	winners, duplicates := 0, 0
	for range values {
		e := <-results
		if e == nil {
			winners++
			continue
		}
		var safe *mysqlstore.Error
		if errors.As(mysqlstore.SafeError(e), &safe) && safe.Number == 1062 {
			duplicates++
		} else {
			t.Fatal("concurrent holdout unexpected safe failure", mysqlstore.SafeError(e))
		}
	}
	if winners != 1 || duplicates != 1 {
		t.Fatal("concurrent version/recipe must have one committed winner")
	}
	var parents, sources, prompts int
	query := `SELECT COUNT(*),SUM((SELECT COUNT(*) FROM model_holdout_sources s WHERE s.tenant_id=h.tenant_id AND s.workspace_id=h.workspace_id AND s.holdout_id=h.id)),SUM((SELECT COUNT(*) FROM model_holdout_prompts p WHERE p.tenant_id=h.tenant_id AND p.workspace_id=h.workspace_id AND p.holdout_id=h.id)) FROM model_holdouts h WHERE h.tenant_id=? AND h.workspace_id=? AND h.recipe_id=?`
	if e := s.Pool().QueryRowContext(t.Context(), query, r.tenant, r.workspace, recipe).Scan(&parents, &sources, &prompts); e != nil || parents != 1 || sources != 1 || prompts != 1 || holdoutSchemaPrefixDigest(t, s) != before {
		t.Fatal("concurrent parent/children were not atomic", mysqlstore.SafeError(e))
	}
	migrationRun(t, s, c, Readiness)
}
