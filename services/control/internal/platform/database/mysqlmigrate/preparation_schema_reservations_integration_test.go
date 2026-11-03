//go:build mysql_integration

package mysqlmigrate

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

const preparationSchemaInsertLease = `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,training_job_id,model_preparation_id,revision,state) VALUES(?,?,?,?,?,?,?,?,1,?)`

func preparationSchemaLeaseValues(t *testing.T, r preparationSchemaRows, kind, owner, node, gpu, state string) []any {
	t.Helper()
	values := []any{uuid.NewString(), r.tenant, r.workspace, node, gpu, nil, nil, nil, state}
	switch kind {
	case "inference":
		values[5] = owner
	case "training":
		values[6] = owner
	case "preparation":
		values[7] = owner
	default:
		t.Fatal("explicit synthetic reservation owner kind required")
	}
	return values
}

func preparationSchemaLeaseOwner(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows, kind, node, gpu string) string {
	t.Helper()
	switch kind {
	case "inference":
		id := uuid.NewString()
		migrationExec(t, s, modelInferenceInsertDeployment, id, r.tenant, r.workspace, node, r.model, gpu, r.user, r.heartbeat)
		return id
	case "training":
		base := r.datasetSchemaRows
		base.node, base.gpu = node, gpu
		return trainingSchemaSeed(t, s, base).training
	case "preparation":
		values := preparationSchemaCopyValues(t, s, r, "model_preparations")
		values["node_id"], values["current_node_id"], values["gpu_uuid"] = node, node, gpu
		preparationSchemaInsertCopy(t, s, r, "model_preparations", values)
		return values["id"].(string)
	}
	t.Fatal("owned reservation owner kind required")
	return ""
}

func preparationSchemaUnusedNode(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows) string {
	t.Helper()
	id := uuid.NewString()
	migrationExec(t, s, nodeSchemaInsertNode, id, r.tenant, r.workspace, "Synthetic reservation node "+id, []byte(uuid.NewString())[:32])
	return id
}

