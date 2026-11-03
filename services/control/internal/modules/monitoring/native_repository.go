package monitoring

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// These statements borrow Get's selected transaction. They create no store or
// authority: current actor, active Workspace and License rechecks remain in Get.
// DM has no admitted shared-row lock; lock both installed facts through commit.
func installationStatement(q transaction.Executor) transaction.Statement {
	if q == nil {
		return transaction.Statement{}
	}
	switch q.Dialect() {
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, `SELECT T."ID" FROM "INSTALLATION_STATE" I JOIN "TENANTS" T ON T."ID"=I."TENANT_ID" WHERE I."ID"=1 AND I."BOOTSTRAP_COMPLETE"=1 AND T."STATUS"='active' FOR UPDATE OF I."ID",T."ID"`)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, `SELECT t.id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t`)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, `SELECT t.id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE`)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}

func observationClockStatement(q transaction.Executor) transaction.Statement {
	if q == nil {
		return transaction.Statement{}
	}
	switch q.Dialect() {
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, `SELECT SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) FROM DUAL`)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, `SELECT clock_timestamp()`)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, `SELECT UTC_TIMESTAMP(6)`)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}

func monitoringNativeTime(stamp time.Time) bool {
	return stamp.Location() == time.UTC && stamp.Year() >= 1970 && stamp.Year() <= 9999 && stamp.Nanosecond()%1000 == 0
}

func observationTimeWithin(ctx context.Context, q transaction.Executor) (time.Time, error) {
	if ctx == nil || q == nil {
		return time.Time{}, errors.New("MONITORING_OWNER_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool {
		return time.Time{}, errors.New("MONITORING_TRANSACTION_REQUIRED")
	}
	var stamp time.Time
	if e := q.QueryRow(ctx, observationClockStatement(q)).Scan(&stamp); e != nil {
		return time.Time{}, e
	}
	stamp = stamp.UTC()
	if stamp.IsZero() || q.Dialect() == transaction.Dameng && !monitoringNativeTime(stamp) {
		return time.Time{}, errors.New("STORAGE_UNAVAILABLE")
	}
	return stamp, nil
}

// Policy validation precedes constructing any native literal LIMIT. Cursor
// timestamps are never rounded, and duplicated timestamp placeholders each bind
// the exact same microsecond value. All identifiers are fixed repository SQL.
func nodePageStatement(q transaction.Executor, tenant, workspace string, policy Config, at time.Time, id string) transaction.Statement {
	if q == nil {
		return transaction.Statement{}
	}
	if policy.Validate() != nil {
		return transaction.SQL(q.Dialect(), "")
	}
	if q.Dialect() == transaction.Dameng {
		if !validID(tenant) || !validID(workspace) || id == "" && !at.IsZero() || id != "" && (!validID(id) || !monitoringNativeTime(at)) {
			return transaction.SQL(transaction.Dameng, "")
		}
		query := `SELECT "ID","CREATED_AT" FROM "MANAGED_NODES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`
		args := []any{tenant, workspace}
		if id != "" {
			query += ` AND ("CREATED_AT"<? OR ("CREATED_AT"=? AND "ID"<?))`
			args = append(args, at, at, id)
		}
		query += ` ORDER BY "CREATED_AT" DESC,"ID" DESC LIMIT ` + strconv.Itoa(policy.NodePageSize+1)
		return transaction.SQL(transaction.Dameng, query, args...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		if id == "" {
			return transaction.SQL(transaction.PostgreSQL, `SELECT id::text,created_at FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3`, tenant, workspace, policy.NodePageSize+1)
		}
		return transaction.SQL(transaction.PostgreSQL, `SELECT id::text,created_at FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND (created_at,id)<($3,$4) ORDER BY created_at DESC,id DESC LIMIT $5`, tenant, workspace, at, id, policy.NodePageSize+1)
	}
	if q.Dialect() == transaction.MySQL {
		if id == "" {
			return transaction.SQL(transaction.MySQL, `SELECT id,created_at FROM managed_nodes WHERE tenant_id=? AND workspace_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, tenant, workspace, policy.NodePageSize+1)
		}
		return transaction.SQL(transaction.MySQL, `SELECT id,created_at FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND (created_at,id)<(?,?) ORDER BY created_at DESC,id DESC LIMIT ?`, tenant, workspace, at, id, policy.NodePageSize+1)
	}
	return transaction.SQL(q.Dialect(), "")
}

func snapshotAuditStatement(q transaction.Executor, audit, actor, workspace, request, tenant string) transaction.Statement {
	if q == nil {
		return transaction.Statement{}
	}
	args := []any{audit, actor, workspace, request, tenant, workspace}
	switch q.Dialect() {
	case transaction.Dameng:
		for _, value := range []string{audit, actor, workspace, request, tenant} {
			if !validID(value) {
				return transaction.SQL(transaction.Dameng, "")
			}
		}
		return transaction.SQL(transaction.Dameng, `INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","ACTOR_USER_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REQUEST_ID","TENANT_ID","WORKSPACE_ID") VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,'admin','monitoring.snapshot','success','workspace',?,?,?,?)`, args...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,request_id,tenant_id,workspace_id) VALUES($1,clock_timestamp(),$2,'admin','monitoring.snapshot','success','workspace',$3,$4,$5,$6)`, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,request_id,tenant_id,workspace_id) VALUES(?,UTC_TIMESTAMP(6),?,'admin','monitoring.snapshot','success','workspace',?,?,?,?)`, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}

func insertSnapshotAuditWithin(ctx context.Context, q transaction.Executor, audit, actor, workspace, request, tenant string) error {
	if ctx == nil || q == nil {
		return errors.New("MONITORING_OWNER_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool {
		return errors.New("MONITORING_TRANSACTION_REQUIRED")
	}
	count, e := q.Exec(ctx, snapshotAuditStatement(q, audit, actor, workspace, request, tenant))
	if e != nil {
		return e
	}
	if q.Dialect() == transaction.Dameng && count != 1 {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	return nil
}
