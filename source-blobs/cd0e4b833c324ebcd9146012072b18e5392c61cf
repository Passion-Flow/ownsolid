//go:build mysql_integration

package mysqlmigrate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

const holdoutSchemaStepCount = 94

var holdoutSchemaTables = []string{"model_holdouts", "model_holdout_sources", "model_holdout_prompts"}
var holdoutSchemaColumns = []string{"id", "tenant_id", "workspace_id", "dataset_id", "dataset_version_id", "name", "manifest_sha256", "effective_source_sha256", "row_mapping_sha256", "source_bytes", "row_count", "label_counts", "recipe_id", "recipe_sha256", "instruction_sha256", "fingerprint_algorithm", "created_by", "audience", "reason", "creation_key", "creation_fingerprint", "created_at"}

type holdoutSchemaRows struct {
	trainingSchemaRows
	holdout string
}

// Every row is synthetic SQL metadata. This is not file analysis, human rights
// approval, completed training or physical hardware evidence.
func holdoutSchemaValues(r trainingSchemaRows) map[string]any {
	digest := strings.Repeat("a", 64)
	return map[string]any{
		"id": uuid.NewString(), "tenant_id": r.tenant, "workspace_id": r.workspace,
		"dataset_id": r.dataset, "dataset_version_id": r.version,
		"name": "Synthetic holdout \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "manifest_sha256": digest,
		"effective_source_sha256": digest, "row_mapping_sha256": digest,
		"source_bytes": int64(64), "row_count": int64(3),
		"label_counts": `{"account":1,"billing":1,"technical":1}`,
		"recipe_id":    "synthetic-" + uuid.NewString(), "recipe_sha256": digest,
		"instruction_sha256": digest, "fingerprint_algorithm": "synthetic-metadata-only",
		"created_by": r.user, "audience": "admin", "reason": "Synthetic SQL only; no rights approval",
		"creation_key": uuid.NewString(), "creation_fingerprint": []byte(strings.Repeat("x", 32)),
		"created_at": r.heartbeat,
	}
}

func holdoutSchemaInsert(t *testing.T, values map[string]any) (string, []any) {
	t.Helper()
	if len(values) != len(holdoutSchemaColumns) {
		t.Fatal("exact owned holdout insert fields required")
	}
	arguments := make([]any, 0, len(holdoutSchemaColumns))
	for _, column := range holdoutSchemaColumns {
		value, found := values[column]
		if !found {
			t.Fatal("missing owned holdout insert field", column)
		}
		arguments = append(arguments, value)
	}
	return "INSERT INTO model_holdouts(" + strings.Join(holdoutSchemaColumns, ",") + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(arguments)), ",") + ")", arguments
}

func holdoutSchemaSeed(t *testing.T, s *mysqlstore.Store, old trainingSchemaRows) holdoutSchemaRows {
	t.Helper()
	values := holdoutSchemaValues(old)
	statement, arguments := holdoutSchemaInsert(t, values)
	migrationExec(t, s, statement, arguments...)
	r := holdoutSchemaRows{trainingSchemaRows: old, holdout: values["id"].(string)}
	migrationExec(t, s, `INSERT INTO model_holdout_sources(tenant_id,workspace_id,holdout_id,source_sha256) VALUES(?,?,?,?)`, r.tenant, r.workspace, r.holdout, []byte(strings.Repeat("s", 32)))
	migrationExec(t, s, `INSERT INTO model_holdout_prompts(tenant_id,workspace_id,holdout_id,prompt_sha256) VALUES(?,?,?,?)`, r.tenant, r.workspace, r.holdout, []byte(strings.Repeat("p", 32)))
	return r
}

func holdoutSchemaOldRows(t *testing.T, s *mysqlstore.Store) trainingSchemaRows {
	t.Helper()
	return trainingSchemaSeed(t, s, datasetSchemaSeed(t, s, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s)))))
}

func holdoutSchemaLayer(t *testing.T, count int) (*mysqlstore.Store, Catalog, holdoutSchemaRows) {
	t.Helper()
	s, c, _ := actualMigration(t)
	if len(c.steps) < holdoutSchemaStepCount || count != 88 && count != holdoutSchemaStepCount {
		t.Fatal("complete owned holdout catalog/layer required")
	}
	c.steps = c.steps[:count]
	migrationRun(t, s, c, Apply)
	return s, c, holdoutSchemaSeed(t, s, holdoutSchemaOldRows(t, s))
}

