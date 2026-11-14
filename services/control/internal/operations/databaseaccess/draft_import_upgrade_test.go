package databaseaccess

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatasetDraftImportUpgradeResumesExistingRuntimePrincipal(t *testing.T) {
	f, runtime := resumeTableGrantUpgrade(t, 59, "dataset_draft_import_candidates")
	ctx := t.Context()
	data, draft := uuid.NewString(), uuid.NewString()
	// Seed an ordinary predecessor-shaped draft through the owning operator.
	// These synthetic rows test runtime SQL grants, not an uploaded artifact.
	if changed, err := f.lease.Exec(ctx, `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint)
 SELECT $1,tenant_id,default_workspace_id,'Grant dataset','grant dataset','Synthetic fixture','CC0-1.0',true,primary_admin_user_id,$2,$3 FROM installation_state WHERE id=1 AND bootstrap_complete`, data, uuid.NewString(), make([]byte, 32)); err != nil || changed.RowsAffected() != 1 {
		t.Fatal("runtime grant dataset fixture", err)
	}
	if changed, err := f.lease.Exec(ctx, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,state,creation_key,creation_fingerprint)
 SELECT $1,tenant_id,default_workspace_id,$2,primary_admin_user_id,'admin',1,$3,'grant.jsonl',repeat('a',64),2,'uploading',$4,$5 FROM installation_state WHERE id=1 AND bootstrap_complete`, draft, data, uuid.NewString(), uuid.NewString(), make([]byte, 32)); err != nil || changed.RowsAffected() != 1 {
		t.Fatal("runtime grant draft fixture", err)
	}
	const insert = `INSERT INTO dataset_draft_import_candidates(tenant_id,workspace_id,dataset_id,draft_id,site_id,cell_id,source_format,report)
 SELECT tenant_id,workspace_id,dataset_id,$2,site_id,cell_id,'csv','{"source_bytes":42,"output_bytes":31}'::jsonb FROM dataset_drafts WHERE id=$1`
	if changed, err := runtime.Exec(ctx, insert, draft, draft); err != nil || changed.RowsAffected() != 1 {
		t.Fatal("runtime scoped receipt insert", err)
	}
	var bytes string
	if err := runtime.QueryRow(ctx, "SELECT report->>'source_bytes' FROM dataset_draft_import_candidates WHERE draft_id=$1", draft).Scan(&bytes); err != nil || bytes != "42" {
		t.Fatal("runtime receipt read", err)
	}
	if changed, err := runtime.Exec(ctx, "UPDATE dataset_draft_import_candidates SET report=report WHERE draft_id=$1", draft); err != nil || changed.RowsAffected() != 1 {
		t.Fatal("runtime receipt update", err)
	}
	if changed, err := runtime.Exec(ctx, "DELETE FROM dataset_draft_import_candidates WHERE draft_id=$1", draft); err != nil || changed.RowsAffected() != 1 {
		t.Fatal("runtime receipt delete", err)
	}
	_, err := runtime.Exec(ctx, insert, draft, uuid.NewString())
	var foreignKey *pgconn.PgError
	if !errors.As(err, &foreignKey) || foreignKey.Code != "23503" || foreignKey.ConstraintName != "dataset_draft_import_candidate_scope_fk" {
		t.Fatal("runtime insert did not retain the six-column draft scope fence", err)
	}
}
