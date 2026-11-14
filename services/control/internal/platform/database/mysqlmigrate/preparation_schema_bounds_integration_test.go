//go:build mysql_integration

package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func preparationSchemaJSONMaximum(table, field string) int64 {
	switch table + "." + field {
	case "model_preparations.input_snapshot":
		return 65536
	case "model_preparations.configuration", "model_preparation_events.payload":
		return 16384
	case "model_preparations.execution_recipe", "model_preparation_attempts.physical_stop_receipt", "model_preparation_uploads.descriptor", "model_preparation_artifacts.descriptor":
		return 32768
	case "model_preparation_attempts.outcome", "model_preparation_uploads.manifest", "model_preparation_artifacts.report":
		return 8388608
	}
	return 0
}

func preparationSchemaBoundsPredicate(t *testing.T, r preparationSchemaRows, table string, values map[string]any) (string, []any) {
	t.Helper()
	value := func(field string, fallback any) any {
		if result, found := values[field]; found {
			return result
		}
		return fallback
	}
	switch table {
	case "model_preparation_events":
		return "attempt_id=? AND sequence=? AND tenant_id=? AND workspace_id=?", []any{value("attempt_id", r.preparationAttempt), value("sequence", int64(1)), r.tenant, r.workspace}
	case "model_preparation_upload_chunks":
		return "upload_id=? AND asset=? AND byte_offset=?", []any{value("upload_id", r.preparationUpload), value("asset", "synthetic.bin"), value("byte_offset", int64(0))}
	default:
		id, found := values["id"]
		if !found || !identifier.MatchString(table) {
			t.Fatal("exact inserted bounds fixture identity required")
		}
		return "id=? AND tenant_id=? AND workspace_id=?", []any{id, r.tenant, r.workspace}
	}
}

// Go's canonical JSON HTML escaping lets the raw byte ceiling be exercised
// without also exceeding MySQL's separately bounded native JSON rendering.
func preparationSchemaCanonicalBytes(t *testing.T, length int64) []byte {
	t.Helper()
	if length == 2 {
		return []byte("{}")
	}
	const objectOverhead = int64(len(`{"pad":""}`))
	const escapedCharacterBytes = int64(len(`\u003c`))
	padding := length - objectOverhead
	if padding < 0 {
		t.Fatal("valid canonical JSON byte length required")
	}
	value := strings.Repeat("<", int(padding/escapedCharacterBytes)) + strings.Repeat("x", int(padding%escapedCharacterBytes))
	raw, e := json.Marshal(map[string]string{"pad": value})
	if e != nil || int64(len(raw)) != length {
		t.Fatal("exact canonical JSON fixture size required")
	}
	return raw
}

