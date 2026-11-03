package databaseaccess

// Reuse the existing native retirement/lifecycle-review SQL fixture contracts.
// Every real FK, CHECK and deferred trigger remains enabled. These synthetic
// metadata rows assert no physical artifact verification or human approval.
import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"strings"
	"testing"
	"time"
)

type mvpUpgradeDataRows struct{ tenant, workspace, user, dataset, draft, row, job, attempt, version string }
type mvpUpgradeReviewRows struct {
	mvpUpgradeDataRows
	node, model, training, trainingAttempt, trainingArtifact string
	holdout, preparation, attempt, upload, artifact          string
}

func mvpUpgradeSeedExec(t *testing.T, p *pgxpool.Pool, statement string, arguments ...any) {
	t.Helper()
	if _, e := p.Exec(t.Context(), statement, arguments...); e != nil {
		t.Fatal("synthetic dataset retirement prerequisite", e)
	}
}

func mvpUpgradeSeedDataset(t *testing.T, p *pgxpool.Pool) mvpUpgradeDataRows {
	t.Helper()
	r := mvpUpgradeDataRows{dataset: uuid.NewString(), draft: uuid.NewString(), row: uuid.NewString(), job: uuid.NewString(), attempt: uuid.NewString(), version: uuid.NewString()}
	if e := p.QueryRow(t.Context(), `SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1`).Scan(&r.tenant, &r.workspace, &r.user); e != nil {
		t.Fatal(e)
	}
	digest := strings.Repeat("a", 64)
	mvpUpgradeSeedExec(t, p, "INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,next_version,creation_key,creation_fingerprint) VALUES($1,$2,$3,'Synthetic retirement \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','synthetic retirement','Synthetic metadata only','Synthetic; no rights approval',true,$4,2,$5,$6)", r.dataset, r.tenant, r.workspace, r.user, uuid.NewString(), make([]byte, 32))
	mvpUpgradeSeedExec(t, p, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,source_received_at,state,total_rows,validation_completed_at,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'admin',1,$6,'synthetic.jsonl',$7,64,clock_timestamp(),'published',1,clock_timestamp(),$8,$9)`, r.draft, r.tenant, r.workspace, r.dataset, r.user, uuid.NewString(), digest, uuid.NewString(), make([]byte, 32))
	mvpUpgradeSeedExec(t, p, "INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion) VALUES($1,$2,$3,$4,$5,1,'synthetic','Prompt \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','Completion \u0627\u0644\u0639\u0631\u0628\u064a\u0629 \u4e16\u754c')", r.row, r.tenant, r.workspace, r.dataset, r.draft)
	mvpUpgradeSeedExec(t, p, `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason) VALUES($1,$2,$3,$4,$5,1,$6,'imported',$7,'Synthetic schema metadata')`, r.tenant, r.workspace, r.dataset, r.draft, r.row, r.user, digest)
	mvpUpgradeSeedExec(t, p, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,attempt_count,attempt_id,completed_at,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'publish',$6,'admin',1,'Synthetic publication',1,'succeeded',$7,$8,'{"synthetic_schema":true}',$9,1000,1,$10,clock_timestamp(),$11,$12)`, r.job, r.tenant, r.workspace, r.dataset, r.draft, r.user, r.version, digest, strings.Repeat("a", 32), r.attempt, uuid.NewString(), make([]byte, 32))
	mvpUpgradeSeedExec(t, p, `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state,finished_at,memory_enforcement) VALUES($1,$2,$3,$4,$5,$6,1,'succeeded',clock_timestamp(),'Synthetic metadata only')`, r.tenant, r.workspace, r.dataset, r.draft, r.job, r.attempt)
	mvpUpgradeSeedExec(t, p, "INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,published_by,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$9,$9,'{\"synthetic_schema\":true,\"label\":\"\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629\"}',$9)", r.version, r.tenant, r.workspace, r.dataset, r.draft, r.job, r.attempt, r.user, digest)
	mvpUpgradeSeedExec(t, p, `UPDATE dataset_drafts SET current_job_id=$1,published_version_id=$2 WHERE id=$3`, r.job, r.version, r.draft)
	return r
}

