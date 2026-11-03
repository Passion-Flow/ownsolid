//go:build mysql_integration

package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

const preparationSchemaMaximumAttempts = 1000
const preparationSchemaMaximumEvents = 1024

type preparationSchemaColumn struct{ name, kind string }

func preparationSchemaOwnedColumns(t *testing.T, s *mysqlstore.Store, table string) []preparationSchemaColumn {
	t.Helper()
	owned := false
	for _, name := range preparationSchemaTables {
		owned = owned || name == table
	}
	if !owned {
		t.Fatal("owned preparation fixture table required")
	}
	rows, e := s.Pool().QueryContext(t.Context(), `SELECT column_name,data_type,extra FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, table)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	var result []preparationSchemaColumn
	for rows.Next() {
		var column preparationSchemaColumn
		var extra string
		if e = rows.Scan(&column.name, &column.kind, &extra); e != nil || !identifier.MatchString(column.name) {
			t.Fatal("owned preparation fixture column required", mysqlstore.SafeError(e))
		}
		if strings.Contains(extra, "VIRTUAL GENERATED") || strings.Contains(extra, "STORED GENERATED") {
			continue
		}
		result = append(result, column)
	}
	if rows.Err() != nil || len(result) == 0 {
		t.Fatal("complete ordinary preparation fixture columns required")
	}
	return result
}

func preparationSchemaCopySQL(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows, table string, values map[string]any) (string, []any) {
	t.Helper()
	columns := preparationSchemaOwnedColumns(t, s, table)
	var names, selected []string
	var arguments []any
	matched := 0
	for _, column := range columns {
		quoted := "`" + column.name + "`"
		names = append(names, quoted)
		if value, found := values[column.name]; found {
			selected = append(selected, "?")
			arguments = append(arguments, value)
			matched++
		} else {
			selected = append(selected, quoted)
		}
	}
	if matched != len(values) {
		t.Fatal("unknown or generated fixture override field")
	}
	var predicate string
	var scope []any
	switch table {
	case "model_preparations":
		predicate, scope = "id=?", []any{r.preparation}
	case "model_preparation_attempts":
		predicate, scope = "id=?", []any{r.preparationAttempt}
	case "model_preparation_events":
		predicate, scope = "attempt_id=? AND sequence=1", []any{r.preparationAttempt}
	case "model_preparation_recoveries":
		predicate, scope = "id=?", []any{r.preparationRecovery}
	case "model_preparation_uploads", "model_preparation_artifacts":
		predicate, scope = "id=?", []any{r.preparationUpload}
	case "model_preparation_upload_chunks":
		predicate = "upload_id=? AND asset='synthetic.bin' AND byte_offset=0 AND EXISTS(SELECT 1 FROM model_preparation_uploads u WHERE u.id=model_preparation_upload_chunks.upload_id AND u.tenant_id=? AND u.workspace_id=?)"
		scope = []any{r.preparationUpload, r.tenant, r.workspace}
	default:
		t.Fatal("owned preparation fixture source required")
	}
	if table != "model_preparation_upload_chunks" {
		predicate += " AND tenant_id=? AND workspace_id=?"
		scope = append(scope, r.tenant, r.workspace)
	}
	arguments = append(arguments, scope...)
	return "INSERT INTO " + table + "(" + strings.Join(names, ",") + ") SELECT " + strings.Join(selected, ",") + " FROM " + table + " WHERE " + predicate, arguments
}

func preparationSchemaInsertCopy(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows, table string, values map[string]any) {
	t.Helper()
	query, arguments := preparationSchemaCopySQL(t, s, r, table, values)
	result, e := s.Pool().ExecContext(t.Context(), query, arguments...)
	if e != nil {
		t.Fatal("actual auxiliary preparation fixture insert", mysqlstore.SafeError(e))
	}
	count, e := result.RowsAffected()
	if e != nil || count != 1 {
		t.Fatal("exact one existing scoped fixture source row required")
	}
}

func preparationSchemaUnusedOrdinal(t *testing.T, s *mysqlstore.Store, table, column, parentColumn, parent string, maximum int64) int64 {
	t.Helper()
	if !identifier.MatchString(table) || !identifier.MatchString(column) || !identifier.MatchString(parentColumn) {
		t.Fatal("owned ordinal identifiers required")
	}
	rows, e := s.Pool().QueryContext(t.Context(), "SELECT "+column+" FROM "+table+" WHERE "+parentColumn+"=?", parent)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	used := map[int64]bool{}
	for rows.Next() {
		var number int64
		if e = rows.Scan(&number); e != nil {
			rows.Close()
			t.Fatal(mysqlstore.SafeError(e))
		}
		used[number] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	for number := int64(2); number <= maximum; number++ {
		if !used[number] {
			return number
		}
	}
	t.Fatal("bounded synthetic fixture ordinal capacity exhausted")
	return 0
}

func preparationSchemaUnusedAttempt(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows) string {
	t.Helper()
	id, task := uuid.NewString(), uuid.NewString()
	migrationExec(t, s, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,completed_at,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,'model.prepare.v1','succeeded',?,?,UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)),JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?)`, task, r.tenant, r.workspace, r.preparationNode, uuid.NewString(), r.user, []byte(strings.Repeat("x", 32)))
	number := preparationSchemaUnusedOrdinal(t, s, "model_preparation_attempts", "attempt_number", "preparation_id", r.preparation, preparationSchemaMaximumAttempts)
	preparationSchemaInsertCopy(t, s, r, "model_preparation_attempts", map[string]any{"id": id, "task_id": task, "attempt_number": number})
	return id
}

