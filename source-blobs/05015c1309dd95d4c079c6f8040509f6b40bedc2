package datasets

import "time"

// Keep cursor ordering exact. Native timestamps are UTC TIMESTAMP(6) with the
// driver's admitted 1970..9999 range; rounding a cursor can skip or repeat rows.
func datasetDamengTime(value time.Time) bool {
	return value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}

const dmDatasetFields = `id,workspace_id,name,origin,license,rights_asserted,created_by,revision,created_at,updated_at,retired_at,retire_reason`
const dmDraftFields = `error_line_number,id,dataset_id,workspace_id,created_by,parent_version_id,source_name,source_sha256,expected_bytes,source_received_at,state,revision,CASE WHEN validation_completed_at IS NOT NULL THEN total_rows END,CASE WHEN validation_completed_at IS NOT NULL THEN invalid_rows END,validation_completed_at,fatal_source_error,validation_errors,errors_truncated,error_code,current_job_id,published_version_id,created_at,updated_at`
const dmVersionFields = `id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason`
const dmRowFields = `id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced`
const dmPublishedDataset = "SELECT " + dmDatasetFields + " FROM datasets WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE"
const dmPublishedVersion = "SELECT " + dmVersionFields + " FROM dataset_versions WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE"
const dmInsertInspectedRow = `INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion,error_code,raw_preview,preview_replaced) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`
const dmInsertInspectedHistory = `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason) VALUES(?,?,?,?,?,?,?,?,?,?)`

func datasetDamengFields(fields string) string {
	switch fields {
	case datasetFields:
		return dmDatasetFields
	case draftFields:
		return dmDraftFields
	case versionFields:
		return dmVersionFields
	default:
		return ""
	}
}
