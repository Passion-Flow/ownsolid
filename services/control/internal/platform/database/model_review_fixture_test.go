package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func modelReviewFrozen19(t *testing.T) *identity.Service {
	t.Helper()
	s := retirementFrozen18(t)
	for name, expected := range map[string]string{
		"000010_model_lifecycle.up.sql":           "970ce5f0b4a06bb34b531ea5484332d14b547ea66c3c08693181e144bc6e2484",
		"000019_dataset_retirement_reason.up.sql": "662444cdb343b16c610af6ee82bfcc8faca8d9822feff572ea4b2d93ee8b68cc",
	} {
		raw, e := migrations.Files.ReadFile("postgresql/" + name)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != expected {
			t.Fatal("immutable model review prerequisite changed", name)
		}
		if strings.HasPrefix(name, "000019_") {
			tx, e := s.DB.Begin(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(t.Context())
			if _, e = tx.Exec(t.Context(), string(raw)); e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(t.Context(), `INSERT INTO schema_migrations(version,checksum) VALUES(19,$1)`, expected); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(t.Context()); e != nil {
				t.Fatal(e)
			}
		}
	}
	var count, latest int
	if e := s.DB.QueryRow(t.Context(), `SELECT count(*),max(version) FROM schema_migrations`).Scan(&count, &latest); e != nil || count != 19 || latest != 19 {
		t.Fatal("complete frozen1-19 required", e)
	}
	return s
}

type modelReviewRows struct {
	retirementRows
	node, model, training, trainingAttempt, trainingArtifact string
	holdout, preparation, attempt, upload, artifact          string
}

// These are actual scoped SQL rows, not a trained model, uploaded file, reviewed
// result or hardware receipt. Every real CHECK, FK and trigger remains enabled.
func modelReviewSeed(t *testing.T, p *pgxpool.Pool) modelReviewRows {
	t.Helper()
	r := modelReviewRows{retirementRows: retirementSeed(t, p),
		node: uuid.NewString(), model: uuid.NewString(), training: uuid.NewString(),
		trainingAttempt: uuid.NewString(), trainingArtifact: uuid.NewString(),
		holdout: uuid.NewString(), preparation: uuid.NewString(), attempt: uuid.NewString(),
		upload: uuid.NewString(), artifact: uuid.NewString()}
	digest, gpu, task := strings.Repeat("a", 64), "GPU-"+uuid.NewString(), uuid.NewString()
	retirementExec(t, p, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES($1,$2,$3,'Synthetic review metadata node','active',$4,'1.4','{}',clock_timestamp(),'docker_model_lifecycle','[]')`, r.node, r.tenant, r.workspace, make([]byte, 32))
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

const modelReviewInsert = `INSERT INTO model_preparation_reviews(id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,audience,reason,idempotency_key,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$10,$11,'admin','Synthetic SQL integrity case; no human approval',$12,$13)`

func modelReviewArguments(r modelReviewRows, decision string, model any, name string) []any {
	return []any{uuid.NewString(), r.tenant, r.workspace, r.preparation, r.attempt, r.artifact,
		strings.Repeat("a", 64), decision, model, name, r.user, uuid.NewString(), make([]byte, 32)}
}

func modelReviewJournal(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	var value string
	if e := p.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(record) ORDER BY version)::text FROM schema_migrations AS record WHERE version<=19`).Scan(&value); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// The synthetic graph exercises the real deferred approval publication contract;
// it conveys no artifact verification, human approval or hardware qualification.
func modelReviewPublish(t *testing.T, p *pgxpool.Pool, r modelReviewRows) {
	t.Helper()
	arguments := modelReviewArguments(r, "approve", r.artifact, "Synthetic complete approval graph")
	tx, e := p.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	statements := []struct {
		query string
		args  []any
	}{
		{modelReviewInsert, arguments},
		{`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES($1,$2,$3,'Synthetic complete approval graph',$4,$5,'{}','ready',$6,'merged_lora')`, []any{r.artifact, r.tenant, r.workspace, wire.ApprovedModel().ModelKey, strings.Repeat("b", 64), r.user}},
		{`INSERT INTO model_derivations(model_id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,base_model_id,review_id,catalog_digest,catalog,report_sha256) VALUES($1,$2,$3,$4,$5,$1,$6,$7,$8,'{}',$9)`, []any{r.artifact, r.tenant, r.workspace, r.preparation, r.attempt, r.model, arguments[0], strings.Repeat("b", 64), strings.Repeat("a", 64)}},
		{`UPDATE model_preparations SET state='approved',attempt_number=1,current_attempt_id=$1,review_id=$2,derived_model_id=$3 WHERE id=$4`, []any{r.attempt, arguments[0], r.artifact, r.preparation}},
	}
	for _, statement := range statements {
		if _, e = tx.Exec(t.Context(), statement.query, statement.args...); e != nil {
			t.Fatal("synthetic complete review graph", e)
		}
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal("actual deferred review publication graph did not commit", e)
	}
	var count int
	if e = p.QueryRow(t.Context(), `SELECT count(*) FROM model_preparation_reviews r JOIN model_derivations d ON d.review_id=r.id AND d.model_id=r.model_id JOIN model_versions m ON m.id=d.model_id AND m.catalog_digest=d.catalog_digest JOIN model_preparations p ON p.id=d.preparation_id AND p.review_id=r.id AND p.derived_model_id=m.id WHERE r.decision='approve' AND p.state='approved' AND r.model_id=r.artifact_id AND m.id=$1`, r.artifact).Scan(&count); e != nil || count != 1 {
		t.Fatal("committed complete model review identity missing", e)
	}
}
