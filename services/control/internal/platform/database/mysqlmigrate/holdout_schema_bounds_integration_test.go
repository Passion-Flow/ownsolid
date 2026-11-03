//go:build mysql_integration

package mysqlmigrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestActualMySQLHoldoutSchemaBounds(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, 88)
	for _, item := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"name_reason_exact_unicode_lengths", func(v map[string]any) { v["name"], v["reason"] = strings.Repeat("\u754c", 100), strings.Repeat("\u0639", 500) }},
		{"recipe_algorithm_exact_lengths", func(v map[string]any) {
			v["recipe_id"], v["fingerprint_algorithm"] = strings.Repeat("\u754c", 80), strings.Repeat("\u0639", 100)
		}},
		{"source_bytes_minimum", func(v map[string]any) { v["source_bytes"] = int64(1) }},
		{"source_bytes_maximum", func(v map[string]any) { v["source_bytes"] = int64(67108864) }},
		{"row_count_minimum", func(v map[string]any) { v["row_count"] = int64(3) }},
		{"row_count_maximum", func(v map[string]any) {
			v["row_count"], v["label_counts"] = int64(256), `{"account":254,"billing":1,"technical":1}`
		}},
		{"binary32_and_UTC_microseconds_roundtrip", func(v map[string]any) { v["creation_fingerprint"] = bytes.Repeat([]byte{0xff}, 32) }},
	} {
		t.Run(item.name, func(t *testing.T) {
			v := holdoutSchemaValues(r.trainingSchemaRows)
			item.change(v)
			query, args := holdoutSchemaInsert(t, v)
			migrationExec(t, s, query, args...)
			var name, reason, recipe, algorithm string
			var source, count int64
			var fingerprint, labels []byte
			var created time.Time
			e := s.Pool().QueryRowContext(t.Context(), `SELECT name,reason,recipe_id,fingerprint_algorithm,source_bytes,row_count,creation_fingerprint,label_counts,created_at FROM model_holdouts WHERE id=? AND tenant_id=? AND workspace_id=?`, v["id"], r.tenant, r.workspace).Scan(&name, &reason, &recipe, &algorithm, &source, &count, &fingerprint, &labels, &created)
			var actual, expected map[string]int64
			if e != nil || json.Unmarshal(labels, &actual) != nil || json.Unmarshal([]byte(v["label_counts"].(string)), &expected) != nil || name != v["name"] || reason != v["reason"] || recipe != v["recipe_id"] || algorithm != v["fingerprint_algorithm"] || source != v["source_bytes"] || count != v["row_count"] || !bytes.Equal(fingerprint, v["creation_fingerprint"].([]byte)) || !created.Equal(r.heartbeat) || created.Location() != time.UTC || actual["account"] != expected["account"] || actual["billing"] != expected["billing"] || actual["technical"] != expected["technical"] {
				t.Fatal("exact valid holdout bounds failed persisted readback")
			}
		})
	}
	for _, item := range []struct {
		name, field string
		value       any
	}{
		{"name_overflow", "name", strings.Repeat("\u754c", 101)},
		{"reason_overflow", "reason", strings.Repeat("\u0639", 501)},
		{"recipe_overflow", "recipe_id", strings.Repeat("\u754c", 81)},
		{"algorithm_overflow", "fingerprint_algorithm", strings.Repeat("\u0639", 101)},
		{"fingerprint_overflow", "creation_fingerprint", bytes.Repeat([]byte{0xff}, 33)},
	} {
		t.Run(item.name, func(t *testing.T) {
			v := holdoutSchemaValues(r.trainingSchemaRows)
			v[item.field] = item.value
			query, args := holdoutSchemaInsert(t, v)
			holdoutSchemaRefuses(t, s, 1406, query, args...)
		})
	}
	for _, item := range []struct{ name, table, column string }{
		{"source_digest_overflow", "model_holdout_sources", "source_sha256"},
		{"prompt_digest_overflow", "model_holdout_prompts", "prompt_sha256"},
	} {
		t.Run(item.name, func(t *testing.T) {
			holdoutSchemaRefuses(t, s, 1406, "INSERT INTO "+item.table+"(tenant_id,workspace_id,holdout_id,"+item.column+") VALUES(?,?,?,?)", r.tenant, r.workspace, r.holdout, bytes.Repeat([]byte{0xff}, 33))
		})
	}
	migrationRun(t, s, c, Readiness)
}
