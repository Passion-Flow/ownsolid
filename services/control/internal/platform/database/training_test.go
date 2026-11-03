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
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestM09ActualSchema8UpgradeRollbackAndPreservedInferenceReservation(t *testing.T) {
	s := fixture.NewM01(t)
	fixture.Bootstrap(t, s)
	ctx := context.Background()
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
		if version <= 1 || version >= 9 {
			continue
		}
		raw, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if _, e = s.DB.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", version, hex.EncodeToString(sum[:])); e != nil {
			t.Fatal(e)
		}
	}
	var tenant, workspace, user string
	if e = s.DB.QueryRow(ctx, "SELECT tenant_id::text,default_workspace_id::text FROM installation_state WHERE id=1").Scan(&tenant, &workspace); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT id::text FROM users LIMIT 1").Scan(&user); e != nil {
		t.Fatal(e)
	}
	node, model, deployment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	gpu := "GPU-" + uuid.NewString()
	// Synthetic historical rows verify physical reservation identity; no hardware capability is asserted.
	if _, e = s.DB.Exec(ctx, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES($1,$2,$3,'Synthetic historical GPU','active',decode(repeat('ab',32),'hex'),'1.3','{}',clock_timestamp(),'docker_inference','[]')`, node, tenant, workspace); e != nil {
		t.Fatal(e)
	}
	manifest, e := json.Marshal(wire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic historical model',$5,$6,$7,'ready',$4)`, model, tenant, workspace, user, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,created_by) VALUES($1,$2,$3,$4,$5,$6,'running',7,$7)`, deployment, tenant, workspace, node, model, gpu, user); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO gpu_leases(tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES($1,$2,$3,$4,$5,7,'recovery_required')`, tenant, workspace, node, gpu, deployment); e != nil {
		t.Fatal(e)
	}
	// A deliberate owned name conflict must roll back every preceding schema9 change.
	if _, e = s.DB.Exec(ctx, "CREATE TABLE training_jobs(id integer)"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB); e == nil {
		t.Fatal("conflicted schema9 reported success")
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); e != nil || count != 8 {
		t.Fatal("partial version metadata", count, e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='gpu_leases' AND column_name='id'").Scan(&count); e != nil || count != 0 {
		t.Fatal("partial reservation DDL", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TABLE training_jobs"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	var id, owner, state, preservedGPU string
	var revision int64
	var training *string
	if e = s.DB.QueryRow(ctx, "SELECT id::text,deployment_id::text,training_job_id::text,gpu_uuid,revision,state FROM gpu_leases").Scan(&id, &owner, &training, &preservedGPU, &revision, &state); e != nil {
		t.Fatal(e)
	}
	if id != deployment || owner != deployment || training != nil || preservedGPU != gpu || revision != 7 || state != "recovery_required" {
		t.Fatal("historical reservation changed")
	}
	var kind, catalog string
	var recipe *string
	if e = s.DB.QueryRow(ctx, "SELECT model_kind,catalog_digest,serving_recipe_sha256 FROM deployments WHERE id=$1", deployment).Scan(&kind, &catalog, &recipe); e != nil || kind != "approved_base" || catalog != wire.ModelCatalogDigest() || recipe != nil {
		t.Fatal("historical approved base identity not preserved", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE gpu_leases SET deployment_id=NULL WHERE id=$1", id); e == nil {
		t.Fatal("ownerless reservation accepted")
	}
	var duplicate int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('gpu_exclusive_node_idx','gpu_exclusive_uuid_idx')").Scan(&duplicate); e != nil || duplicate != 2 {
		t.Fatal("physical exclusivity indexes lost", e)
	}
	if e = database.Migrate(ctx, s.DB); e != nil {
		t.Fatal("idempotent upgrade", e)
	}
}
