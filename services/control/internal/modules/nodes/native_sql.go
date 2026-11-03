package nodes

import (
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// DM uses native UTC timestamps, positional parameters and the schema owner's
// uppercase unquoted identifiers. SQL is explicit at each call, never rewritten.
const damengNodeFields = `n.id,n.tenant_id,n.workspace_id,n.name,n.status,n.version,n.protocol_version,n.facts,n.last_heartbeat_at,n.created_at,n.updated_at,CASE WHEN n.last_heartbeat_at>=DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) THEN 'online' WHEN n.last_heartbeat_at>=DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) THEN 'unknown' ELSE 'offline' END,n.execution_profile,n.capabilities`
const damengJoinFields = `j.id,j.tenant_id,j.workspace_id,j.node_name,j.created_by,j.node_id,j.created_at,j.expires_at,CASE WHEN j.revoked_at IS NOT NULL THEN 'revoked' WHEN j.consumed_at IS NOT NULL THEN 'consumed' WHEN j.expires_at<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 'expired' ELSE 'pending' END,j.execution_profile`
const damengTaskFields = `id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at`

func nodePositional(q transaction.Executor) bool {
	return q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng
}

// Only JSON arguments use CLOB text binding. Credential and fencing hashes keep
// binary binding, including SQL NULL versus non-nil empty binary distinctions.
func nodeJSON(q transaction.Executor, raw []byte) any {
	if q.Dialect() == transaction.Dameng {
		if raw == nil {
			return nil
		}
		return string(raw)
	}
	return raw
}

func nodeNativeTime(value time.Time) bool {
	// Match the native ODBC boundary exactly. A zero-offset custom Location is
	// not time.UTC; callers must choose UTC before supplying authoritative times.
	return !value.IsZero() && value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}