// Legitimate auxiliary rows are installed before the refusal digest. Fresh
// identities keep duplicate/parent-update errors from masking the tested guard.
func preparationSchemaCopyValues(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows, table string) map[string]any {
	t.Helper()
	switch table {
	case "model_preparations":
		return map[string]any{"id": uuid.NewString(), "creation_key": uuid.NewString(), "attempt_number": int64(0), "current_attempt_id": nil, "pending_recovery_id": nil, "state": "queued"}
	case "model_preparation_attempts":
		task := uuid.NewString()
		migrationExec(t, s, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,completed_at,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,'model.prepare.v1','succeeded',?,?,UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)),JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?)`, task, r.tenant, r.workspace, r.preparationNode, uuid.NewString(), r.user, []byte(strings.Repeat("x", 32)))
		return map[string]any{"id": uuid.NewString(), "task_id": task, "attempt_number": preparationSchemaUnusedOrdinal(t, s, table, "attempt_number", "preparation_id", r.preparation, preparationSchemaMaximumAttempts)}
	case "model_preparation_events":
		return map[string]any{"sequence": preparationSchemaUnusedOrdinal(t, s, table, "sequence", "attempt_id", r.preparationAttempt, preparationSchemaMaximumEvents)}
	case "model_preparation_recoveries":
		return map[string]any{"id": uuid.NewString(), "creation_key": uuid.NewString()}
	case "model_preparation_uploads":
		return map[string]any{"id": uuid.NewString(), "attempt_id": preparationSchemaUnusedAttempt(t, s, r)}
	case "model_preparation_upload_chunks":
		return map[string]any{"asset": "synthetic-" + uuid.NewString()}
	case "model_preparation_artifacts":
		id, attempt := uuid.NewString(), preparationSchemaUnusedAttempt(t, s, r)
		preparationSchemaInsertCopy(t, s, r, "model_preparation_uploads", map[string]any{"id": id, "attempt_id": attempt})
		return map[string]any{"id": id, "upload_id": id, "attempt_id": attempt, "storage_path": "artifacts/" + r.preparation + "/" + attempt + "/" + id}
	default:
		t.Fatal("owned preparation copy table required")
		return nil
	}
}
