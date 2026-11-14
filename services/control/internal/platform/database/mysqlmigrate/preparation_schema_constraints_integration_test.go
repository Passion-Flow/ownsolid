//go:build mysql_integration

package mysqlmigrate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type preparationSchemaRange struct {
	minimum, maximum int64
	binary           bool
}

// These independent test-domain bounds are fixed by the frozen protocol/DDL
// contract. They are not parsed from the CHECK being tested.
func preparationSchemaNumericRange(table, field string) (preparationSchemaRange, bool) {
	switch field {
	case "revision":
		return preparationSchemaRange{1, 9007199254740991, false}, true
	case "attempt_number":
		minimum := int64(1)
		if table == "model_preparations" {
			minimum = 0
		}
		return preparationSchemaRange{minimum, preparationSchemaMaximumAttempts, false}, true
	case "next_attempt_number":
		return preparationSchemaRange{2, preparationSchemaMaximumAttempts, false}, true
	case "last_event_sequence":
		return preparationSchemaRange{0, preparationSchemaMaximumEvents, false}, true
	case "sequence":
		return preparationSchemaRange{1, preparationSchemaMaximumEvents, false}, true
	case "event_bytes":
		return preparationSchemaRange{0, 16777216, false}, true
	case "active_offset", "byte_offset":
		return preparationSchemaRange{0, 2147483648, false}, true
	case "size_bytes":
		return preparationSchemaRange{1, 8388608, false}, true
	case "manifest_bytes", "outcome_bytes", "report_bytes":
		return preparationSchemaRange{2, 8388608, true}, true
	}
	return preparationSchemaRange{}, false
}

