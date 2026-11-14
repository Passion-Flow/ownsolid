package distribution

import (
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This is the existing monitoring admission ceiling, not a larger ordinary
// gateway-history page policy. The native query takes one extra coverage row.
const monitoringMaximumRows = 10000

func monitoringWindowStatement(q transaction.Executor, tenant, workspace string, from, until time.Time, maximum int) transaction.Statement {
	if q == nil {
		return transaction.Statement{}
	}
	if maximum < 1 || maximum > monitoringMaximumRows {
		return transaction.SQL(q.Dialect(), "")
	}
	if q.Dialect() == transaction.Dameng {
		// OUTCOME is a nullable, at most4096-byte CLOB in the owning schema.
		// Retain the actual CLOB descriptor and original nullable text. A VARCHAR
		// conversion would move large pages onto the ordinary-result byte cap.
		// The Go contract validates the original outcome and canonical digest.
		// Only the validated integer limit is literal; scope/window stay bound.
		return distributionNativeBound(`SELECT "ID","STATE","CAPACITY_HELD","ADMITTED_AT","DEADLINE_AT","RESOLVED_AT","REPORTED_AT","OUTCOME","OUTCOME_SHA256","MAXIMUM_OUTPUT_TOKENS","MAXIMUM_CONTEXT_TOKENS","MAXIMUM_REQUEST_SECONDS" FROM "GATEWAY_REQUESTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ADMITTED_AT">=? AND "ADMITTED_AT"<? ORDER BY "ADMITTED_AT" DESC,"ID" DESC LIMIT `+strconv.Itoa(maximum+1), []any{tenant, workspace, from, until})
	}
	return distributionSQL(q, `SELECT id::text,state,capacity_held,admitted_at,deadline_at,resolved_at,reported_at,outcome,outcome_sha256,maximum_output_tokens,maximum_context_tokens,maximum_request_seconds FROM gateway_requests WHERE tenant_id=$1 AND workspace_id=$2 AND admitted_at>=$3 AND admitted_at<$4 ORDER BY admitted_at DESC,id DESC LIMIT $5`, `SELECT id,state,capacity_held,admitted_at,deadline_at,resolved_at,reported_at,outcome,outcome_sha256,maximum_output_tokens,maximum_context_tokens,maximum_request_seconds FROM gateway_requests WHERE tenant_id=? AND workspace_id=? AND admitted_at>=? AND admitted_at<? ORDER BY admitted_at DESC,id DESC LIMIT ?`, tenant, workspace, from, until, maximum+1)
}