func TestActualMySQLPreparationSchemaReservations(t *testing.T) {
	s, c, _ := actualMigration(t)
	c.steps = c.steps[:103]
	migrationRun(t, s, c, Apply)
	r := preparationSchemaSeed(t, s, holdoutSchemaSeed(t, s, holdoutSchemaReservationSeed(t, s)))
	var inferenceNode, inferenceGPU string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT node_id,gpu_uuid FROM gpu_leases WHERE deployment_id=? AND state='held'`, r.deployment).Scan(&inferenceNode, &inferenceGPU); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	freeNode, freeGPU := preparationSchemaUnusedNode(t, s, r), "GPU-"+uuid.NewString()
	unusedInference := preparationSchemaLeaseOwner(t, s, r, "inference", freeNode, freeGPU)
	unusedTraining := preparationSchemaLeaseOwner(t, s, r, "training", freeNode, freeGPU)
	for _, item := range []struct {
		name                             string
		inference, training, preparation any
		number                           uint16
	}{
		{"owner_none", nil, nil, nil, 3819},
		{"owner_inference_and_preparation", unusedInference, nil, r.preparation, 3819},
		{"owner_training_and_preparation", nil, unusedTraining, r.preparation, 3819},
		{"owner_all_three", unusedInference, unusedTraining, r.preparation, 3819},
		{"missing_preparation_owner", nil, nil, uuid.NewString(), 1452},
	} {
		t.Run(item.name, func(t *testing.T) {
			preparationSchemaRefuses(t, s, item.number, preparationSchemaInsertLease, uuid.NewString(), r.tenant, r.workspace, freeNode, freeGPU, item.inference, item.training, item.preparation, "held")
		})
	}
	install := func(t *testing.T, state string) string {
		values := preparationSchemaLeaseValues(t, r, "preparation", r.preparation, r.preparationNode, r.preparationGPU, state)
		migrationExec(t, s, preparationSchemaInsertLease, values...)
		return values[0].(string)
	}
	t.Run("one_retained_preparation_lease", func(t *testing.T) {
		id := install(t, "held")
		defer migrationExec(t, s, `DELETE FROM gpu_leases WHERE id=?`, id)
		preparationSchemaRefuses(t, s, 1062, preparationSchemaInsertLease, preparationSchemaLeaseValues(t, r, "preparation", r.preparation, freeNode, freeGPU, "released")...)
		var actual string
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT id FROM gpu_leases WHERE model_preparation_id=?`, r.preparation).Scan(&actual); e != nil || actual != id || actual == r.preparation {
			t.Fatal("independent retained preparation lease identity required", mysqlstore.SafeError(e))
		}
	})
	for _, item := range []struct{ name, node, gpu string }{
		{"held_inference_excludes_preparation_node", inferenceNode, freeGPU},
		{"held_inference_excludes_preparation_GPU", freeNode, inferenceGPU},
		{"recovery_training_excludes_preparation_node", r.node, freeGPU},
		{"recovery_training_excludes_preparation_GPU", freeNode, r.gpu},
	} {
		t.Run(item.name, func(t *testing.T) {
			owner := preparationSchemaLeaseOwner(t, s, r, "preparation", item.node, item.gpu)
			preparationSchemaRefuses(t, s, 1062, preparationSchemaInsertLease, preparationSchemaLeaseValues(t, r, "preparation", owner, item.node, item.gpu, "held")...)
		})
	}
	for _, item := range []struct{ name, kind, state, node, gpu string }{
		{"held_preparation_excludes_inference_node", "inference", "held", r.preparationNode, freeGPU},
		{"held_preparation_excludes_training_node", "training", "held", r.preparationNode, freeGPU},
		{"recovery_preparation_keeps_node", "inference", "recovery_required", r.preparationNode, freeGPU},
		{"recovery_preparation_keeps_GPU", "inference", "recovery_required", freeNode, r.preparationGPU},
	} {
		t.Run(item.name, func(t *testing.T) {
			id := install(t, item.state)
			defer migrationExec(t, s, `DELETE FROM gpu_leases WHERE id=?`, id)
			owner := preparationSchemaLeaseOwner(t, s, r, item.kind, item.node, item.gpu)
			preparationSchemaRefuses(t, s, 1062, preparationSchemaInsertLease, preparationSchemaLeaseValues(t, r, item.kind, owner, item.node, item.gpu, "held")...)
		})
	}
	t.Run("released_preparation_allows_owner", func(t *testing.T) {
		id := install(t, "recovery_required")
		defer migrationExec(t, s, `DELETE FROM gpu_leases WHERE id=?`, id)
		migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE id=?`, id)
		owner := preparationSchemaLeaseOwner(t, s, r, "training", r.preparationNode, r.preparationGPU)
		values := preparationSchemaLeaseValues(t, r, "training", owner, r.preparationNode, r.preparationGPU, "held")
		migrationExec(t, s, preparationSchemaInsertLease, values...)
		defer migrationExec(t, s, `DELETE FROM gpu_leases WHERE id=?`, values[0])
		var released, held int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM gpu_leases WHERE id=? AND model_preparation_id=? AND state='released' AND reserved_node_id IS NULL AND reserved_gpu_uuid IS NULL`, id, r.preparation).Scan(&released); e != nil || released != 1 {
			t.Fatal("explicitly released retained identity required", mysqlstore.SafeError(e))
		}
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM gpu_leases WHERE id=? AND training_job_id=? AND state='held' AND reserved_node_id=? AND reserved_gpu_uuid=?`, values[0], owner, r.preparationNode, r.preparationGPU).Scan(&held); e != nil || held != 1 {
			t.Fatal("released synthetic reservation did not admit explicit next owner", mysqlstore.SafeError(e))
		}
	})
	for _, successor := range []bool{false, true} {
		name := "uncleared_attempt_unique"
		if successor {
			name = "stopped_attempt_allows_successor"
		}
		t.Run(name, func(t *testing.T) {
			first := preparationSchemaCopyValues(t, s, r, "model_preparation_attempts")
			first["state"], first["physical_stop_receipt"], first["physical_stop_sha256"], first["physical_stopped_at"] = "pending", nil, nil, nil
			preparationSchemaInsertCopy(t, s, r, "model_preparation_attempts", first)
			defer migrationExec(t, s, `DELETE FROM model_preparation_attempts WHERE id=?`, first["id"])
			second := preparationSchemaCopyValues(t, s, r, "model_preparation_attempts")
			second["state"], second["physical_stop_receipt"], second["physical_stop_sha256"], second["physical_stopped_at"] = "pending", nil, nil, nil
			query, arguments := preparationSchemaCopySQL(t, s, r, "model_preparation_attempts", second)
			if !successor {
				preparationSchemaRefuses(t, s, 1062, query, arguments...)
				return
			}
			// Synthetic SQL proof only; no actual process was stopped or released.
			migrationExec(t, s, `UPDATE model_preparation_attempts SET physical_stop_receipt=JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),physical_stop_sha256=?,physical_stopped_at=? WHERE id=?`, hash(`{"synthetic_schema":true}`), r.heartbeat, first["id"])
			preparationSchemaInsertCopy(t, s, r, "model_preparation_attempts", second)
			defer migrationExec(t, s, `DELETE FROM model_preparation_attempts WHERE id=?`, second["id"])
			var count, uncleared int
			if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*),SUM(uncleared_preparation_id IS NOT NULL) FROM model_preparation_attempts WHERE id IN (?,?)`, first["id"], second["id"]).Scan(&count, &uncleared); e != nil || count != 2 || uncleared != 1 {
				t.Fatal("stopped metadata proof did not admit exactly one successor", mysqlstore.SafeError(e))
			}
		})
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLPreparationSchemaConcurrentAttempts(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, preparationSchemaStepCount)
	queries := make([]string, 2)
	arguments := make([][]any, 2)
	ids := make([]string, 2)
	for index := range 2 {
		values := preparationSchemaCopyValues(t, s, r, "model_preparation_attempts")
		values["attempt_number"], values["state"] = int64(index+2), "pending"
		values["physical_stop_receipt"], values["physical_stop_sha256"], values["physical_stopped_at"] = nil, nil, nil
		values["outcome"], values["outcome_sha256"], values["outcome_bytes"] = nil, nil, nil
		values["last_event_sequence"], values["event_bytes"] = int64(0), int64(0)
		ids[index] = values["id"].(string)
		queries[index], arguments[index] = preparationSchemaCopySQL(t, s, r, "model_preparation_attempts", values)
	}
	before := holdoutSchemaDataDigest(t, s)
	start, ready, results := make(chan struct{}), make(chan struct{}, 2), make(chan error, 2)
	for index := range 2 {
		go func(index int) {
			tx, e := s.Pool().BeginTx(t.Context(), nil)
			ready <- struct{}{}
			<-start
			if e != nil {
				results <- e
				return
			}
			defer tx.Rollback()
			_, e = tx.ExecContext(t.Context(), queries[index], arguments[index]...)
			if e == nil {
				_, e = tx.ExecContext(t.Context(), `INSERT INTO model_preparation_events(tenant_id,workspace_id,preparation_id,attempt_id,sequence,kind,payload,canonical_sha256) VALUES(?,?,?,?,1,'phase',JSON_OBJECT(),?)`, r.tenant, r.workspace, r.preparation, ids[index], hash("{}"))
			}
			if e == nil {
				e = tx.Commit()
			}
			if e != nil {
				if rollback := tx.Rollback(); rollback != nil && !errors.Is(rollback, sql.ErrTxDone) {
					e = rollback
				}
			}
			results <- e
		}(index)
	}
	<-ready
	<-ready
	close(start)
	winners, conflicts := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			winners++
			continue
		}
		var safe *mysqlstore.Error
		if errors.As(mysqlstore.SafeError(e), &safe) && safe.Number == 1062 {
			conflicts++
		} else {
			t.Fatal("actual concurrent preparation transaction", mysqlstore.SafeError(e))
		}
	}
	var attempts, events, uncleared int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*),COALESCE(SUM(uncleared_preparation_id IS NOT NULL),0) FROM model_preparation_attempts WHERE id IN (?,?)`, ids[0], ids[1]).Scan(&attempts, &uncleared); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_preparation_events e JOIN model_preparation_attempts a ON a.id=e.attempt_id AND a.preparation_id=e.preparation_id AND a.tenant_id=e.tenant_id AND a.workspace_id=e.workspace_id WHERE e.attempt_id IN (?,?)`, ids[0], ids[1]).Scan(&events); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if winners != 1 || conflicts != 1 || attempts != 1 || events != 1 || uncleared != 1 || holdoutSchemaDataDigest(t, s) != before {
		t.Fatal("concurrent preparation needs one complete durable graph, full loser rollback and unchanged old94 graph")
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLPreparationSchemaConcurrentReservations(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, preparationSchemaStepCount)
	for _, kind := range []string{"inference", "training", "preparation"} {
		for _, axis := range []string{"node", "GPU"} {
			t.Run(kind+"_"+axis, func(t *testing.T) {
				leftNode, leftGPU := preparationSchemaUnusedNode(t, s, r), "GPU-"+uuid.NewString()
				rightNode, rightGPU := leftNode, "GPU-"+uuid.NewString()
				if axis == "GPU" {
					rightNode, rightGPU = preparationSchemaUnusedNode(t, s, r), leftGPU
				}
				left := preparationSchemaLeaseOwner(t, s, r, "preparation", leftNode, leftGPU)
				right := preparationSchemaLeaseOwner(t, s, r, kind, rightNode, rightGPU)
				a := preparationSchemaLeaseValues(t, r, "preparation", left, leftNode, leftGPU, "held")
				b := preparationSchemaLeaseValues(t, r, kind, right, rightNode, rightGPU, "held")
				modelInferenceOneWinner(t, s, preparationSchemaInsertLease, a, b)
				var count, retained int
				if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*),COALESCE(SUM(state='held' AND reserved_node_id=node_id AND reserved_gpu_uuid=gpu_uuid),0) FROM gpu_leases WHERE id IN (?,?)`, a[0], b[0]).Scan(&count, &retained); e != nil || count != 1 || retained != 1 {
					t.Fatal("one exact durable physical reservation winner required", mysqlstore.SafeError(e))
				}
				// Explicitly release only the winner in this disposable synthetic
				// SQL namespace; never a live hardware or customer reservation.
				migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE id IN (?,?)`, a[0], b[0])
				var node, gpu sql.NullString
				if e := s.Pool().QueryRowContext(t.Context(), `SELECT reserved_node_id,reserved_gpu_uuid FROM gpu_leases WHERE id IN (?,?)`, a[0], b[0]).Scan(&node, &gpu); e != nil || node.Valid || gpu.Valid {
					t.Fatal("explicit synthetic winner release did not clear only active reservation", mysqlstore.SafeError(e))
				}
			})
		}
	}
	migrationRun(t, s, c, Readiness)
}