func TestActualMySQLPreparationSchemaConstraints(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, 103)
	type refusal struct {
		name, table, field string
		value              any
		number             uint16
	}
	var cases []refusal
	add := func(table, field, suffix string, value any, number uint16) {
		cases = append(cases, refusal{table + "__" + field + "__" + suffix, table, field, value, number})
	}
	for _, table := range preparationSchemaTables {
		for _, column := range preparationSchemaOwnedColumns(t, s, table) {
			field := column.name
			if column.kind == "char" {
				add(table, field, "canonical_uuid", "ABCDEFAB-CDEF-ABCD-EFAB-CDEFABCDEFAB", 3819)
			}
			if strings.HasSuffix(field, "sha256") {
				add(table, field, "canonical_hash", strings.Repeat("A", 64), 3819)
			}
			if column.kind == "varbinary" {
				add(table, field, "binary31", bytes.Repeat([]byte{'x'}, 31), 3819)
			}
			if field == "state" || strings.HasSuffix(field, "audience") {
				add(table, field, "invalid_enum", "foreign", 3819)
			}
			if column.kind == "json" {
				for _, item := range []struct{ name, value string }{{"JSON_null", "null"}, {"array", "[]"}, {"scalar", `"value"`}, {"malformed", "{"}} {
					number := uint16(3819)
					if item.name == "malformed" {
						number = 3140
					}
					add(table, field, item.name, item.value, number)
				}
			}
			if domain, found := preparationSchemaNumericRange(table, field); found {
				var minimum, maximum any = domain.minimum - 1, domain.maximum + 1
				if domain.binary {
					minimum = bytes.Repeat([]byte{'x'}, int(domain.minimum-1))
					maximum = bytes.Repeat([]byte{'x'}, int(domain.maximum+1))
				} else if field == "byte_offset" {
					// Keep alignment valid so it cannot mask the actual range guard.
					minimum, maximum = int64(-8388608), domain.maximum+8388608
				}
				add(table, field, "underflow", minimum, 3819)
				add(table, field, "overflow", maximum, 3819)
			}
			if field == "reason" {
				add(table, field, "empty", " ", 3819)
			}
			if field == "wait_code" || field == "error_code" {
				add(table, field, "invalid_code", "invalid", 3819)
			}
		}
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if item.table == "model_preparations" && item.field == "attempt_number" {
				// Retain the existing valid current-attempt FK while isolating the
				// numeric bound; a copied parent cannot yet own that backreference.
				preparationSchemaRefuses(t, s, item.number, `UPDATE model_preparations SET attempt_number=? WHERE id=?`, item.value, r.preparation)
				return
			}
			values := preparationSchemaCopyValues(t, s, r, item.table)
			values[item.field] = item.value
			query, arguments := preparationSchemaCopySQL(t, s, r, item.table, values)
			preparationSchemaRefuses(t, s, item.number, query, arguments...)
		})
	}
	for _, item := range []struct {
		name, table, change, key string
		arguments                []any
	}{
		{"preparation__portal_authority_missing", "model_preparations", "requested_audience='portal',membership_authority=NULL", r.preparation, nil},
		{"attempt__portal_authority_missing", "model_preparation_attempts", "requested_audience='portal',membership_authority=NULL", r.preparationAttempt, nil},
		{"recovery__portal_authority_missing", "model_preparation_recoveries", "requested_audience='portal',membership_authority=NULL", r.preparationRecovery, nil},
		{"preparation__cancel_actor_without_time", "model_preparations", "cancel_requested_by=?,cancel_requested_at=NULL", r.preparation, []any{r.user}},
		{"preparation__cancel_time_without_actor", "model_preparations", "cancel_requested_at=?,cancel_requested_by=NULL", r.preparation, []any{r.heartbeat}},
		{"preparation__attempt_number_without_identity", "model_preparations", "attempt_number=1,current_attempt_id=NULL,pending_recovery_id=NULL", r.preparation, nil},
		{"preparation__attempt_identity_without_number", "model_preparations", "attempt_number=0,pending_recovery_id=NULL", r.preparation, nil},
		{"preparation__pending_recovery_without_attempt", "model_preparations", "attempt_number=0,current_attempt_id=NULL", r.preparation, nil},
		{"attempt__outcome_without_hash", "model_preparation_attempts", "outcome_sha256=NULL", r.preparationAttempt, nil},
		{"attempt__hash_without_outcome", "model_preparation_attempts", "outcome=NULL,outcome_bytes=NULL", r.preparationAttempt, nil},
		{"attempt__outcome_without_bytes", "model_preparation_attempts", "outcome_bytes=NULL", r.preparationAttempt, nil},
		{"attempt__bytes_without_outcome", "model_preparation_attempts", "outcome=NULL,outcome_sha256=NULL", r.preparationAttempt, nil},
		{"attempt__stop_receipt_without_hash", "model_preparation_attempts", "physical_stop_sha256=NULL", r.preparationAttempt, nil},
		{"attempt__stop_hash_without_receipt", "model_preparation_attempts", "physical_stop_receipt=NULL,physical_stopped_at=NULL", r.preparationAttempt, nil},
		{"attempt__stop_receipt_without_time", "model_preparation_attempts", "physical_stopped_at=NULL", r.preparationAttempt, nil},
		{"attempt__stop_time_without_receipt", "model_preparation_attempts", "physical_stop_receipt=NULL,physical_stop_sha256=NULL", r.preparationAttempt, nil},
		{"recovery__dispatch_without_applied", "model_preparation_recoveries", "dispatched_attempt_id=?,applied_at=NULL", r.preparationRecovery, []any{r.preparationAttempt}},
		{"recovery__applied_without_dispatch", "model_preparation_recoveries", "applied_at=?,dispatched_attempt_id=NULL", r.preparationRecovery, []any{r.heartbeat}},
		{"upload__chunk_identity_without_asset", "model_preparation_uploads", "active_chunk_id=?,active_asset=NULL,active_offset=0,active_expires_at=?", r.preparationUpload, []any{uuid.NewString(), r.heartbeat}},
		{"upload__chunk_identity_without_offset", "model_preparation_uploads", "active_chunk_id=?,active_asset='synthetic.bin',active_offset=NULL,active_expires_at=?", r.preparationUpload, []any{uuid.NewString(), r.heartbeat}},
		{"upload__chunk_identity_without_expiry", "model_preparation_uploads", "active_chunk_id=?,active_asset='synthetic.bin',active_offset=0,active_expires_at=NULL", r.preparationUpload, []any{uuid.NewString()}},
		{"upload__asset_without_chunk_identity", "model_preparation_uploads", "active_asset='synthetic.bin'", r.preparationUpload, nil},
		{"upload__offset_without_chunk_identity", "model_preparation_uploads", "active_offset=0", r.preparationUpload, nil},
		{"upload__expiry_without_chunk_identity", "model_preparation_uploads", "active_expires_at=?", r.preparationUpload, []any{r.heartbeat}},
		{"upload__accepted_without_timestamp", "model_preparation_uploads", "accepted_at=NULL", r.preparationUpload, nil},
		{"upload__timestamp_without_accepted", "model_preparation_uploads", "state='uploading'", r.preparationUpload, nil},
		{"artifact__id_upload_mismatch", "model_preparation_artifacts", "id=?", r.preparationUpload, []any{uuid.NewString()}},
		{"artifact__wrong_storage_path", "model_preparation_artifacts", "storage_path='artifacts/../synthetic'", r.preparationUpload, nil},
		{"preparation__review_state_unavailable", "model_preparations", "state='approved'", r.preparation, nil},
	} {
		t.Run(item.name, func(t *testing.T) {
			arguments := append(append([]any{}, item.arguments...), item.key, r.tenant, r.workspace)
			preparationSchemaRefuses(t, s, 3819, "UPDATE "+item.table+" SET "+item.change+" WHERE id=? AND tenant_id=? AND workspace_id=?", arguments...)
		})
	}
	for _, item := range []struct {
		name, change string
	}{
		{"chunk__asset_parent", "asset='..'"},
		{"chunk__asset_dot", "asset='.'"},
		{"chunk__unaligned_offset", "byte_offset=1"},
	} {
		t.Run(item.name, func(t *testing.T) {
			preparationSchemaRefuses(t, s, 3819, "UPDATE model_preparation_upload_chunks SET "+item.change+" WHERE upload_id=? AND asset='synthetic.bin' AND byte_offset=0", r.preparationUpload)
		})
	}
	if len(cases)+32 != 176 {
		t.Fatal("exact corrected176 preparation constraints required")
	}
	migrationRun(t, s, c, Readiness)
}