// Freeze every original column from the measured85 catalog, including lease
// owner/generation fields. New journal rows cannot masquerade as business drift.
func holdoutSchemaPrefixDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	c, e := DefaultCatalog()
	if e != nil || len(c.steps) < holdoutSchemaStepCount {
		t.Fatal("complete measured holdout catalog required", e)
	}
	definitions := map[string]string{}
	var tables []string
	for _, entry := range c.steps[:85] {
		if entry.Kind == createTable || entry.Kind == alterTable {
			if _, found := definitions[entry.Object]; !found {
				tables = append(tables, entry.Object)
			}
			definitions[entry.Object] = entry.Definition
		}
	}
	result := ""
	for _, table := range tables {
		var columns []string
		for _, line := range strings.Split(definitions[table], "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "`") {
				continue
			}
			end := strings.Index(line[1:], "`") + 1
			if end < 2 || !identifier.MatchString(line[1:end]) {
				t.Fatal("exact85 measured column identity")
			}
			quoted := line[:end+1]
			typeName := strings.Fields(line[end+1:])[0]
			if strings.Contains(typeName, "binary") || strings.Contains(typeName, "blob") {
				quoted = "HEX(" + quoted + ")"
			}
			columns = append(columns, quoted)
		}
		if len(columns) == 0 {
			t.Fatal("complete85 owned column list required")
		}
		result += trainingSchemaColumnDigest(t, s, table, columns)
	}
	return hash(result + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step<=85 ORDER BY step`))
}

func holdoutSchemaDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	result := holdoutSchemaPrefixDigest(t, s)
	for _, table := range holdoutSchemaTables {
		rows, e := s.Pool().QueryContext(t.Context(), `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, table)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		var columns []string
		for rows.Next() {
			var name, kind string
			if e = rows.Scan(&name, &kind); e != nil || !identifier.MatchString(name) {
				rows.Close()
				t.Fatal("owned holdout digest column", mysqlstore.SafeError(e))
			}
			quoted := "`" + name + "`"
			if strings.Contains(kind, "binary") || strings.Contains(kind, "blob") {
				quoted = "HEX(" + quoted + ")"
			}
			columns = append(columns, quoted)
		}
		e = rows.Err()
		rows.Close()
		if e != nil || len(columns) == 0 {
			t.Fatal("complete holdout columns required", mysqlstore.SafeError(e))
		}
		result += trainingSchemaColumnDigest(t, s, table, columns)
	}
	return hash(result + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step BETWEEN 86 AND 94 ORDER BY step`))
}

func holdoutSchemaRefuses(t *testing.T, s *mysqlstore.Store, number uint16, statement string, arguments ...any) {
	t.Helper()
	before := holdoutSchemaDataDigest(t, s)
	_, e := s.Pool().ExecContext(t.Context(), statement, arguments...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) {
		t.Fatal("holdout refusal missing safe numeric category")
	}
	if safe.Number != number {
		t.Fatalf("HOLDOUT_REFUSAL_CATEGORY_MISMATCH expected_number=%d actual_number=%d state_hex=%x code=%s", number, safe.Number, safe.SQLState, safe.Error())
	}
	if holdoutSchemaDataDigest(t, s) != before {
		t.Fatal("refused holdout statement changed complete persisted data or journal")
	}
}

func holdoutSchemaAssertRows(t *testing.T, s *mysqlstore.Store, r holdoutSchemaRows) {
	t.Helper()
	for _, table := range holdoutSchemaTables {
		column := "holdout_id"
		if table == "model_holdouts" {
			column = "id"
		}
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE "+column+"=? AND tenant_id=? AND workspace_id=?", r.holdout, r.tenant, r.workspace).Scan(&count); e != nil || count != 1 {
			t.Fatal("complete actual scoped holdout graph required", table, mysqlstore.SafeError(e))
		}
	}
	var name, reason string
	var labels, fingerprint []byte
	var created time.Time
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT name,reason,label_counts,creation_fingerprint,created_at FROM model_holdouts WHERE id=? AND tenant_id=? AND workspace_id=?`, r.holdout, r.tenant, r.workspace).Scan(&name, &reason, &labels, &fingerprint, &created); e != nil {
		t.Fatal("actual immutable holdout readback", mysqlstore.SafeError(e))
	}
	var values map[string]int64
	if json.Unmarshal(labels, &values) != nil || len(values) != 3 || values["account"] != 1 || values["billing"] != 1 || values["technical"] != 1 || name != "Synthetic holdout \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || reason != "Synthetic SQL only; no rights approval" || string(fingerprint) != strings.Repeat("x", 32) || !created.Equal(r.heartbeat) {
		t.Fatal("holdout Unicode/native JSON/binary/UTC data changed")
	}
}

func TestActualMySQLHoldoutSchemaFresh(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, holdoutSchemaStepCount)
	holdoutSchemaAssertRows(t, s, r)
	before := holdoutSchemaDataDigest(t, s)
	migrationRun(t, s, c, Apply)
	migrationRun(t, s, c, Readiness)
	if holdoutSchemaDataDigest(t, s) != before {
		t.Fatal("current holdout migration no-op changed complete business/journal data")
	}
}
