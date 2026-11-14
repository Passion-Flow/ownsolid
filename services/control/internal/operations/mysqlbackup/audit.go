package mysqlbackup

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func auditMetadata(m BackupManifest, sha string) map[string]any {
	return map[string]any{"backup_id": m.BackupID, "manifest_sha256": sha, "sql_sha256": m.SQLSHA256, "database_readback_sha256": m.Data.SHA256, "native_artifact_manifest_sha256": m.NativeArtifactManifestSHA256, "source_binding_sha256": m.SourceBindingSHA256, "schema_steps": m.SchemaSteps}
}

func auditMatches(ctx context.Context, q transaction.Executor, tenant, action, target, id, reason string, metadata map[string]any) (bool, error) {
	raw, e := json.Marshal(metadata)
	if e != nil {
		return false, errors.New("MYSQL_BACKUP_AUDIT_INVALID")
	}
	var count, matching int
	e = q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT COUNT(*),COALESCE(SUM(id=? AND request_id=? AND tenant_id=? AND workspace_id IS NULL AND actor_user_id IS NULL AND subject_user_id IS NULL AND audience='operator' AND action=? AND outcome='success' AND target_type=? AND target_id=? AND reason=? AND JSON_CONTAINS(metadata,CAST(? AS JSON)) AND JSON_LENGTH(metadata)=?),0) FROM audit_events WHERE request_id=? OR id=?`, id, id, tenant, action, target, id, reason, string(raw), len(metadata), id, id)).Scan(&count, &matching)
	if e != nil || count > 1 || matching != count {
		return false, errors.New("MYSQL_BACKUP_AUDIT_CONFLICT")
	}
	return count == 1, nil
}
func writeAudit(ctx context.Context, q transaction.Executor, tenant, action, target, id, reason string, metadata map[string]any) error {
	raw, e := json.Marshal(metadata)
	if e != nil {
		return errors.New("MYSQL_BACKUP_AUDIT_INVALID")
	}
	_, e = q.Exec(ctx, transaction.SQL(transaction.MySQL, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator',?,'success',?,?,?,?,CAST(? AS JSON))`, id, tenant, action, target, id, reason, id, string(raw)))
	return e
}
func verifyTransactionTarget(ctx context.Context, q transaction.Executor, t ConnectionTarget, connection uint64) error {
	lock, e := mysqlmigrate.LockName(t.Database)
	if e != nil {
		return e
	}
	var exact bool
	if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT @@server_uuid=? AND DATABASE()=? AND CURRENT_USER()=? AND VERSION()=? AND IS_USED_LOCK(?)=?`, t.ServerUUID, t.Database, t.Principal, t.Version, lock, connection)).Scan(&exact) != nil || !exact {
		return errors.New("MYSQL_NATIVE_TRANSACTION_TARGET_MISMATCH")
	}
	return nil
}

// backupAudit neither guesses a COMMIT result nor adopts foreign history. Its
// read-only mode supports explicit reconciliation without another SQL write.
func backupAudit(ctx context.Context, o *mysqloperator.Context, m BackupManifest, sha string, mutate bool) (bool, error) {
	lease, e := o.Lease(ctx)
	if e != nil {
		return false, e
	}
	t, connection, e := connectionTarget(ctx, lease)
	if e != nil {
		return false, e
	}
	store, e := o.Store(ctx)
	if e != nil {
		return false, e
	}
	matched := false
	e = store.Within(ctx, !mutate, func(ctx context.Context, q transaction.Executor) error {
		if e := verifyTransactionTarget(ctx, q, t, connection); e != nil {
			return e
		}
		query := "SELECT id FROM installation_state WHERE id=1"
		if mutate {
			query += " FOR UPDATE"
		}
		var id int
		if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, query)).Scan(&id) != nil || id != 1 {
			return errors.New("MYSQL_BACKUP_ACTIVE_INSTALLATION_REQUIRED")
		}
		tenant, e := installedTenant(ctx, q)
		if e != nil || tenant != m.TenantID {
			return errors.New("MYSQL_BACKUP_INSTALLATION_CHANGED")
		}
		matched, e = auditMatches(ctx, q, m.TenantID, "operator.mysql_backup_exported", "mysql_backup", m.BackupID, m.Reason, auditMetadata(m, sha))
		if e != nil || matched || !mutate {
			return e
		}
		if e = writeAudit(ctx, q, m.TenantID, "operator.mysql_backup_exported", "mysql_backup", m.BackupID, m.Reason, auditMetadata(m, sha)); e != nil {
			return e
		}
		matched = true
		return nil
	})
	if e != nil {
		return false, e
	}
	if e = o.Verify(ctx); e != nil {
		return false, e
	}
	return matched, nil
}
