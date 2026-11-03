package database_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"testing"
)

func lifecycleReviewSeed(t *testing.T, p *pgxpool.Pool) modelReviewRows {
	t.Helper()
	r := modelReviewRows{retirementRows: retirementSeed(t, p),
		node: uuid.NewString(), model: uuid.NewString(), training: uuid.NewString(),
		trainingAttempt: uuid.NewString(), trainingArtifact: uuid.NewString(),
		holdout: uuid.NewString(), preparation: uuid.NewString(), attempt: uuid.NewString(),
		upload: uuid.NewString(), artifact: uuid.NewString()}
	digest, gpu, task := strings.Repeat("a", 64), "GPU-"+uuid.NewString(), uuid.NewString()
	retirementExec(t, p, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES($1,$2,$3,'Synthetic review metadata node','active',$4,'1.5','{}',clock_timestamp(),'docker_model_lifecycle','["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]')`, r.node, r.tenant, r.workspace, make([]byte, 32))
	retirementExec(t, p, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic review base',$4,$5,'{}','ready',$6)`, r.model, r.tenant, r.workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), r.user)
	retirementExec(t, p, `INSERT INTO training_jobs(id,tenant_id,workspace_id,node_id,current_node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,'{}',$9,'{}',$9,$9,$10,$10,'admin',1,'Synthetic SQL metadata only',$11,$12,'succeeded')`, r.training, r.tenant, r.workspace, r.node, r.model, r.dataset, r.version, gpu, digest, r.user, uuid.NewString(), make([]byte, 32))
	retirementExec(t, p, `INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory) VALUES($1,$2,$3,$4,$5,1,'succeeded',$6,'admin',1,'{}')`, r.trainingAttempt, r.tenant, r.workspace, r.training, r.node, r.user)
	retirementExec(t, p, `INSERT INTO training_uploads(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,state,manifest_bytes) VALUES($1,$2,$3,$4,$5,'artifact',1,$6,'{}','accepted',$7)`, r.trainingArtifact, r.tenant, r.workspace, r.training, r.trainingAttempt, digest, []byte("{}"))
	retirementExec(t, p, `INSERT INTO training_artifacts(id,tenant_id,workspace_id,job_id,attempt_id,manifest_sha256,manifest,file_inventory,storage_key) VALUES($1,$2,$3,$4,$5,$6,'{}','{}',$7)`, r.trainingArtifact, r.tenant, r.workspace, r.training, r.trainingAttempt, digest, "artifacts/"+r.tenant+"/"+r.workspace+"/"+r.trainingArtifact)
	retirementExec(t, p, `INSERT INTO model_holdouts(id,tenant_id,workspace_id,dataset_id,dataset_version_id,name,manifest_sha256,effective_source_sha256,row_mapping_sha256,source_bytes,row_count,label_counts,recipe_id,recipe_sha256,instruction_sha256,fingerprint_algorithm,created_by,audience,reason,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'Synthetic review holdout',$6,$6,$6,64,3,'{"account":1,"billing":1,"technical":1}','synthetic-metadata-only',$6,$6,'synthetic-metadata-only',$7,'admin','Synthetic SQL; no rights approval',$8,$9)`, r.holdout, r.tenant, r.workspace, r.dataset, r.version, digest, r.user, uuid.NewString(), make([]byte, 32))
	retirementExec(t, p, `INSERT INTO model_preparations(id,tenant_id,workspace_id,node_id,current_node_id,model_id,training_job_id,training_attempt_id,training_artifact_id,heldout_id,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by,requested_by,creation_audience,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,$9,$10,'{}',$11,'{}',$11,'{}',$12,$12,'admin','admin',1,'Synthetic SQL metadata only',$13,$14,'queued')`, r.preparation, r.tenant, r.workspace, r.node, r.model, r.training, r.trainingAttempt, r.trainingArtifact, r.holdout, gpu, digest, r.user, uuid.NewString(), make([]byte, 32))
	retirementExec(t, p, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,completed_at) VALUES($1,$2,$3,$4,'model.prepare.v1','succeeded',$5,$6,clock_timestamp(),clock_timestamp())`, task, r.tenant, r.workspace, r.node, uuid.NewString(), r.user)
	retirementExec(t, p, `INSERT INTO model_preparation_attempts(id,tenant_id,workspace_id,preparation_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,command_sha256) VALUES($1,$2,$3,$4,$5,$6,1,'prepared',$7,'admin',1,$8)`, r.attempt, r.tenant, r.workspace, r.preparation, r.node, task, r.user, digest)
	retirementExec(t, p, `INSERT INTO model_preparation_uploads(id,tenant_id,workspace_id,preparation_id,attempt_id,manifest_sha256,manifest,manifest_bytes,descriptor,state,accepted_at) VALUES($1,$2,$3,$4,$5,$6,'{}',$7,'{}','accepted',clock_timestamp())`, r.upload, r.tenant, r.workspace, r.preparation, r.attempt, digest, []byte("{}"))
	retirementExec(t, p, `INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path) VALUES($1,$2,$3,$4,$5,$6,$7,'{}','{}',$8,'synthetic-sql-metadata-only')`, r.artifact, r.tenant, r.workspace, r.preparation, r.attempt, r.upload, digest, []byte("{}"))
	return r
}

func TestM10CumulativeReviewIdentityAndCompleteGraph(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			s := fixture.NewM10(t)
			fixture.Bootstrap(t, s)
			r := lifecycleReviewSeed(t, s.DB)
			for _, item := range []struct {
				decision, name string
				model          any
			}{{"approve", "Nonempty", nil}, {"approve", "Wrong", r.model}, {"approve", "", r.artifact}, {"reject", "Wrong", nil}, {"reject", "", r.model}} {
				before := retirementBusinessDigest(t, s.DB)
				_, e := s.DB.Exec(t.Context(), modelReviewInsert, modelReviewArguments(r, item.decision, item.model, item.name)...)
				var pg *pgconn.PgError
				if !errors.As(e, &pg) || pg.Code != "23514" {
					t.Fatal("current identity check", e)
				}
				if before != retirementBusinessDigest(t, s.DB) {
					t.Fatal("refused review changed current graph")
				}
			}
			if decision == "approve" {
				modelReviewPublish(t, s.DB, r)
			} else {
				args := modelReviewArguments(r, "reject", nil, "")
				tx, e := s.DB.Begin(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback(t.Context())
				if _, e = tx.Exec(t.Context(), modelReviewInsert, args...); e != nil {
					t.Fatal(e)
				}
				if _, e = tx.Exec(t.Context(), `UPDATE model_preparations SET state='rejected',attempt_number=1,current_attempt_id=$1,review_id=$2 WHERE id=$3`, r.attempt, args[0], r.preparation); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
