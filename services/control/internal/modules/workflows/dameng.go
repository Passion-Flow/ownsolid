package workflows

import (
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"strconv"
	"time"
)

const workflowFieldsDameng = "id,workspace_id,name,enabled,revision,draft_graph,draft_sha256,current_version_id,execution_authority,created_by,created_at,updated_at"
const versionFieldsDameng = "id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by,published_at"
const conversationFieldsDameng = "id,workflow_id,version_id,revision,turn_count,created_at,updated_at"
const runFieldsDameng = `r.id,r.workflow_id,r.conversation_id,r.version_id,r.draft_revision,r.graph_sha256,r.state,r.question,r.answer,r.error_code,r.cancellation_requested_at,r.created_at,r.started_at,r.finished_at,r.deadline_at,r.lease_expires_at,r.owner_id,r.requested_audience,r.execution_authority,r.membership_authority,r.graph_snapshot,r.evidence,r.alias_snapshot,r."USAGE",r.process_owner,r.lease_id,r.result_kind,r.tool_result,r.tool_proof`

// Native maintenance limits match the validated WorkflowPolicy ceiling. Only
// the validated integer is rendered; every value and identity remains bound.
const workflowNativeMaintenanceMaximum = 1000

func workflowDamengStatement(query workflowQuery, input ...any) transaction.Statement {
	invalid := func() transaction.Statement { return transaction.SQL(transaction.Dameng, "") }
	if query.dameng == "" {
		return invalid()
	}
	args := append([]any(nil), input...)
	for _, position := range query.damengJSON {
		if position < 1 || position > len(args) {
			return invalid()
		}
		var raw []byte
		switch v := args[position-1].(type) {
		case []byte:
			raw = v
		case json.RawMessage:
			raw = v
		case string:
			raw = []byte(v)
		default:
			return invalid()
		}
		if !json.Valid(raw) {
			return invalid()
		}
		// []byte denotes a BLOB in the native driver; JSON is a CLOB string.
		args[position-1] = string(raw)
	}
	text := query.dameng
	if query.damengLimit != 0 {
		index := query.damengLimit - 1
		if index < 0 || index >= len(args) {
			return invalid()
		}
		limit, ok := args[index].(int)
		if !ok || limit < 1 || limit > workflowNativeMaintenanceMaximum {
			return invalid()
		}
		text += " LIMIT " + strconv.Itoa(limit) + query.damengSuffix
		args = append(args[:index], args[index+1:]...)
	}
	if len(query.damengOrder) != 0 {
		ordered := make([]any, len(query.damengOrder))
		for i, position := range query.damengOrder {
			if position < 1 || position > len(args) {
				return invalid()
			}
			ordered[i] = args[position-1]
		}
		args = ordered
	}
	return transaction.SQL(transaction.Dameng, text, args...)
}

func workflowNativeCursor(cursor pagination.Cursor) error {
	if cursor.ID != "" && (cursor.Time.Year() < 1970 || cursor.Time.Year() > 9999 || cursor.Time.Nanosecond()%1000 != 0 || cursor.Time.Location() != time.UTC) {
		return failure("INVALID_CURSOR")
	}
	return nil
}

// The owner supplies only fixed SQL fragments. Seek timestamps are repeated as
// bound values so the native path does not depend on row-value comparisons.
func workflowNativePage(base, timestamp, id string, args []any, cursor pagination.Cursor, limit int) (transaction.Statement, error) {
	if e := workflowNativeCursor(cursor); e != nil {
		return transaction.Statement{}, e
	}
	if limit < 1 || limit > pagination.MaximumLimit {
		return transaction.Statement{}, failure("VALIDATION_FAILED")
	}
	args = append([]any(nil), args...)
	if cursor.ID != "" {
		base += " AND (" + timestamp + "<? OR (" + timestamp + "=? AND " + id + "<?))"
		args = append(args, cursor.Time, cursor.Time, cursor.ID)
	}
	return transaction.SQL(transaction.Dameng, base+" ORDER BY "+timestamp+" DESC,"+id+" DESC LIMIT "+strconv.Itoa(limit+1), args...), nil
}
