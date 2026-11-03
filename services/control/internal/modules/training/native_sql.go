package training

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Native SQL is an explicit repository-owned statement. Text parameter indexes
// identify JSON/CLOB values only; binary fingerprints and canonical BLOB bytes
// retain their types. No statement text is translated or inferred at runtime.
type trainingNativeQuery struct {
	text          string
	textArgs      []int
	pageMaximum   int
	argumentOrder []int
}

func trainingDM(text string, textArgs ...int) trainingNativeQuery {
	return trainingNativeQuery{text: text, textArgs: textArgs}
}
func trainingDMPage(text string, maximum int, textArgs ...int) trainingNativeQuery {
	return trainingNativeQuery{text: text, textArgs: textArgs, pageMaximum: maximum}
}
func (query trainingNativeQuery) order(indexes ...int) trainingNativeQuery {
	query.argumentOrder = indexes
	return query
}
func (query trainingNativeQuery) bind(args []any) (string, []any, bool) {
	bound := append([]any(nil), args...)
	for _, index := range query.textArgs {
		if index < 0 || index >= len(bound) {
			return "", nil, false
		}
		if bound[index] == nil {
			continue
		}
		var value string
		switch v := bound[index].(type) {
		case string:
			value = v
		case []byte:
			value = string(v)
		case json.RawMessage:
			value = string(v)
		default:
			return "", nil, false
		}
		if !json.Valid([]byte(value)) {
			return "", nil, false
		}
		bound[index] = value
	}
	if query.argumentOrder != nil {
		ordered := make([]any, len(query.argumentOrder))
		for i, index := range query.argumentOrder {
			if index < 0 || index >= len(bound) {
				return "", nil, false
			}
			ordered[i] = bound[index]
		}
		bound = ordered
	}
	sql := query.text
	if query.pageMaximum != 0 {
		if query.pageMaximum < 1 || len(bound) == 0 {
			return "", nil, false
		}
		limit, ok := bound[len(bound)-1].(int)
		if !ok || limit < 1 || limit > query.pageMaximum {
			return "", nil, false
		}
		sql += " LIMIT " + strconv.Itoa(limit)
		bound = bound[:len(bound)-1]
	}
	return sql, bound, sql != ""
}
func trainingSelectedSQL(q transaction.Executor, native trainingNativeQuery, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() != transaction.Dameng {
		return acceptedAdapterSQL(q, pg, my, args...)
	}
	text, bound, ok := native.bind(args)
	if !ok {
		return transaction.SQL(transaction.Dameng, "")
	}
	return transaction.SQL(transaction.Dameng, text, bound...)
}
func trainingSelectedRuntimeSQL(q transaction.Executor, native trainingNativeQuery, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	case transaction.Dameng:
		text, bound, ok := native.bind(myArgs)
		if ok {
			return transaction.SQL(transaction.Dameng, text, bound...)
		}
	}
	return transaction.SQL(q.Dialect(), "")
}
func trainingNativeCursor(value time.Time) bool {
	return value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}

const nativeTrainingJobFields = "\"ID\",\"WORKSPACE_ID\",\"NODE_ID\",\"CURRENT_NODE_ID\",\"MODEL_ID\",\"DATASET_ID\",\"DATASET_VERSION_ID\",\"GPU_UUID\",\"CONFIGURATION\",\"CONFIGURATION_SHA256\",\"EXECUTION_RECIPE\",\"DATASET_MANIFEST_SHA256\",\"MODEL_CATALOG_SHA256\",\"CREATED_BY\",\"REASON\",\"STATE\",\"WAIT_CODE\",\"REVISION\",\"ATTEMPT_NUMBER\",\"CURRENT_ATTEMPT_ID\",\"ACCEPTED_ARTIFACT_ID\",\"CANCEL_REQUESTED_AT\",\"ERROR_CODE\",\"CREATED_AT\",\"UPDATED_AT\",\"FINISHED_AT\",\"PENDING_RECOVERY_ID\""

func nativeTrainingHistory(kind string) string {
	switch kind {
	case "attempts":
		return "SELECT \"ID\",\"JOB_ID\",\"NODE_ID\",\"TASK_ID\",\"ATTEMPT_NUMBER\",\"STATE\",\"RESUME_CHECKPOINT_ID\",\"LAST_EVENT_SEQUENCE\",\"LAST_GLOBAL_STEP\",\"ERROR_CODE\",\"PHYSICAL_STOPPED_AT\",JSON_VALUE(\"PHYSICAL_STOP_RECEIPT\",'$.proof'),\"CREATED_AT\",\"STARTED_AT\",\"FINISHED_AT\" FROM \"TRAINING_ATTEMPTS\" WHERE \"TENANT_ID\"=? AND \"WORKSPACE_ID\"=? AND \"JOB_ID\"=?"
	case "checkpoints":
		return "SELECT \"ID\",\"JOB_ID\",\"ATTEMPT_ID\",\"GLOBAL_STEP\",\"RECEIPT_SHA256\",\"CREATED_AT\" FROM \"TRAINING_CHECKPOINTS\" WHERE \"TENANT_ID\"=? AND \"WORKSPACE_ID\"=? AND \"JOB_ID\"=?"
	case "recoveries":
		return "SELECT \"ID\",\"JOB_ID\",\"PREVIOUS_ATTEMPT_ID\",\"NEXT_ATTEMPT_ID\",\"CHECKPOINT_ID\",\"PREVIOUS_NODE_ID\",\"NEXT_NODE_ID\",\"MODE\",\"REQUESTED_BY\",\"REASON\",\"CREATED_AT\" FROM \"TRAINING_RECOVERIES\" WHERE \"TENANT_ID\"=? AND \"WORKSPACE_ID\"=? AND \"JOB_ID\"=?"
	}
	return ""
}

// An absent installation is a business refusal. Transport/statement failures
// must retain native error metadata and may not masquerade as missing setup.
func trainingInstallationFailure(q transaction.Executor, e error) error {
	if e == nil {
		return nil
	}
	if q.Dialect() == transaction.Dameng && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	return fail("TRAINING_INSTALLATION_REQUIRED")
}
