//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Only synthetic relational metadata is created here. No gateway/model/GPU
// execution, production distribution API or target qualification is established.
func aliasSchemaGateway(id, node, user string) map[string]any {
	return map[string]any{"id": id, "node_id": node, "name": "Synthetic gateway", "client_origin": "https://synthetic.invalid", "control_origin_sha256": strings.Repeat("1", 64), "credential_hash": bytes.Repeat([]byte{9}, 32), "created_by": user, "requested_epoch": 1, "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{9}, 32)}
}
func aliasSchemaAlias(id, node, gateway, dep, user string) map[string]any {
	return map[string]any{"id": id, "node_id": node, "gateway_id": gateway, "name": "synthetic-" + id, "deployment_id": dep, "deployment_revision": 1, "revision": 1, "created_by": user, "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{9}, 32)}
}
func aliasSchemaHistory(alias, user string, revision int) map[string]any {
	return map[string]any{"alias_id": alias, "revision": revision, "action": "published", "actor_user_id": user, "reason": "Synthetic retained alias target"}
}
func aliasSchemaTarget(alias, dep, model string, revision int) map[string]any {
	return map[string]any{"id": uuid.NewString(), "alias_id": alias, "revision": revision, "action": "published", "deployment_id": dep, "deployment_revision": 1, "model_id": model, "model_kind": "approved_base", "catalog_digest": wire.ModelCatalogDigest()}
}
func aliasSchemaCount(t *testing.T, x modelReadFixture, table string, want int) {
	t.Helper()
	var n int
	if e := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.MySQL, "SELECT COUNT(*) FROM "+table)).Scan(&n); e != nil || n != want {
		t.Fatal("constrained graph count", table, n, want, e)
	}
}
func aliasSchemaReject(t *testing.T, x modelReadFixture, statement string, args ...any) {
	t.Helper()
	_, e := x.f.Store.Exec(x.ctx, transaction.SQL(transaction.MySQL, statement, args...))
	if e == nil || strings.Contains(e.Error(), "SYNTHETIC_PRIVATE") || strings.Contains(e.Error(), statement) {
		t.Fatal("invalid graph mutation must fail without vendor/data leakage", e)
	}
}
func aliasSchemaGraph(t *testing.T, x modelReadFixture, q transaction.Executor, node, dep, alias, gateway string) {
	t.Helper()
	retirementSeed(t, x, q, "gateway_instances", aliasSchemaGateway(gateway, node, x.f.User))
	retirementSeed(t, x, q, "model_aliases", aliasSchemaAlias(alias, node, gateway, dep, x.f.User))
	retirementSeed(t, x, q, "model_alias_history", aliasSchemaHistory(alias, x.f.User, 1))
	retirementSeed(t, x, q, "model_alias_target_versions", aliasSchemaTarget(alias, dep, x.model, 1))
}
func aliasSchemaSelected(t *testing.T, x modelReadFixture, alias string) (node, gateway, dep string) {
	t.Helper()
	if e := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.MySQL, "SELECT node_id,gateway_id,deployment_id FROM model_aliases WHERE id=?", alias)).Scan(&node, &gateway, &dep); e != nil {
		t.Fatal(e)
	}
	return
}
func TestActualModelAliasReferenceSchema(t *testing.T) {
	for _, name := range []string{"migration_prefix_apply_and_noop", "gateway_active_node_and_credential_state", "scoped_alias_foreign_keys_and_identity", "history_and_target_immutability", "target_snapshot_scope_and_rollback_source", "bounded_fields_and_canonical_identifiers", "transaction_rollback_preserves_graph"} {
		t.Run(name, func(t *testing.T) {
			x := retirementSetup(t)
			if x.f.Store.Dialect() != transaction.MySQL {
				t.Fatal("actual MySQL alias schema profile required")
			}
			switch name {
			case "migration_prefix_apply_and_noop":
				catalog, e := mysqlmigrate.DefaultCatalog()
				if e != nil || catalog.Steps() != 149 {
					t.Fatal("current measured alias schema required", e)
				}
				c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
				if e != nil {
					t.Fatal(e)
				}
				kernel, _, e := mysqlstore.Open(x.ctx, c)
				if e != nil {
					t.Fatal(e)
				}
				defer kernel.Close()
				options := mysqlmigrate.Options{LockWaitSeconds: 1, StepSeconds: 5, TotalSeconds: 20, CleanupSeconds: 2, MaximumSteps: catalog.Steps(), MaximumMetadataBytes: 1 << 20}
				events := []mysqlmigrate.Event{}
				report, e := mysqlmigrate.Run(x.ctx, kernel, catalog, options, mysqlmigrate.Apply, func(v mysqlmigrate.Event) { events = append(events, v) })
				if e != nil || !report.Ready || !report.LockReleased || report.AppliedSteps != 149 || report.TotalSteps != 149 || report.DirtyStep != 0 {
					t.Fatal("real migration no-op readback", report, e)
				}
				for _, event := range events {
					if event.Stage == mysqlmigrate.Applying || event.Stage == mysqlmigrate.Applied {
						t.Fatal("no-op must not repeat DDL")
					}
				}
			case "gateway_active_node_and_credential_state":
				node := retirementNode(t, x)
				id := uuid.NewString()
				retirementSeed(t, x, x.f.Store, "gateway_instances", aliasSchemaGateway(id, node, x.f.User))
				aliasSchemaReject(t, x, `INSERT INTO gateway_instances(id,tenant_id,workspace_id,node_id,name,client_origin,control_origin_sha256,credential_hash,created_by,requested_epoch,idempotency_key,fingerprint) SELECT ?,tenant_id,workspace_id,node_id,name,client_origin,control_origin_sha256,credential_hash,created_by,requested_epoch,?,fingerprint FROM gateway_instances WHERE id=?`, uuid.NewString(), uuid.NewString(), id)
				aliasSchemaReject(t, x, "UPDATE gateway_instances SET credential_hash=NULL WHERE id=?", id)
				aliasSchemaReject(t, x, "UPDATE gateway_instances SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='' WHERE id=?", id)
				x.exec(t, "", "UPDATE gateway_instances SET credential_hash=NULL,authority_invalidated_at=UTC_TIMESTAMP(6) WHERE id=?", id)
				retirementSeed(t, x, x.f.Store, "gateway_instances", aliasSchemaGateway(uuid.NewString(), node, x.f.User))
				aliasSchemaCount(t, x, "gateway_instances", 2)
			case "scoped_alias_foreign_keys_and_identity":
				alias := retirementAlias(t, x, false)
				_, _, dep := aliasSchemaSelected(t, x, alias)
				aliasSchemaReject(t, x, "UPDATE model_aliases SET name='SYNTHETIC_PRIVATE' WHERE id=?", alias)
				aliasSchemaReject(t, x, "UPDATE model_aliases SET created_by=? WHERE id=?", uuid.NewString(), alias)
				aliasSchemaReject(t, x, `INSERT INTO model_aliases(id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,revision,created_by,idempotency_key,fingerprint) SELECT ?,tenant_id,?,node_id,gateway_id,?,deployment_id,1,1,created_by,?,fingerprint FROM model_aliases WHERE id=?`, uuid.NewString(), uuid.NewString(), "synthetic-"+uuid.NewString(), uuid.NewString(), alias)
				aliasSchemaReject(t, x, `INSERT INTO model_aliases(id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,revision,created_by,idempotency_key,fingerprint) SELECT ?,tenant_id,workspace_id,node_id,?, ?,?,1,1,created_by,?,fingerprint FROM model_aliases WHERE id=?`, uuid.NewString(), uuid.NewString(), "synthetic-"+uuid.NewString(), dep, uuid.NewString(), alias)
				x.exec(t, "", "UPDATE model_aliases SET disabled_at=UTC_TIMESTAMP(6) WHERE id=?", alias)
				aliasSchemaCount(t, x, "model_aliases", 1)
			case "history_and_target_immutability":
				alias := retirementAlias(t, x, true)
				for _, query := range []string{"UPDATE model_alias_history SET reason='SYNTHETIC_PRIVATE' WHERE alias_id=?", "DELETE FROM model_alias_history WHERE alias_id=?", "UPDATE model_alias_target_versions SET deployment_revision=2 WHERE alias_id=?", "DELETE FROM model_alias_target_versions WHERE alias_id=?"} {
					aliasSchemaReject(t, x, query, alias)
				}
				aliasSchemaCount(t, x, "model_alias_history", 1)
				aliasSchemaCount(t, x, "model_alias_target_versions", 1)
			case "target_snapshot_scope_and_rollback_source":
				alias := retirementAlias(t, x, true)
				_, _, dep := aliasSchemaSelected(t, x, alias)
				x.exec(t, "", "UPDATE model_aliases SET revision=2 WHERE id=?", alias)
				retirementSeed(t, x, x.f.Store, "model_alias_history", aliasSchemaHistory(alias, x.f.User, 2))
				statement := `INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest) SELECT ?,tenant_id,workspace_id,alias_id,2,'published',deployment_id,?,model_id,model_kind,catalog_digest FROM model_alias_target_versions WHERE alias_id=? AND revision=1`
				aliasSchemaReject(t, x, statement, uuid.NewString(), 2, alias)
				aliasSchemaReject(t, x, `INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,source_revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest) SELECT ?,tenant_id,workspace_id,alias_id,2,99,'rolled_back',deployment_id,1,model_id,model_kind,catalog_digest FROM model_alias_target_versions WHERE alias_id=? AND revision=1`, uuid.NewString(), alias)
				target := aliasSchemaTarget(alias, dep, x.model, 2)
				target["action"] = "rolled_back"
				target["source_revision"] = 1
				retirementSeed(t, x, x.f.Store, "model_alias_target_versions", target)
				aliasSchemaCount(t, x, "model_alias_target_versions", 2)
				aliasSchemaReject(t, x, `INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest) SELECT ?,tenant_id,?,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest FROM model_alias_target_versions WHERE alias_id=? AND revision=1`, uuid.NewString(), uuid.NewString(), alias)
			case "bounded_fields_and_canonical_identifiers":
				alias := retirementAlias(t, x, false)
				_, gateway, _ := aliasSchemaSelected(t, x, alias)
				for _, change := range []string{"requested_epoch=-1", "control_origin_sha256='UPPERCASE'", "credential_hash=X'01'", "client_origin='short'"} {
					aliasSchemaReject(t, x, "UPDATE gateway_instances SET "+change+" WHERE id=?", gateway)
				}
				aliasSchemaReject(t, x, `INSERT INTO gateway_instances(id,tenant_id,workspace_id,node_id,name,client_origin,control_origin_sha256,credential_hash,created_by,requested_epoch,idempotency_key,fingerprint,authority_invalidated_at) SELECT ?,tenant_id,workspace_id,node_id,name,client_origin,control_origin_sha256,credential_hash,created_by,requested_epoch,?,fingerprint,UTC_TIMESTAMP(6) FROM gateway_instances WHERE id=?`, "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", uuid.NewString(), gateway)
				for _, revision := range []int64{0, 9007199254740992} {
					aliasSchemaReject(t, x, "UPDATE model_aliases SET revision=? WHERE id=?", revision, alias)
				}
				h := aliasSchemaHistory(alias, x.f.User, 1)
				h["reason"] = strings.Repeat("\u754c", 500)
				retirementSeed(t, x, x.f.Store, "model_alias_history", h)
				aliasSchemaReject(t, x, "INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES(?,?,?,2,'published',?,?)", x.f.Tenant, x.f.Workspace, alias, x.f.User, strings.Repeat("\u754c", 501))
				aliasSchemaCount(t, x, "model_alias_history", 1)
			case "transaction_rollback_preserves_graph":
				node := retirementNode(t, x)
				dep := retirementDeployment(t, x, x.f.Store, node, x.model, true)
				alias, gateway := uuid.NewString(), uuid.NewString()
				e := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					aliasSchemaGraph(t, x, q, node, dep, alias, gateway)
					return errors.New("ALIAS_SCHEMA_SYNTHETIC_ROLLBACK")
				})
				modelReadError(t, e, "ALIAS_SCHEMA_SYNTHETIC_ROLLBACK")
				for _, table := range []string{"gateway_instances", "model_aliases", "model_alias_history", "model_alias_target_versions"} {
					aliasSchemaCount(t, x, table, 0)
				}
				if e = x.f.Store.Within(x.ctx, true, func(ctx context.Context, q transaction.Executor) error {
					found, e := retirementReferences(ctx, q, x.f.Tenant, x.f.Workspace, x.model)
					if e != nil {
						return e
					}
					if found {
						return errors.New("ROLLED_BACK_REFERENCE_RETAINED")
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
			}
			if deadline, ok := x.ctx.Deadline(); !ok || time.Until(deadline) <= 0 {
				t.Fatal("live bounded schema verification required")
			}
		})
	}
}
