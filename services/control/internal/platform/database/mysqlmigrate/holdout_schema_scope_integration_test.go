//go:build mysql_integration

package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func holdoutSchemaForeignDataset(t *testing.T, s *mysqlstore.Store, r holdoutSchemaRows, tenant string) datasetSchemaRows {
	t.Helper()
	f := modelInferenceForeign(t, s, r.modelInferenceSchemaRows, tenant)
	base := r.modelInferenceSchemaRows
	base.tenant, base.workspace, base.node, base.model, base.task = f.tenant, f.workspace, f.node, f.model, f.task
	return datasetSchemaSeed(t, s, base)
}

func TestActualMySQLHoldoutSchemaForeignScopes(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, 88)
	same := holdoutSchemaForeignDataset(t, s, r, r.tenant)
	tenant := uuid.NewString()
	migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic held-out foreign tenant','active')`, tenant)
	foreign := holdoutSchemaForeignDataset(t, s, r, tenant)
	for _, item := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"dataset_version_pair", func(v map[string]any) { v["dataset_id"] = same.dataset }},
		{"workspace_version", func(v map[string]any) { v["workspace_id"] = same.workspace }},
		{"tenant_version", func(v map[string]any) { v["tenant_id"], v["workspace_id"] = foreign.tenant, foreign.workspace }},
		{"missing_creator", func(v map[string]any) { v["created_by"] = uuid.NewString() }},
	} {
		t.Run(item.name, func(t *testing.T) {
			v := holdoutSchemaValues(r.trainingSchemaRows)
			item.change(v)
			query, args := holdoutSchemaInsert(t, v)
			holdoutSchemaRefuses(t, s, 1452, query, args...)
		})
	}
	for _, item := range []struct{ kind, table, column string }{
		{"source", "model_holdout_sources", "source_sha256"}, {"prompt", "model_holdout_prompts", "prompt_sha256"},
	} {
		for _, scope := range []struct{ name, tenant, workspace, holdout string }{
			{"foreign_workspace", r.tenant, same.workspace, r.holdout},
			{"foreign_tenant", foreign.tenant, foreign.workspace, r.holdout},
			{"missing_holdout", r.tenant, r.workspace, uuid.NewString()},
		} {
			t.Run(item.kind+"_"+scope.name, func(t *testing.T) {
				holdoutSchemaRefuses(t, s, 1452, "INSERT INTO "+item.table+"(tenant_id,workspace_id,holdout_id,"+item.column+") VALUES(?,?,?,?)", scope.tenant, scope.workspace, scope.holdout, []byte(strings.Repeat("f", 32)))
			})
		}
	}
	t.Run("referenced_holdout_delete", func(t *testing.T) {
		holdoutSchemaRefuses(t, s, 1451, `DELETE FROM model_holdouts WHERE id=?`, r.holdout)
	})
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLHoldoutSchemaUniqueness(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, 88)
	base := holdoutSchemaValues(r.trainingSchemaRows)
	query, args := holdoutSchemaInsert(t, base)
	migrationExec(t, s, query, args...)
	for _, kind := range []string{"version_recipe", "actor_audience_creation_key"} {
		t.Run(kind, func(t *testing.T) {
			v := holdoutSchemaValues(r.trainingSchemaRows)
			if kind == "version_recipe" {
				v["recipe_id"] = base["recipe_id"]
			} else {
				v["creation_key"] = base["creation_key"]
			}
			q, a := holdoutSchemaInsert(t, v)
			holdoutSchemaRefuses(t, s, 1062, q, a...)
		})
	}
	for _, item := range []struct{ name, table, column, digest string }{
		{"source_primary_key", "model_holdout_sources", "source_sha256", "s"},
		{"prompt_primary_key", "model_holdout_prompts", "prompt_sha256", "p"},
	} {
		t.Run(item.name, func(t *testing.T) {
			holdoutSchemaRefuses(t, s, 1062, "INSERT INTO "+item.table+"(tenant_id,workspace_id,holdout_id,"+item.column+") VALUES(?,?,?,?)", r.tenant, r.workspace, r.holdout, []byte(strings.Repeat(item.digest, 32)))
		})
	}
	t.Run("distinct_Workspace_creation_key_allowed", func(t *testing.T) {
		peer := holdoutSchemaForeignDataset(t, s, r, r.tenant)
		v := holdoutSchemaValues(r.trainingSchemaRows)
		v["workspace_id"], v["dataset_id"], v["dataset_version_id"], v["creation_key"] = peer.workspace, peer.dataset, peer.version, base["creation_key"]
		q, a := holdoutSchemaInsert(t, v)
		migrationExec(t, s, q, a...)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_holdouts WHERE tenant_id=? AND creation_key=? AND workspace_id IN (?,?)`, r.tenant, base["creation_key"], r.workspace, peer.workspace).Scan(&count); e != nil || count != 2 {
			t.Fatal("distinct Workspace creation key did not persist")
		}
	})
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLHoldoutSchemaImmutability(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, holdoutSchemaStepCount)
	for _, table := range holdoutSchemaTables {
		column := "holdout_id"
		if table == "model_holdouts" {
			column = "id"
		}
		t.Run(table+"_update", func(t *testing.T) {
			holdoutSchemaRefuses(t, s, 1644, "UPDATE "+table+" SET "+column+"=? WHERE "+column+"=?", uuid.NewString(), r.holdout)
		})
		t.Run(table+"_delete", func(t *testing.T) {
			holdoutSchemaRefuses(t, s, 1644, "DELETE FROM "+table+" WHERE "+column+"=?", r.holdout)
		})
	}
	holdoutSchemaAssertRows(t, s, r)
	migrationRun(t, s, c, Readiness)
}
