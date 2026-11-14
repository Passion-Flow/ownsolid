package postgresbackup

import (
	"context"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type restoreAuthorityMetadata struct {
	Format              string `json:"format"`
	RestoreID           string `json:"restore_id"`
	IntentSHA256        string `json:"intent_sha256"`
	FilesSHA256         string `json:"files_sha256"`
	ManifestSHA256      string `json:"manifest_sha256"`
	ArchiveSHA256       string `json:"archive_sha256"`
	TargetBindingSHA256 string `json:"target_binding_sha256"`
	PostDataSHA256      string `json:"post_data_sha256"`
}

func restoreAuditID(i RestoreIntent) string {
	id, e := uuid.Parse(i.Request.RestoreID)
	if e != nil {
		return ""
	}
	return id.String()
}
func authorityMetadata(i RestoreIntent, intentSHA, filesSHA, post string) restoreAuthorityMetadata {
	return restoreAuthorityMetadata{"projectone.postgres.restore-authority.v1", i.Request.RestoreID, intentSHA, filesSHA, i.Request.SourceManifestSHA256, i.ArchiveSHA256, targetBinding(i.Target), post}
}

func checkRestoreTransaction(ctx context.Context, tx pgx.Tx, i RestoreIntent) error {
	var databaseName, operator, system, marker string
	var oid uint32
	var pid int
	var held bool
	if ctx == nil || tx == nil || tx.QueryRow(ctx, `SELECT pg_catalog.current_database(),CURRENT_USER,(SELECT system_identifier::text FROM pg_catalog.pg_control_system()),d.oid,pg_catalog.pg_backend_pid(),COALESCE(pg_catalog.shobj_description(d.oid,'pg_database'),''),EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE locktype='advisory' AND pid=pg_catalog.pg_backend_pid() AND classid=0::oid AND objid=$1::bigint::oid AND objsubid=1 AND mode='ExclusiveLock' AND granted AND database=d.oid) FROM pg_catalog.pg_database d WHERE d.datname=pg_catalog.current_database()`, database.InstallationLockID).Scan(&databaseName, &operator, &system, &oid, &pid, &marker, &held) != nil || !held || databaseName != i.Target.Database || operator != i.Target.Operator || system != i.Target.SystemIdentifier || oid != i.Target.DatabaseOID || pid != i.Target.BackendPID || marker != i.Target.Marker {
		return errors.New("POSTGRES_RESTORE_TARGET_SESSION_CHANGED")
	}
	return nil
}

func closeRestoreTransaction(ctx context.Context, tx pgx.Tx, seconds int) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(seconds))
	defer cancel()
	if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
		return errors.New("POSTGRES_RESTORE_TRANSACTION_CLOSE_UNCONFIRMED")
	}
	return nil
}

// The original cumulative owners and their bound post-image/audit commit in one
// caller-owned transaction. No domain can commit/release the installation lock.
func invalidateRestore(ctx context.Context, lease *pgxpool.Conn, accounts *identity.Service, i RestoreIntent, intentSHA, filesSHA string) (post string, result error) {
	if ctx == nil || lease == nil || accounts == nil || restoreAuditID(i) == "" {
		return "", errors.New("POSTGRES_RESTORE_AUTHORITY_CONTEXT_REQUIRED")
	}
	tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if e != nil {
		return "", errors.New("POSTGRES_RESTORE_TRANSACTION_UNAVAILABLE")
	}
	defer func() {
		if e := closeRestoreTransaction(ctx, tx, i.Request.Policy.TerminationGraceSeconds); e != nil {
			result = errors.Join(result, e)
			post = ""
		}
	}()
	if e = checkRestoreTransaction(ctx, tx, i); e != nil {
		return "", e
	}
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id=$1::uuid OR request_id=$1::uuid`, restoreAuditID(i)).Scan(&count) != nil || count != 0 {
		return "", errors.New("POSTGRES_RESTORE_AUDIT_CONFLICT")
	}
	if e = recovery.InvalidateRestoredWithin(ctx, accounts, transaction.PostgreSQLTransaction(tx), i.Request.Reason); e != nil {
		return "", e
	}
	data, e := readProfileDatabaseContents(ctx, tx, i.Request.Policy, restoreAuditID(i), i.Request.EffectiveApplicationProfile())
	if e != nil {
		return "", e
	}
	metadata, e := canonical(authorityMetadata(i, intentSHA, filesSHA, data.ContentSHA256))
	if e != nil {
		return "", e
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1::uuid,pg_catalog.clock_timestamp(),$2::uuid,'operator','operator.kubernetes_data_restored','success','postgres_restore',$1::uuid,$3,$1::uuid,$4::jsonb)`, restoreAuditID(i), i.TenantID, i.Request.Reason, string(metadata))
	if e != nil {
		return "", errors.New("POSTGRES_RESTORE_AUDIT_WRITE_FAILED")
	}
	if e = checkRestoreTransaction(ctx, tx, i); e != nil {
		return "", e
	}
	if tx.Commit(ctx) != nil {
		return "", errors.New("POSTGRES_RESTORE_AUTHORITY_COMMIT_UNKNOWN")
	}
	return data.ContentSHA256, nil
}

