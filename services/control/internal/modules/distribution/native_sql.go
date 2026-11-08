package distribution

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
	"time"
)

const nativeGatewayFields = `ID,WORKSPACE_ID,NODE_ID,NAME,CLIENT_ORIGIN,CREATED_BY,CASE WHEN AUTHORITY_INVALIDATED_AT IS NOT NULL THEN 'invalidated' WHEN REVOKED_AT IS NOT NULL THEN 'revoked' ELSE 'active' END,CREATED_AT,REVOKED_AT,'manual',NULL,NULL,NULL`
const nativeAliasFields = `ID,WORKSPACE_ID,GATEWAY_ID,NODE_ID,NAME,DEPLOYMENT_ID,DEPLOYMENT_REVISION,REVISION,DISABLED_AT,CREATED_AT`
const nativeKeyFields = `K.ID,K.WORKSPACE_ID,K.ALIAS_ID,A.NAME,K.LABEL,K.CREATED_BY,CASE WHEN K.REVOKED_AT IS NOT NULL THEN 'revoked' WHEN K.EXPIRES_AT<=CURRENT_TIMESTAMP(6) THEN 'expired' WHEN K.REQUESTED_EPOCH<>U.AUTH_EPOCH OR U.STATUS<>'active' OR NOT EXISTS(SELECT 1 FROM WORKSPACE_MEMBERSHIPS M WHERE M.TENANT_ID=K.TENANT_ID AND M.WORKSPACE_ID=K.WORKSPACE_ID AND M.USER_ID=K.CREATED_BY AND M.AUTHORITY_ID=K.MEMBERSHIP_AUTHORITY) THEN 'invalidated' WHEN W.STATUS<>'active' THEN 'unavailable' ELSE 'active' END,K.CREATED_AT,K.EXPIRES_AT,K.LAST_USED_AT,K.REVOKED_AT,NULL,NULL`
const nativeKeyJoins = ` FROM API_KEYS K JOIN MODEL_ALIASES A ON A.ID=K.ALIAS_ID AND A.TENANT_ID=K.TENANT_ID AND A.WORKSPACE_ID=K.WORKSPACE_ID JOIN USERS U ON U.ID=K.CREATED_BY JOIN WORKSPACES W ON W.ID=K.WORKSPACE_ID AND W.TENANT_ID=K.TENANT_ID`
const nativeRequestFields = `R.ID,R.WORKSPACE_ID,R.GATEWAY_ID,R.NODE_ID,R.API_KEY_ID,R.ACTOR_USER_ID,R.ALIAS_ID,A.NAME,R.DEPLOYMENT_ID,R.DEPLOYMENT_REVISION,R.STATE,R.CAPACITY_HELD,R.STREAM,R.ADMITTED_AT,R.DEADLINE_AT,R.REPORTED_AT,R.RECONCILED_AT,R.ACTIVE_RECOVERY_ID,R.OUTCOME`
const nativeRequestJoins = ` FROM GATEWAY_REQUESTS R JOIN MODEL_ALIASES A ON A.TENANT_ID=R.TENANT_ID AND A.WORKSPACE_ID=R.WORKSPACE_ID AND A.ID=R.ALIAS_ID`

// Native SQL and each positional argument sequence are supplied explicitly by
// the owner. No PostgreSQL/MySQL statement is rewritten or used as a fallback.
func distributionNativeSQL(q transaction.Executor, native, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return distributionNativeBound(native, args)
	}
	return distributionSQL(q, pg, my, args...)
}
func distributionNativeStatement(q transaction.Executor, native string, nativeArgs []any, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return distributionNativeBound(native, nativeArgs)
	}
	return distributionStatement(q, pg, my, pgArgs, myArgs)
}
func distributionNativeBound(native string, args []any) transaction.Statement {
	// Do not truncate cursor/receipt times: that would change order or authority.
	for _, arg := range args {
		switch value := arg.(type) {
		case time.Time:
			if !nativeTime(value) {
				return transaction.SQL(transaction.Dameng, "")
			}
		case *time.Time:
			if value != nil && !nativeTime(*value) {
				return transaction.SQL(transaction.Dameng, "")
			}
		}
	}
	return transaction.SQL(transaction.Dameng, native, args...)
}
func nativeTime(value time.Time) bool {
	return value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}
