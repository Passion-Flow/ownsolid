package cloudinventory

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Every statement is owned SQL for one selected vendor. There is no SQL
// translation, compatibility mode, or fallback for an unknown dialect/query.
type awsQuery uint8

const (
	awsReceipt awsQuery = iota
	awsNewerReceipts
	awsReceiptCount
	awsMarkMissing
	awsUpsert
	awsResourceCount
	awsInsertReceipt
	awsAuditImport
	awsListResources
	awsUpdateResource
	awsInsertResource
)

func awsQueryText(dialect transaction.Dialect, query awsQuery) string {
	var pg, my, dm string
	switch query {
	case awsReceipt:
		pg = `SELECT idempotency_key::text,connection_name,account_id,region,started_at,completed_at,imported_count,actor_id::text FROM aws_import_receipts WHERE tenant_id=$1 AND workspace_id=$2 AND idempotency_key=$3`
		my = `SELECT idempotency_key,connection_name,account_id,region,started_at,completed_at,imported_count,actor_id FROM aws_import_receipts WHERE tenant_id=? AND workspace_id=? AND idempotency_key=?`
		dm = `SELECT IDEMPOTENCY_KEY,CONNECTION_NAME,ACCOUNT_ID,REGION,STARTED_AT,COMPLETED_AT,IMPORTED_COUNT,ACTOR_ID FROM AWS_IMPORT_RECEIPTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND IDEMPOTENCY_KEY=?`
	case awsNewerReceipts:
		pg = `SELECT count(*) FROM aws_import_receipts WHERE tenant_id=$1 AND workspace_id=$2 AND account_id=$3 AND region=$4 AND started_at>=$5`
		my = `SELECT count(*) FROM aws_import_receipts WHERE tenant_id=? AND workspace_id=? AND account_id=? AND region=? AND started_at>=?`
		dm = `SELECT COUNT(*) FROM AWS_IMPORT_RECEIPTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ACCOUNT_ID=? AND REGION=? AND STARTED_AT>=?`
	case awsReceiptCount:
		pg = `SELECT count(*) FROM aws_import_receipts WHERE tenant_id=$1 AND workspace_id=$2`
		my = `SELECT count(*) FROM aws_import_receipts WHERE tenant_id=? AND workspace_id=?`
		dm = `SELECT COUNT(*) FROM AWS_IMPORT_RECEIPTS WHERE TENANT_ID=? AND WORKSPACE_ID=?`
	case awsMarkMissing:
		pg = `UPDATE aws_discovered_resources SET missing=true WHERE tenant_id=$1 AND workspace_id=$2 AND account_id=$3 AND region=$4`
		my = `UPDATE aws_discovered_resources SET missing=1 WHERE tenant_id=? AND workspace_id=? AND account_id=? AND region=?`
		dm = `UPDATE AWS_DISCOVERED_RESOURCES SET MISSING=1 WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ACCOUNT_ID=? AND REGION=?`
	case awsUpsert:
		pg = `INSERT INTO aws_discovered_resources(id,tenant_id,workspace_id,account_id,region,instance_id,observation,observed_at,missing) VALUES($1,$2,$3,$4,$5,$6,$7,$8,false) ON CONFLICT(tenant_id,workspace_id,account_id,region,instance_id) DO UPDATE SET observation=EXCLUDED.observation,observed_at=EXCLUDED.observed_at,missing=false`
		my = `INSERT INTO aws_discovered_resources(id,tenant_id,workspace_id,account_id,region,instance_id,observation,observed_at,missing) VALUES(?,?,?,?,?,?,?,?,0) ON DUPLICATE KEY UPDATE observation=VALUES(observation),observed_at=VALUES(observed_at),missing=0`
	case awsResourceCount:
		pg = `SELECT count(*) FROM aws_discovered_resources WHERE tenant_id=$1 AND workspace_id=$2`
		my = `SELECT count(*) FROM aws_discovered_resources WHERE tenant_id=? AND workspace_id=?`
		dm = `SELECT COUNT(*) FROM AWS_DISCOVERED_RESOURCES WHERE TENANT_ID=? AND WORKSPACE_ID=?`
	case awsInsertReceipt:
		pg = `INSERT INTO aws_import_receipts(tenant_id,workspace_id,idempotency_key,actor_id,connection_name,account_id,region,started_at,completed_at,imported_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
		my = `INSERT INTO aws_import_receipts(tenant_id,workspace_id,idempotency_key,actor_id,connection_name,account_id,region,started_at,completed_at,imported_count) VALUES(?,?,?,?,?,?,?,?,?,?)`
		dm = `INSERT INTO AWS_IMPORT_RECEIPTS(TENANT_ID,WORKSPACE_ID,IDEMPOTENCY_KEY,ACTOR_ID,CONNECTION_NAME,ACCOUNT_ID,REGION,STARTED_AT,COMPLETED_AT,IMPORTED_COUNT) VALUES(?,?,?,?,?,?,?,?,?,?)`
	case awsAuditImport:
		pg = `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,'aws.inventory_imported','success','aws_import',$6,$7)`
		my = `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,'aws.inventory_imported','success','aws_import',?,?)`
		dm = `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,ACTOR_USER_ID,TENANT_ID,WORKSPACE_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REQUEST_ID) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,?,'aws.inventory_imported','success','aws_import',?,?)`
	case awsListResources:
		pg = `SELECT id::text,account_id,region,observation,observed_at,missing FROM aws_discovered_resources WHERE tenant_id=$1 AND workspace_id=$2 AND id>$3 ORDER BY id LIMIT $4`
		my = `SELECT id,account_id,region,observation,observed_at,missing FROM aws_discovered_resources WHERE tenant_id=? AND workspace_id=? AND id>? ORDER BY id LIMIT ?`
		dm = `SELECT ID,ACCOUNT_ID,REGION,OBSERVATION,OBSERVED_AT,CASE WHEN MISSING=1 THEN 1 ELSE 0 END FROM AWS_DISCOVERED_RESOURCES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID>? ORDER BY ID LIMIT ?`
	case awsUpdateResource:
		dm = `UPDATE AWS_DISCOVERED_RESOURCES SET OBSERVATION=?,OBSERVED_AT=?,MISSING=0 WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ACCOUNT_ID=? AND REGION=? AND INSTANCE_ID=?`
	case awsInsertResource:
		dm = `INSERT INTO AWS_DISCOVERED_RESOURCES(ID,TENANT_ID,WORKSPACE_ID,ACCOUNT_ID,REGION,INSTANCE_ID,OBSERVATION,OBSERVED_AT,MISSING) VALUES(?,?,?,?,?,?,?,?,0)`
	}
	switch dialect {
	case transaction.PostgreSQL:
		return pg
	case transaction.MySQL:
		return my
	case transaction.Dameng:
		return dm
	default:
		return ""
	}
}
func awsSQL(q transaction.Executor, query awsQuery, args ...any) transaction.Statement {
	return transaction.SQL(q.Dialect(), awsQueryText(q.Dialect(), query), args...)
}

// The caller holds the current exclusive Workspace fence in this transaction.
// Native UPDATE then INSERT preserves the original resource ID and passes CLOB
// observations directly, without an untyped MERGE source or a second transaction.
func upsertResource(ctx context.Context, q transaction.Executor, id, tenant, workspace, account, region string, instance Instance, raw string, observed time.Time) error {
	switch q.Dialect() {
	case transaction.Dameng:
		n, e := q.Exec(ctx, awsSQL(q, awsUpdateResource, raw, observed, tenant, workspace, account, region, instance.InstanceID))
		if e != nil {
			return e
		}
		if n == 1 {
			return nil
		}
		if n != 0 {
			return fail("STORAGE_UNAVAILABLE")
		}
		_, e = q.Exec(ctx, awsSQL(q, awsInsertResource, id, tenant, workspace, account, region, instance.InstanceID, raw, observed))
		return e
	case transaction.PostgreSQL, transaction.MySQL:
		_, e := q.Exec(ctx, awsSQL(q, awsUpsert, id, tenant, workspace, account, region, instance.InstanceID, raw, observed))
		return e
	default:
		return fail("AWS_BUSINESS_DATABASE_UNSUPPORTED")
	}
}
