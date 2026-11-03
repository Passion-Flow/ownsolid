package databaseaccess

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Populated historical SQL variants exercise the actual conversion/backfill.
// These synthetic metadata fixtures claim no GPU, model execution or accepted
// physical artifact qualification; all original logical rows must be preserved.
func TestMVPPostgreSQLCPUV1PopulatedVariantsAndAliasBackfill(t *testing.T) {
	f := mvpPopulatedRoleFixture(t, identityfixture.NewM16WithSetup(t, identityfixture.SetupOptions{TimeoutSeconds: 180, StatementTimeoutSeconds: 30}))
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := f.lease.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec("SET statement_timeout=30000")
	if _, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil {
		t.Fatal(e)
	}
	identityfixture.Bootstrap(t, f.service)
	var tenant, workspace, user string
	if e := f.lease.QueryRow(ctx, "SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&tenant, &workspace, &user); e != nil {
		t.Fatal(e)
	}
	graph := mvpUpgradeSeedReview(t, f.service.DB)
	mvpUpgradePublishReview(t, f.service.DB, graph)
	type expectedTarget struct {
		alias, model, kind, digest string
		recipe                     any
	}
	missing := []expectedTarget{}
	for _, kind := range []string{"approved_base", "merged_lora"} {
		node, deployment, gateway := uuid.NewString(), uuid.NewString(), uuid.NewString()
		model := graph.model
		if kind == "merged_lora" {
			model = graph.artifact
		}
		digest := wire.ModelCatalogDigest()
		var recipe any
		if kind == "merged_lora" {
			digest = strings.Repeat("b", 64)
			recipe = strings.Repeat("c", 64)
		}
		hash := sha256.Sum256([]byte(node))
		exec(`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES($1,$2,$3,$4,'active',$5,'1.5','{}',clock_timestamp(),'docker_model_lifecycle','["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]')`, node, tenant, workspace, "Synthetic "+kind, hash[:])

		gpu := "GPU-" + uuid.NewString()
		exec(`INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,created_by,model_kind,catalog_digest,serving_recipe_sha256) VALUES($1,$2,$3,$4,$5,$6,'running',7,$7,$8,$9,$10)`, deployment, tenant, workspace, node, model, gpu, user, kind, digest, recipe)
		exec(`INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES($1,$2,$3,$4,$5,$1,7,'recovery_required')`, deployment, tenant, workspace, node, gpu)
		exec(`INSERT INTO gateway_instances(client_origin,id,tenant_id,workspace_id,node_id,name,control_origin_sha256,credential_hash,created_by,requested_epoch,idempotency_key,fingerprint) VALUES('https://gateway.example.invalid',$1,$2,$3,$4,'Synthetic retained gateway',$5,$6,$7,1,$8,$6)`, gateway, tenant, workspace, node, strings.Repeat("d", 64), hash[:], user, uuid.NewString())
		for _, existing := range []bool{true, false} {
			alias := uuid.NewString()
			name := kind + "-existing"
			if !existing {
				name = kind + "-backfill"
			}
			name = strings.ReplaceAll(name, "_", "-")
			exec(`INSERT INTO model_aliases(id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,created_by,idempotency_key,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,7,$8,$9,$10)`, alias, tenant, workspace, node, gateway, name, deployment, user, uuid.NewString(), hash[:])
			exec(`INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES($1,$2,$3,1,'published',$4,'Synthetic retained history')`, tenant, workspace, alias, user)
			if existing {
				exec(`INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest,serving_recipe_sha256) VALUES($1,$2,$3,$4,1,'published',$5,7,$6,$7,$8,$9)`, uuid.NewString(), tenant, workspace, alias, deployment, model, kind, digest, recipe)
			} else {
				missing = append(missing, expectedTarget{alias, model, kind, digest, recipe})
			}
		}
	}
	// A retained maintenance task/component exercises both the old scoped FK and
	// its canonical name, including nondefault prior version and observations.
	node, task := uuid.NewString(), uuid.NewString()
	hash := sha256.Sum256([]byte(node))
	exec(`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES($1,$2,$3,'Synthetic maintenance','active',$4,'1.2','{}',clock_timestamp(),'systemd_maintenance','["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]')`, node, tenant, workspace, hash[:])
	exec(`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,completed_at) VALUES($1,$2,$3,$4,'node_exporter.start.v1','succeeded',$5,$6,clock_timestamp(),clock_timestamp())`, task, tenant, workspace, node, uuid.NewString(), user)
	exec(`INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,last_task_id,applied_config,previous_version,last_verified_at) VALUES($1,$2,$3,'node_exporter','1.12.1','running','running',true,'{}',clock_timestamp(),$4,'{"log_level":"info","max_scrape_requests":40}','1.12.0',clock_timestamp())`, tenant, workspace, node, task)
	snapshot := func(table string) string {
		t.Helper()
		var raw string
		if e := f.lease.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb)::text FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&raw); e != nil {
			t.Fatal(e)
		}
		return raw
	}
	before := map[string]string{}
	for _, table := range runtimeTables(f.input) {
		before[table] = snapshot(table)
	}
	var oldSnapshots []map[string]any
	if e := json.Unmarshal([]byte(before["model_alias_target_versions"]), &oldSnapshots); e != nil {
		t.Fatal(e)
	}
	input := mvpRuntimeInput(t, f)
	for i := 0; i < 2; i++ {
		if state, e := UpgradeMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); e != nil || state != MVPPostgreSQLCPUV1UpgradeComplete {
			t.Fatal(state, e)
		}
	}
	for table, raw := range before {
		if table != "model_alias_target_versions" && snapshot(table) != raw {
			t.Fatal("historical typed rows changed", table)
		}
	}
	var all []map[string]any
	if e := json.Unmarshal([]byte(snapshot("model_alias_target_versions")), &all); e != nil {
		t.Fatal(e)
	}
	if len(all) != 4 {
		t.Fatal("backfill did not produce exact two absent snapshots", len(all))
	}
	for _, old := range oldSnapshots {
		found := false
		for _, current := range all {
			if current["id"] == old["id"] {
				a, _ := json.Marshal(old)
				b, _ := json.Marshal(current)
				if string(a) != string(b) {
					t.Fatal("old immutable snapshot changed")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("original snapshot ID lost")
		}
	}
	for _, want := range missing {
		found := false
		for _, row := range all {
			if row["alias_id"] == want.alias {
				if row["action"] != "migration_current" || row["model_id"] != want.model || row["model_kind"] != want.kind || row["catalog_digest"] != want.digest || row["serving_recipe_sha256"] != want.recipe {
					t.Fatal("backfill model identity mismatch")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("missing current snapshot not backfilled")
		}
	}
}