// Observe only: a missing/unknown original commit is never retried or fabricated.
func readRestoreAuthority(ctx context.Context, lease *pgxpool.Conn, i RestoreIntent, intentSHA, filesSHA string) (post string, result error) {
	current, e := readRestoreTarget(ctx, lease, i.Request, false)
	if e != nil || targetBinding(current) != targetBinding(i.Target) {
		return "", errors.New("POSTGRES_RESTORE_TARGET_CHANGED")
	}
	// A later observer has a new PID but must retain the exact durable target
	// binding and original intent hash. Never rewrite the historical intent.
	i.Target.BackendPID = current.BackendPID
	tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return "", errors.New("POSTGRES_RESTORE_TRANSACTION_UNAVAILABLE")
	}
	defer func() {
		if e := closeRestoreTransaction(ctx, tx, i.Request.Policy.TerminationGraceSeconds); e != nil {
			result = errors.Join(result, e)
			post = ""
		}
	}()
	if e = checkRestoreTransaction(ctx, tx, i); e != nil {
		return "", e
	}
	var raw []byte
	var tenant, audience, action, outcome, targetType, targetID, reason, request string
	if tx.QueryRow(ctx, `SELECT tenant_id::text,audience,action,outcome,target_type,target_id,reason,request_id::text,metadata FROM public.audit_events WHERE id=$1::uuid`, restoreAuditID(i)).Scan(&tenant, &audience, &action, &outcome, &targetType, &targetID, &reason, &request, &raw) != nil {
		return "", errors.New("POSTGRES_RESTORE_COMMITTED_AUDIT_REQUIRED")
	}
	var m restoreAuthorityMetadata
	if strict.DecodeStrict(raw, &m) != nil || tenant != i.TenantID || audience != "operator" || action != "operator.kubernetes_data_restored" || outcome != "success" || targetType != "postgres_restore" || !restoreAuditSQLIdentities(i, targetID, request) || reason != i.Request.Reason || !reflect.DeepEqual(m, authorityMetadata(i, intentSHA, filesSHA, m.PostDataSHA256)) || !digestPattern.MatchString(m.PostDataSHA256) {
		return "", errors.New("POSTGRES_RESTORE_COMMITTED_AUDIT_MISMATCH")
	}
	data, e := readProfileDatabaseContents(ctx, tx, i.Request.Policy, restoreAuditID(i), i.Request.EffectiveApplicationProfile())
	if e != nil || data.ContentSHA256 != m.PostDataSHA256 {
		return "", errors.New("POSTGRES_RESTORE_POST_IMAGE_CHANGED")
	}
	if e = checkRestoreTransaction(ctx, tx, i); e != nil {
		return "", e
	}
	return m.PostDataSHA256, nil
}

// audit_events.target_id and request_id are PostgreSQL UUID columns. Their
// canonical dashed readback is distinct from the compact record/scope ID.
func restoreAuditSQLIdentities(i RestoreIntent, target, request string) bool {
	id := restoreAuditID(i)
	return id != "" && target == id && request == id
}
