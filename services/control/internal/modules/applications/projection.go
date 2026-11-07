package applications

import (
	"strconv"
	"time"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

// projectionQuery keeps each vendor's SQL explicit. Source authority is still
// rechecked by transaction and ConfigurationWithin before this query executes.
func projectionQuery(dialect transaction.Dialect, conversations bool, tenant, workspace, user string, cursor pagination.Cursor, limit int) (transaction.Statement, error) {
	if limit < 1 || limit > pagination.MaximumLimit {
		return transaction.Statement{}, failure("VALIDATION_FAILED")
	}
	var query, stamp, id string
	args := []any{tenant, workspace}
	if conversations {
		args = append(args, user)
		stamp, id = "c.updated_at", "c.id"
		switch dialect {
		case transaction.PostgreSQL:
			query = `SELECT c.id::text,c.workspace_id::text,c.workflow_id::text,f.name,c.version_id::text,v.version_number,c.turn_count,c.created_at,c.updated_at,f.enabled FROM workflow_conversations c JOIN workflows f ON f.tenant_id=c.tenant_id AND f.workspace_id=c.workspace_id AND f.id=c.workflow_id JOIN workflow_versions v ON v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.workflow_id=c.workflow_id AND v.id=c.version_id WHERE c.tenant_id=$1 AND c.workspace_id=$2 AND c.owner_id=$3 AND c.deleted_at IS NULL`
		case transaction.MySQL:
			query = `SELECT c.id,c.workspace_id,c.workflow_id,f.name,c.version_id,v.version_number,c.turn_count,c.created_at,c.updated_at,f.enabled FROM workflow_conversations c JOIN workflows f ON f.tenant_id=c.tenant_id AND f.workspace_id=c.workspace_id AND f.id=c.workflow_id JOIN workflow_versions v ON v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.workflow_id=c.workflow_id AND v.id=c.version_id WHERE c.tenant_id=? AND c.workspace_id=? AND c.owner_id=? AND c.deleted_at IS NULL`
		case transaction.Dameng:
			query = `SELECT c.id,c.workspace_id,c.workflow_id,f.name,c.version_id,v.version_number,c.turn_count,c.created_at,c.updated_at,f.enabled FROM workflow_conversations c JOIN workflows f ON f.tenant_id=c.tenant_id AND f.workspace_id=c.workspace_id AND f.id=c.workflow_id JOIN workflow_versions v ON v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.workflow_id=c.workflow_id AND v.id=c.version_id WHERE c.tenant_id=? AND c.workspace_id=? AND c.owner_id=? AND c.deleted_at IS NULL`
		default:
			return transaction.Statement{}, failure("STORAGE_UNAVAILABLE")
		}
	} else {
		stamp, id = "v.published_at", "f.id"
		switch dialect {
		case transaction.PostgreSQL:
			query = `SELECT f.id::text,f.workspace_id::text,f.name,v.id::text,v.version_number,v.graph_sha256,v.published_at FROM workflows f JOIN workflow_versions v ON v.tenant_id=f.tenant_id AND v.workspace_id=f.workspace_id AND v.workflow_id=f.id AND v.id=f.current_version_id WHERE f.tenant_id=$1 AND f.workspace_id=$2 AND f.enabled`
		case transaction.MySQL:
			query = `SELECT f.id,f.workspace_id,f.name,v.id,v.version_number,v.graph_sha256,v.published_at FROM workflows f JOIN workflow_versions v ON v.tenant_id=f.tenant_id AND v.workspace_id=f.workspace_id AND v.workflow_id=f.id AND v.id=f.current_version_id WHERE f.tenant_id=? AND f.workspace_id=? AND f.enabled`
		case transaction.Dameng:
			query = `SELECT f.id,f.workspace_id,f.name,v.id,v.version_number,v.graph_sha256,v.published_at FROM workflows f JOIN workflow_versions v ON v.tenant_id=f.tenant_id AND v.workspace_id=f.workspace_id AND v.workflow_id=f.id AND v.id=f.current_version_id WHERE f.tenant_id=? AND f.workspace_id=? AND f.enabled=1`
		default:
			return transaction.Statement{}, failure("STORAGE_UNAVAILABLE")
		}
	}
	if cursor.ID != "" {
		switch dialect {
		case transaction.PostgreSQL:
			query += " AND (" + stamp + "," + id + ")<($" + strconv.Itoa(len(args)+1) + ",$" + strconv.Itoa(len(args)+2) + "::uuid)"
			args = append(args, cursor.Time, cursor.ID)
		case transaction.MySQL:
			query += " AND (" + stamp + "," + id + ")<(?,?)"
			args = append(args, cursor.Time, cursor.ID)
		case transaction.Dameng:
			at := cursor.Time.UTC()
			if !strict.ValidID(cursor.ID) || !nativeCursorTime(at) {
				return transaction.Statement{}, failure("INVALID_CURSOR")
			}
			query += " AND (" + stamp + "<? OR (" + stamp + "=? AND " + id + "<?))"
			args = append(args, at, at, cursor.ID)
		}
	}
	query += " ORDER BY " + stamp + " DESC," + id + " DESC LIMIT "
	switch dialect {
	case transaction.PostgreSQL:
		query += "$" + strconv.Itoa(len(args)+1)
		args = append(args, limit+1)
	case transaction.MySQL:
		query += "?"
		args = append(args, limit+1)
	case transaction.Dameng:
		// DM LIMIT binding remains target-unqualified. Only the validated bounded
		// integer enters SQL text; every identity and cursor value stays bound.
		query += strconv.Itoa(limit + 1)
	}
	return transaction.SQL(dialect, query, args...), nil
}

func nativeCursorTime(at time.Time) bool {
	return !at.IsZero() && at.Location() == time.UTC && at.Year() >= 1970 && at.Year() <= 9999 && at.Nanosecond()%1000 == 0
}