func TestActualMySQLPreparationSchemaBounds(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, 103)
	valid := func(t *testing.T, table, field string, expected any, nativeJSON bool) {
		t.Helper()
		values := preparationSchemaCopyValues(t, s, r, table)
		values[field] = expected
		if table == "model_preparation_attempts" && field == "attempt_number" {
			parent := preparationSchemaCopyValues(t, s, r, "model_preparations")
			preparationSchemaInsertCopy(t, s, r, "model_preparations", parent)
			values["preparation_id"] = parent["id"]
		}
		if table == "model_preparation_events" && field == "sequence" {
			values["attempt_id"] = preparationSchemaUnusedAttempt(t, s, r)
		}
		if field == "active_offset" {
			values["active_chunk_id"], values["active_asset"], values["active_expires_at"] = uuid.NewString(), "synthetic.bin", r.heartbeat.Add(time.Minute)
		}
		if field == "outcome_bytes" {
			raw := expected.([]byte)
			values["outcome"], values["outcome_sha256"] = string(raw), hash(string(raw))
		}
		if field == "manifest_bytes" {
			raw := expected.([]byte)
			values["manifest"], values["manifest_sha256"] = string(raw), hash(string(raw))
		}
		if field == "report_bytes" {
			raw := expected.([]byte)
			values["report"], values["manifest_sha256"] = string(raw), hash(string(raw))
		}
		preparationSchemaInsertCopy(t, s, r, table, values)
		predicate, arguments := preparationSchemaBoundsPredicate(t, r, table, values)
		defer func() {
			result, e := s.Pool().ExecContext(t.Context(), "DELETE FROM "+table+" WHERE "+predicate, arguments...)
			if e != nil {
				t.Fatal("owned table-layer bounds cleanup", mysqlstore.SafeError(e))
			}
			count, e := result.RowsAffected()
			if e != nil || count != 1 {
				t.Fatal("exact one owned bounds row cleanup required")
			}
		}()
		if nativeJSON {
			var raw []byte
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT CAST(`"+field+"` AS CHAR CHARACTER SET utf8mb4) FROM "+table+" WHERE "+predicate, arguments...).Scan(&raw); e != nil || int64(len(raw)) != preparationSchemaJSONMaximum(table, field) {
				t.Fatal("exact native JSON byte ceiling readback", mysqlstore.SafeError(e))
			}
			var actual, wanted map[string]string
			if json.Unmarshal(raw, &actual) != nil || json.Unmarshal([]byte(expected.(string)), &wanted) != nil || actual["pad"] != wanted["pad"] {
				t.Fatal("native JSON content changed at exact byte ceiling")
			}
			return
		}
		switch value := expected.(type) {
		case int64:
			var actual int64
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT `"+field+"` FROM "+table+" WHERE "+predicate, arguments...).Scan(&actual); e != nil || actual != value {
				t.Fatal("exact integer bound readback", mysqlstore.SafeError(e))
			}
		case []byte:
			var actual []byte
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT `"+field+"` FROM "+table+" WHERE "+predicate, arguments...).Scan(&actual); e != nil || !bytes.Equal(actual, value) {
				t.Fatal("exact binary/canonical byte bound readback", mysqlstore.SafeError(e))
			}
		case string:
			var actual string
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT `"+field+"` FROM "+table+" WHERE "+predicate, arguments...).Scan(&actual); e != nil || actual != value {
				t.Fatal("exact Unicode/code character bound readback", mysqlstore.SafeError(e))
			}
		default:
			t.Fatal("owned bounds test value type required")
		}
	}
	for _, table := range preparationSchemaTables {
		for _, column := range preparationSchemaOwnedColumns(t, s, table) {
			field, prefix := column.name, table+"__"+column.name+"__"
			if domain, found := preparationSchemaNumericRange(table, field); found {
				for _, endpoint := range []struct {
					name  string
					value int64
				}{{"minimum", domain.minimum}, {"maximum", domain.maximum}} {
					t.Run(prefix+endpoint.name, func(t *testing.T) {
						if table == "model_preparations" && field == "attempt_number" {
							current, recovery := any(r.preparationAttempt), any(r.preparationRecovery)
							if endpoint.value == 0 {
								current, recovery = nil, nil
							}
							defer migrationExec(t, s, `UPDATE model_preparations SET attempt_number=1,current_attempt_id=?,pending_recovery_id=? WHERE id=?`, r.preparationAttempt, r.preparationRecovery, r.preparation)
							migrationExec(t, s, `UPDATE model_preparations SET attempt_number=?,current_attempt_id=?,pending_recovery_id=? WHERE id=?`, endpoint.value, current, recovery, r.preparation)
							var actual int64
							if e := s.Pool().QueryRowContext(t.Context(), `SELECT attempt_number FROM model_preparations WHERE id=?`, r.preparation).Scan(&actual); e != nil || actual != endpoint.value {
								t.Fatal("exact current-attempt boundary readback", mysqlstore.SafeError(e))
							}
							return
						}
						if domain.binary {
							valid(t, table, field, preparationSchemaCanonicalBytes(t, endpoint.value), false)
						} else {
							valid(t, table, field, endpoint.value, false)
						}
					})
				}
			}
			if column.kind == "json" {
				t.Run(prefix+"exact_native_JSON_bytes", func(t *testing.T) {
					var overhead int64
					if e := s.Pool().QueryRowContext(t.Context(), `SELECT OCTET_LENGTH(CAST(JSON_OBJECT('pad','') AS CHAR CHARACTER SET utf8mb4))`).Scan(&overhead); e != nil || overhead < 1 {
						t.Fatal("actual native JSON overhead required", mysqlstore.SafeError(e))
					}
					maximum := preparationSchemaJSONMaximum(table, field)
					if maximum < overhead {
						t.Fatal("owned native JSON bound required")
					}
					valid(t, table, field, `{"pad":"`+strings.Repeat("x", int(maximum-overhead))+`"}`, true)
				})
			}
			if field == "reason" {
				t.Run(prefix+"exact_unicode_length", func(t *testing.T) { valid(t, table, field, strings.Repeat("\u0639", 500), false) })
			}
			if field == "wait_code" || field == "error_code" {
				t.Run(prefix+"exact_code_length", func(t *testing.T) { valid(t, table, field, strings.Repeat("A", 80), false) })
			}
			if column.kind == "varbinary" {
				t.Run(prefix+"exact_binary32", func(t *testing.T) { valid(t, table, field, bytes.Repeat([]byte{0xff}, 32), false) })
				t.Run(prefix+"binary33", func(t *testing.T) {
					values := preparationSchemaCopyValues(t, s, r, table)
					values[field] = bytes.Repeat([]byte{0xff}, 33)
					query, arguments := preparationSchemaCopySQL(t, s, r, table, values)
					preparationSchemaRefuses(t, s, 1406, query, arguments...)
				})
			}
		}
	}
	t.Run("UTC_microseconds_and_Unicode_roundtrip", func(t *testing.T) {
		var created time.Time
		var reason string
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT created_at,reason FROM model_preparations WHERE id=? AND tenant_id=? AND workspace_id=?`, r.preparation, r.tenant, r.workspace).Scan(&created, &reason); e != nil || !created.Equal(r.heartbeat) || created.Location() != time.UTC || created.Nanosecond() != 123456000 || reason != "Synthetic preparation metadata \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" {
			t.Fatal("exact UTCmicroseconds/Unicode persisted readback", mysqlstore.SafeError(e))
		}
	})
	for _, item := range []struct{ name, table, field, value string }{
		{"reason_overflow", "model_preparations", "reason", strings.Repeat("\u0639", 501)},
		{"asset_overflow", "model_preparation_upload_chunks", "asset", strings.Repeat("a", 101)},
		{"storage_path_overflow", "model_preparation_artifacts", "storage_path", strings.Repeat("a", 513)},
	} {
		t.Run(item.name, func(t *testing.T) {
			values := preparationSchemaCopyValues(t, s, r, item.table)
			values[item.field] = item.value
			query, arguments := preparationSchemaCopySQL(t, s, r, item.table, values)
			preparationSchemaRefuses(t, s, 1406, query, arguments...)
		})
	}
	migrationRun(t, s, c, Readiness)
}