func mvpUpgradeSeedReview(t *testing.T, p *pgxpool.Pool) mvpUpgradeReviewRows {
	t.Helper()
	r := mvpUpgradeReviewRows{mvpUpgradeDataRows: mvpUpgradeSeedDataset(t, p),
		node: uuid.NewString(), model: uuid.NewString(), training: uuid.NewString(),
		trainingAttempt: uuid.NewString(), trainingArtifact: uuid.NewString(),
		holdout: uuid.NewString(), preparation: uuid.NewString(), attempt: uuid.NewString(),
		upload: uuid.NewString(), artifact: uuid.NewString()}
	digest, gpu, task := strings.Repeat("a", 64), "GPU-"+uuid.NewString(), uuid.NewString()
	mvpUpgradeSeedExec(t, p, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES($1,$2,$3,'Synthetic review metadata node','active',$4,'1.5','{}',clock_timestamp(),'docker_model_lifecycle','["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]')`, r.node, r.tenant, r.workspace, make([]byte, 32))
	mvpUpgradeSeedExec(t, p, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic review base',$4,$5,'{}','ready',$6)`, r.model, r.tenant, r.workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), r.user)
	mvpUpgradeSeedExec(t, p, `INSERT INTO training_jobs(id,tenant_id,workspace_id,node_id,current_node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,'{}',$9,'{}',$9,$9,$10,$10,'admin',1,'Synthetic SQL metadata only',$11,$12,'succeeded')`, r.training, r.tenant, r.workspace, r.node, r.model, r.dataset, r.version, gpu, digest, r.user, uuid.NewString(), make([]byte, 32))
	mvpUpgradeSeedExec(t, p, `INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory) VALUES($1,$2,$3,$4,$5,1,'succeeded',$6,'admin',1,'{}')`, r.trainingAttempt, r.tenant, r.workspace, r.training, r.node, r.user)
	mvpUpgradeSeedExec(t, p, `INSERT INTO training_uploads(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,state,manifest_bytes) VALUES($1,$2,$3,$4,$5,'artifact',1,$6,'{}','accepted',$7)`, r.trainingArtifact, r.tenant, r.workspace, r.training, r.trainingAttempt, digest, []byte("{}"))
	mvpUpgradeSeedExec(t, p, `INSERT INTO training_artifacts(id,tenant_id,workspace_id,job_id,attempt_id,manifest_sha256,manifest,file_inventory,storage_key) VALUES($1,$2,$3,$4,$5,$6,'{}','{}',$7)`, r.trainingArtifact, r.tenant, r.workspace, r.training, r.trainingAttempt, digest, "artifacts/"+r.tenant+"/"+r.workspace+"/"+r.trainingArtifact)
	mvpUpgradeSeedExec(t, p, `INSERT INTO model_holdouts(id,tenant_id,workspace_id,dataset_id,dataset_version_id,name,manifest_sha256,effective_source_sha256,row_mapping_sha256,source_bytes,row_count,label_counts,recipe_id,recipe_sha256,instruction_sha256,fingerprint_algorithm,created_by,audience,reason,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'Synthetic review holdout',$6,$6,$6,64,3,'{"account":1,"billing":1,"technical":1}','synthetic-metadata-only',$6,$6,'synthetic-metadata-only',$7,'admin','Synthetic SQL; no rights approval',$8,$9)`, r.holdout, r.tenant, r.workspace, r.dataset, r.version, digest, r.user, uuid.NewString(), make([]byte, 32))
	mvpUpgradeSeedExec(t, p, `INSERT INTO model_preparations(id,tenant_id,workspace_id,node_id,current_node_id,model_id,training_job_id,training_attempt_id,training_artifact_id,heldout_id,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by,requested_by,creation_audience,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,$9,$10,'{}',$11,'{}',$11,'{}',$12,$12,'admin','admin',1,'Synthetic SQL metadata only',$13,$14,'queued')`, r.preparation, r.tenant, r.workspace, r.node, r.model, r.training, r.trainingAttempt, r.trainingArtifact, r.holdout, gpu, digest, r.user, uuid.NewString(), make([]byte, 32))
	mvpUpgradeSeedExec(t, p, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,completed_at) VALUES($1,$2,$3,$4,'model.prepare.v1','succeeded',$5,$6,clock_timestamp(),clock_timestamp())`, task, r.tenant, r.workspace, r.node, uuid.NewString(), r.user)
	mvpUpgradeSeedExec(t, p, `INSERT INTO model_preparation_attempts(id,tenant_id,workspace_id,preparation_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,command_sha256) VALUES($1,$2,$3,$4,$5,$6,1,'prepared',$7,'admin',1,$8)`, r.attempt, r.tenant, r.workspace, r.preparation, r.node, task, r.user, digest)
	mvpUpgradeSeedExec(t, p, `INSERT INTO model_preparation_uploads(id,tenant_id,workspace_id,preparation_id,attempt_id,manifest_sha256,manifest,manifest_bytes,descriptor,state,accepted_at) VALUES($1,$2,$3,$4,$5,$6,'{}',$7,'{}','accepted',clock_timestamp())`, r.upload, r.tenant, r.workspace, r.preparation, r.attempt, digest, []byte("{}"))
	mvpUpgradeSeedExec(t, p, `INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path) VALUES($1,$2,$3,$4,$5,$6,$7,'{}','{}',$8,'synthetic-sql-metadata-only')`, r.artifact, r.tenant, r.workspace, r.preparation, r.attempt, r.upload, digest, []byte("{}"))
	return r
}

const mvpUpgradeReviewInsert = `INSERT INTO model_preparation_reviews(id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,audience,reason,idempotency_key,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$10,$11,'admin','Synthetic SQL integrity case; no human approval',$12,$13)`

func mvpUpgradeReviewArgs(r mvpUpgradeReviewRows, decision string, model any, name string) []any {
	return []any{uuid.NewString(), r.tenant, r.workspace, r.preparation, r.attempt, r.artifact,
		strings.Repeat("a", 64), decision, model, name, r.user, uuid.NewString(), make([]byte, 32)}
}

func mvpUpgradePublishReview(t *testing.T, p *pgxpool.Pool, r mvpUpgradeReviewRows) {
	t.Helper()
	arguments := mvpUpgradeReviewArgs(r, "approve", r.artifact, "Synthetic complete approval graph")
	tx, e := p.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	statements := []struct {
		query string
		args  []any
	}{
		{mvpUpgradeReviewInsert, arguments},
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

// Preserve the original actualFixture role/lease recipe while allowing the
// reviewed identityfixture setup-only service constructor to be selected.
func mvpPopulatedRoleFixture(t *testing.T, s *identity.Service) actualFixture {
	t.Helper()
	if s.DB.Config().ConnConfig.RuntimeParams["statement_timeout"] != "5000" {
		t.Fatal("identity business statement budget was not restored")
	}
	var actualBudget string
	if e := s.DB.QueryRow(t.Context(), "SHOW statement_timeout").Scan(&actualBudget); e != nil || actualBudget != "5s" {
		t.Fatal("actual identity business statement budget was not restored", actualBudget, e)
	}
	role := "m17_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password, e := NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	pc := s.DB.Config().Copy()
	pc.ConnConfig.User = role
	pc.ConnConfig.Password = password
	pc.MaxConns = 4
	pc.MinConns = 1
	c := s.Config
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	lease, e := s.DB.Acquire(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var locked bool
	if e = lease.QueryRow(t.Context(), "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		lease.Release()
		t.Fatal("original populated fixture installation lock", e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, e := lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
			t.Error("owned populated unlock", e)
		}
		if _, e := lease.Exec(ctx, "SET statement_timeout=5000"); e != nil {
			t.Error("restore original populated operator session budget", e)
		}
		lease.Release()
		if _, e := s.DB.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("owned populated grants cleanup", e)
		}
		if _, e := s.DB.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("owned populated role cleanup", e)
		}
	})
	return actualFixture{s, RuntimeInput{configuration: c, connection: pc}, lease, role}
}