func distributionPageSQL(q transaction.Executor, native, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() != transaction.Dameng {
		return distributionSQL(q, pg, my, args...)
	}
	return distributionNativePageBound(native, args)
}
func distributionPageStatement(q transaction.Executor, native string, nativeArgs []any, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() != transaction.Dameng {
		return distributionStatement(q, pg, my, pgArgs, myArgs)
	}
	return distributionNativePageBound(native, nativeArgs)
}
func distributionNativePageBound(native string, args []any) transaction.Statement {
	if len(args) == 0 {
		return transaction.SQL(transaction.Dameng, "")
	}
	limit, ok := args[len(args)-1].(int)
	if !ok || limit < 1 || limit > 101 {
		return transaction.SQL(transaction.Dameng, "")
	}
	return distributionNativeBound(native+" LIMIT "+strconv.Itoa(limit), args[:len(args)-1])
}

// The resource enum selects native projections; SQL text from another dialect
// never enters this builder. Every cursor value remains a bound parameter.
func nativeHistoryPage(resource, tenant, workspace, actor string, administrator bool, apiKey string, stamp time.Time, cursor string, limit int) (transaction.Statement, error) {
	var query, table, clock, owner string
	switch resource {
	case "gateways":
		query = "SELECT " + nativeGatewayFields + " FROM GATEWAY_INSTANCES G"
		table = "G"
		clock = "G.CREATED_AT"
	case "aliases":
		query = "SELECT " + nativeAliasFields + " FROM MODEL_ALIASES A"
		table = "A"
		clock = "A.CREATED_AT"
	case "keys":
		query = "SELECT " + nativeKeyFields + nativeKeyJoins
		table = "K"
		clock = "K.CREATED_AT"
		owner = "K.CREATED_BY"
	case "requests":
		query = "SELECT " + nativeRequestFields + nativeRequestJoins
		table = "R"
		clock = "R.ADMITTED_AT"
		owner = "R.ACTOR_USER_ID"
	default:
		return transaction.Statement{}, fail("VALIDATION_FAILED")
	}
	if limit < 1 || limit > 100 {
		return transaction.Statement{}, fail("VALIDATION_FAILED")
	}
	query += " WHERE " + table + ".TENANT_ID=? AND " + table + ".WORKSPACE_ID=?"
	args := []any{tenant, workspace}
	if !administrator {
		if owner != "" {
			query += " AND " + owner + "=?"
			args = append(args, actor)
		}
		if resource == "aliases" {
			query += " AND A.DISABLED_AT IS NULL"
		}
	}
	if apiKey != "" {
		if resource != "requests" {
			return transaction.Statement{}, fail("VALIDATION_FAILED")
		}
		query += " AND R.API_KEY_ID=?"
		args = append(args, apiKey)
	}
	if cursor != "" {
		if !nativeTime(stamp) {
			return transaction.Statement{}, fail("INVALID_CURSOR")
		}
		query += " AND (" + clock + "<? OR (" + clock + "=? AND " + table + ".ID<?))"
		args = append(args, stamp, stamp, cursor)
	}
	query += " ORDER BY " + clock + " DESC," + table + ".ID DESC LIMIT " + strconv.Itoa(limit+1)
	return distributionNativeBound(query, args), nil
}

// A native mutation must affect its one intended row. A statement failure exits
// immediately: no SELECT or retry may use the now-poisoned physical connection.
func distributionExecOne(ctx context.Context, q transaction.Executor, statement transaction.Statement) (int64, error) {
	count, e := q.Exec(ctx, statement)
	if e != nil {
		return count, e
	}
	if q.Dialect() == transaction.Dameng && count != 1 {
		return count, fail("STORAGE_UNAVAILABLE")
	}
	return count, nil
}

const nativeRecoveryFields = `ID,REQUEST_ID,WORKSPACE_ID,CASE WHEN CONFIRMED_AT IS NOT NULL THEN 'completed' WHEN INVALIDATED_AT IS NOT NULL THEN 'invalidated' WHEN SUPERSEDED_AT IS NOT NULL THEN 'superseded' WHEN EXPIRES_AT<=CURRENT_TIMESTAMP(6) THEN 'expired' WHEN FENCE_ACCEPTED_AT IS NOT NULL THEN 'fenced' ELSE 'pending_fence' END,CREATED_AT,EXPIRES_AT,FENCE_ACCEPTED_AT,CONFIRMED_AT,FENCE_RECEIPT`
