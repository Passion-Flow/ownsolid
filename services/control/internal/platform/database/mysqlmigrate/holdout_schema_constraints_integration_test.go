//go:build mysql_integration

package mysqlmigrate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActualMySQLHoldoutSchemaConstraints(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, 88)
	var cases []struct {
		name, field string
		value       any
		number      uint16
	}
	add := func(name, field string, value any, number uint16) {
		cases = append(cases, struct {
			name, field string
			value       any
			number      uint16
		}{name, field, value, number})
	}
	for _, field := range []string{"id", "tenant_id", "workspace_id", "dataset_id", "dataset_version_id", "created_by", "creation_key"} {
		add("canonical_"+field, field, "ABCDEFAB-CDEF-ABCD-EFAB-CDEFABCDEFAB", 3819)
	}
	for _, field := range []string{"manifest_sha256", "effective_source_sha256", "row_mapping_sha256", "recipe_sha256", "instruction_sha256"} {
		add("invalid_"+field, field, strings.Repeat("A", 64), 3819)
	}
	for _, field := range []string{"name", "reason", "recipe_id", "fingerprint_algorithm"} {
		add("empty_"+field, field, " ", 3819)
	}
	add("invalid_audience", "audience", "operator", 3819)
	add("creation_fingerprint_short", "creation_fingerprint", []byte(strings.Repeat("x", 31)), 3819)
	add("source_bytes_underflow", "source_bytes", int64(0), 3819)
	add("source_bytes_overflow", "source_bytes", int64(67108865), 3819)
	add("row_count_underflow", "row_count", int64(2), 3819)
	add("row_count_overflow", "row_count", int64(257), 3819)
	add("label_SQL_NULL", "label_counts", nil, 1048)
	for _, item := range []struct{ name, value string }{
		{"label_JSON_null", "null"}, {"label_array", "[]"}, {"label_scalar", `"labels"`},
		{"label_malformed_JSON", "{"}, {"label_extra_key", `{"account":1,"billing":1,"technical":1,"extra":1}`},
		{"label_wrong_case", `{"Account":1,"billing":1,"technical":1}`},
		{"label_wrong_sum", `{"account":2,"billing":1,"technical":1}`},
		{"label_large_integer", `{"account":18446744073709551615,"billing":1,"technical":1}`},
	} {
		number := uint16(3819)
		if item.name == "label_malformed_JSON" {
			number = 3140
		}
		add(item.name, "label_counts", item.value, number)
	}
	for _, field := range []string{"account", "billing", "technical"} {
		missing := map[string]any{"account": 1, "billing": 1, "technical": 1}
		delete(missing, field)
		raw, e := json.Marshal(missing)
		if e != nil {
			t.Fatal(e)
		}
		add("label_missing_"+field, "label_counts", string(raw), 3819)
		for _, invalid := range []struct {
			name  string
			value any
		}{
			{"null", nil}, {"string", "1"}, {"float", json.RawMessage("1.0")}, {"boolean", true},
			{"negative", -1}, {"zero", 0}, {"overflow", 257}, {"object", map[string]any{}},
		} {
			value := map[string]any{"account": 1, "billing": 1, "technical": 1}
			value[field] = invalid.value
			raw, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			add("label_"+field+"_"+invalid.name, "label_counts", string(raw), 3819)
		}
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			values := holdoutSchemaValues(r.trainingSchemaRows)
			values[item.field] = item.value
			if item.name == "row_count_overflow" {
				values["label_counts"] = `{"account":255,"billing":1,"technical":1}`
			}
			statement, arguments := holdoutSchemaInsert(t, values)
			holdoutSchemaRefuses(t, s, item.number, statement, arguments...)
		})
	}
	for _, item := range []struct{ table, field, name string }{
		{"model_holdout_sources", "source_sha256", "source_digest_short"},
		{"model_holdout_prompts", "prompt_sha256", "prompt_digest_short"},
	} {
		t.Run(item.name, func(t *testing.T) {
			holdoutSchemaRefuses(t, s, 3819, "INSERT INTO "+item.table+"(tenant_id,workspace_id,holdout_id,"+item.field+") VALUES(?,?,?,?)", r.tenant, r.workspace, r.holdout, []byte(strings.Repeat("x", 31)))
		})
	}
	migrationRun(t, s, c, Readiness)
}
