package database_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func retirementFrozen18(t *testing.T) *identity.Service {
	t.Helper()
	s := fixture.NewM01(t)
	frozen, e := migrations.Files.ReadFile("postgresql/000008_datasets.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	frozenHash := sha256.Sum256(frozen)
	if hex.EncodeToString(frozenHash[:]) != "25f65a1c1824b863ae6ca42f7123c025b101dcc903800cd4c9ad89ebd4f332f1" {
		t.Fatal("frozen dataset migration8 changed")
	}
	entries, e := fs.ReadDir(migrations.Files, "postgresql")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		var version int
		if _, e = fmt.Sscanf(entry.Name(), "%d_", &version); e != nil {
			t.Fatal(e)
		}
		if version <= 1 || version > 18 {
			continue
		}
		raw, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		tx, e := s.DB.Begin(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(t.Context(), string(raw)); e != nil {
			tx.Rollback(t.Context())
			t.Fatal("frozen schema prerequisite", version, e)
		}
		sum := sha256.Sum256(raw)
		if _, e = tx.Exec(t.Context(), `INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)`, version, hex.EncodeToString(sum[:])); e != nil {
			tx.Rollback(t.Context())
			t.Fatal(e)
		}
		if e = tx.Commit(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	var count, latest int
	if e = s.DB.QueryRow(t.Context(), `SELECT count(*),max(version) FROM schema_migrations`).Scan(&count, &latest); e != nil || count != 18 || latest != 18 {
		t.Fatal("complete frozen1–18 prerequisite required", e)
	}
	fixture.Bootstrap(t, s)
	return s
}

type retirementRows struct{ tenant, workspace, user, dataset, draft, row, job, attempt, version string }

func retirementExec(t *testing.T, p *pgxpool.Pool, statement string, arguments ...any) {
	t.Helper()
	if _, e := p.Exec(t.Context(), statement, arguments...); e != nil {
		t.Fatal("synthetic dataset retirement prerequisite", e)
	}
}

// The seven-table publication graph is real SQL metadata. It represents no
// uploaded artifact, Python execution, rights approval or hardware qualification.
func retirementSeed(t *testing.T, p *pgxpool.Pool) retirementRows {
	t.Helper()
	r := retirementRows{dataset: uuid.NewString(), draft: uuid.NewString(), row: uuid.NewString(), job: uuid.NewString(), attempt: uuid.NewString(), version: uuid.NewString()}
	if e := p.QueryRow(t.Context(), `SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1`).Scan(&r.tenant, &r.workspace, &r.user); e != nil {
		t.Fatal(e)
	}
	digest := strings.Repeat("a", 64)
	retirementExec(t, p, "INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,next_version,creation_key,creation_fingerprint) VALUES($1,$2,$3,'Synthetic retirement \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','synthetic retirement','Synthetic metadata only','Synthetic; no rights approval',true,$4,2,$5,$6)", r.dataset, r.tenant, r.workspace, r.user, uuid.NewString(), make([]byte, 32))
	retirementExec(t, p, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,source_received_at,state,total_rows,validation_completed_at,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'admin',1,$6,'synthetic.jsonl',$7,64,clock_timestamp(),'published',1,clock_timestamp(),$8,$9)`, r.draft, r.tenant, r.workspace, r.dataset, r.user, uuid.NewString(), digest, uuid.NewString(), make([]byte, 32))
	retirementExec(t, p, "INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion) VALUES($1,$2,$3,$4,$5,1,'synthetic','Prompt \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','Completion \u0627\u0644\u0639\u0631\u0628\u064a\u0629 \u4e16\u754c')", r.row, r.tenant, r.workspace, r.dataset, r.draft)
	retirementExec(t, p, `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason) VALUES($1,$2,$3,$4,$5,1,$6,'imported',$7,'Synthetic schema metadata')`, r.tenant, r.workspace, r.dataset, r.draft, r.row, r.user, digest)
	retirementExec(t, p, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,attempt_count,attempt_id,completed_at,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'publish',$6,'admin',1,'Synthetic publication',1,'succeeded',$7,$8,'{"synthetic_schema":true}',$9,1000,1,$10,clock_timestamp(),$11,$12)`, r.job, r.tenant, r.workspace, r.dataset, r.draft, r.user, r.version, digest, strings.Repeat("a", 32), r.attempt, uuid.NewString(), make([]byte, 32))
	retirementExec(t, p, `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state,finished_at,memory_enforcement) VALUES($1,$2,$3,$4,$5,$6,1,'succeeded',clock_timestamp(),'Synthetic metadata only')`, r.tenant, r.workspace, r.dataset, r.draft, r.job, r.attempt)
	retirementExec(t, p, "INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,published_by,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$9,$9,'{\"synthetic_schema\":true,\"label\":\"\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629\"}',$9)", r.version, r.tenant, r.workspace, r.dataset, r.draft, r.job, r.attempt, r.user, digest)
	retirementExec(t, p, `UPDATE dataset_drafts SET current_job_id=$1,published_version_id=$2 WHERE id=$3`, r.job, r.version, r.draft)
	return r
}

func retirementBusinessDigest(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	rows, e := p.Query(t.Context(), `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') AND c.relname<>'schema_migrations' ORDER BY c.relname`)
	if e != nil {
		t.Fatal(e)
	}
	var tables []string
	for rows.Next() {
		var table string
		if e = rows.Scan(&table); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		tables = append(tables, table)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		t.Fatal(e)
	}
	parts := make([]string, 0, len(tables)*2)
	for _, table := range tables {
		var value string
		if e = p.QueryRow(t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(record) ORDER BY to_jsonb(record)::text)::text,'[]') FROM `+pgx.Identifier{table}.Sanitize()+` AS record`).Scan(&value); e != nil {
			t.Fatal(e)
		}
		parts = append(parts, table, value)
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func retirementJournalPrefix(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	var value string
	if e := p.QueryRow(context.Background(), `SELECT jsonb_agg(to_jsonb(record) ORDER BY version)::text FROM schema_migrations AS record WHERE version<=18`).Scan(&value); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
