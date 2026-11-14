//go:build mysql_integration

package mysqlmigrate

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// SQL metadata and real restart/restore are verified. No gateway service,
// approved file ingestion, serving, GPU or human review is represented by seeds.
func aliasReferenceRun(t *testing.T, s *mysqlstore.Store, c Catalog, mode Mode) {
	t.Helper()
	options := testOptions()
	options.MaximumSteps = c.Steps()
	options.MaximumMetadataBytes = 1 << 20
	report, e := Run(t.Context(), s, c, options, mode, nil)
	if e != nil || !report.Ready || !report.LockReleased || report.AppliedSteps != c.Steps() {
		t.Fatal("actual alias reference migration", report, e)
	}
}
func aliasReferenceRecoverySeed(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	base := nodeSchemaSeedOld(t, s)
	r := nodeSchemaSeedRows(t, s, base)
	model, dep, gateway, alias := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	manifest, e := json.Marshal(wire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	migrationExec(t, s, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic approved catalog metadata','qwen3-0.6b',?,CAST(? AS JSON),'ready',?)`, model, r.tenant, r.workspace, wire.ModelCatalogDigest(), manifest, r.user)
	migrationExec(t, s, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,created_by) VALUES(?,?,?,?,?,?,'stopped',1,?)`, dep, r.tenant, r.workspace, r.node, model, "GPU-"+uuid.NewString(), r.user)
	migrationExec(t, s, `INSERT INTO gateway_instances(id,tenant_id,workspace_id,node_id,name,client_origin,control_origin_sha256,credential_hash,created_by,requested_epoch,idempotency_key,fingerprint) VALUES(?,?,?,?,'Synthetic retained gateway','https://synthetic.invalid',?,?,?,1,?,?)`, gateway, r.tenant, r.workspace, r.node, wire.ModelCatalogDigest(), make([]byte, 32), r.user, uuid.NewString(), make([]byte, 32))
	migrationExec(t, s, `INSERT INTO model_aliases(id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,revision,created_by,idempotency_key,fingerprint) VALUES(?,?,?,?,?,'synthetic',?,1,1,?,?,?)`, alias, r.tenant, r.workspace, r.node, gateway, dep, r.user, uuid.NewString(), make([]byte, 32))
	migrationExec(t, s, `INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES(?,?,?,1,'published',?,'Synthetic persisted reference graph')`, r.tenant, r.workspace, alias, r.user)
	migrationExec(t, s, `INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest) VALUES(?,?,?,?,1,'published',?,1,?,'approved_base',?)`, uuid.NewString(), r.tenant, r.workspace, alias, dep, model, wire.ModelCatalogDigest())
}
func aliasReferenceRecoveryReadback(t *testing.T, s *mysqlstore.Store, c Catalog) {
	t.Helper()
	aliasReferenceRun(t, s, c, Readiness)
	for _, table := range []string{"gateway_instances", "model_aliases", "model_alias_history", "model_alias_target_versions", "deployments", "model_versions"} {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != 1 {
			t.Fatal("nonempty retained alias graph", table, count, mysqlstore.SafeError(e))
		}
	}
	var exact int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_alias_target_versions v JOIN model_aliases a ON a.tenant_id=v.tenant_id AND a.workspace_id=v.workspace_id AND a.id=v.alias_id AND a.revision=v.revision JOIN deployments d ON d.tenant_id=v.tenant_id AND d.workspace_id=v.workspace_id AND d.id=v.deployment_id AND d.node_id=a.node_id AND d.model_id=v.model_id AND d.model_kind=v.model_kind AND d.catalog_digest=v.catalog_digest JOIN gateway_instances g ON g.tenant_id=a.tenant_id AND g.workspace_id=a.workspace_id AND g.id=a.gateway_id AND g.node_id=a.node_id WHERE g.active_node_id=a.node_id`).Scan(&exact); e != nil || exact != 1 {
		t.Fatal("exact recovered alias/model/gateway scope", mysqlstore.SafeError(e))
	}
	digest := publicationSchemaDigest(t, s.Pool(), publicationSchemaLayout(t, s.Pool()), false)
	t.Logf("MYSQL_ALIAS_REFERENCE_DATA_SHA256=%s", digest)
}
func TestActualMySQLAliasReferenceSchemaRecovery(t *testing.T) {
	for _, control := range []string{"prepare_complete_graph", "after_restart_readback", "fresh_logical_restore_readback", "drift_reject_restore"} {
		t.Run(control, func(t *testing.T) {
			s, c, _ := actualMigration(t)
			if c.Steps() != 149 {
				t.Fatal("exact149 alias recovery profile required")
			}
			if control == "prepare_complete_graph" {
				aliasReferenceRun(t, s, c, Apply)
				aliasReferenceRecoverySeed(t, s)
			}
			if control == "drift_reject_restore" {
				before := publicationSchemaDigest(t, s.Pool(), publicationSchemaLayout(t, s.Pool()), false)
				var source string
				for _, entry := range c.steps {
					if entry.Object == "alias_target_snapshot" {
						source = entry.sql
					}
				}
				if source == "" {
					t.Fatal("owned exact trigger repair source required")
				}
				migrationExec(t, s, "DROP TRIGGER alias_target_snapshot")
				pending := true
				t.Cleanup(func() {
					if pending {
						if _, e := s.Pool().ExecContext(t.Context(), source); e != nil {
							t.Error("owned trigger repair", mysqlstore.SafeError(e))
						}
					}
				})
				options := testOptions()
				options.MaximumSteps = c.Steps()
				options.MaximumMetadataBytes = 1 << 20
				report, e := Run(t.Context(), s, c, options, Readiness, nil)
				if e == nil || e.Error() != CodeDefinitionDrift || report.Ready {
					t.Fatal("missing owned trigger must refuse readiness", report, e)
				}
				migrationExec(t, s, source)
				pending = false
				if after := publicationSchemaDigest(t, s.Pool(), publicationSchemaLayout(t, s.Pool()), false); before != after {
					t.Fatal("drift repair changed complete business/journal rows")
				}
			}
			aliasReferenceRecoveryReadback(t, s, c)
		})
	}
}
