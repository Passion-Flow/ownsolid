package retention

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// PassSelected retains one installation's existing identity maintenance contract.
// Every selected key, mutation and audit belongs to the same actual transaction.
func PassSelected(ctx context.Context, store transaction.Store, policy config.RetentionPolicy) error {
	if ctx == nil {
		return errors.New("RETENTION_CONTEXT_REQUIRED")
	}
	if store == nil {
		return errors.New("RETENTION_STORE_REQUIRED")
	}
	if store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
		return errors.New("RETENTION_DIALECT_UNSUPPORTED")
	}
	check := config.DefaultRuntimePolicy()
	check.Retention = policy
	if e := check.Validate(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(policy.TimeoutSeconds))
	defer cancel()
	return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var bootstrapped bool
		var tenant, workspace string
		pg := `SELECT bootstrap_complete,COALESCE(tenant_id::text,''),COALESCE(default_workspace_id::text,'') FROM installation_state WHERE id=1 FOR SHARE`
		my := `SELECT bootstrap_complete,COALESCE(tenant_id,''),COALESCE(default_workspace_id,'') FROM installation_state WHERE id=1 FOR SHARE`
		if e := tx.QueryRow(ctx, statement(tx, pg, my)).Scan(&bootstrapped, &tenant, &workspace); e != nil {
			return e
		}
		if !bootstrapped {
			if tenant != "" || workspace != "" {
				return errors.New("RETENTION_INSTALLATION_INVALID")
			}
			return nil
		}
		if uuid.Validate(tenant) != nil || uuid.Validate(workspace) != nil {
			return errors.New("RETENTION_INSTALLATION_INVALID")
		}
		var installed string
		if e := tx.QueryRow(ctx, statement(tx, `SELECT id::text FROM tenants WHERE id=$1 AND status='active' FOR SHARE`, `SELECT id FROM tenants WHERE id=? AND status='active' FOR SHARE`, tenant)).Scan(&installed); e != nil {
			return e
		}
		var now time.Time
		if e := tx.QueryRow(ctx, statement(tx, `SELECT clock_timestamp()`, `SELECT UTC_TIMESTAMP(6)`)).Scan(&now); e != nil {
			return e
		}
		var changed int64
		for _, op := range operations(policy, tenant, now) {
			selectArgs := op.selectArgs
			if tx.Dialect() == transaction.MySQL && op.selectMyArgs != nil {
				selectArgs = op.selectMyArgs
			}
			rows, e := tx.Query(ctx, statement(tx, op.selectPG, op.selectMy, selectArgs...))
			if e != nil {
				return e
			}
			var selected []retainedKey
			for rows.Next() {
				var key retainedKey
				switch op.key {
				case scopedKey:
					var id, scope string
					e = rows.Scan(&id, &scope)
					key.values = []any{id}
					key.workspace = scope
				case binaryKey:
					var value []byte
					e = rows.Scan(&value)
					key.values = []any{append([]byte(nil), value...)}
				case compositeKey:
					var actor, operation, id string
					e = rows.Scan(&actor, &operation, &id)
					key.values = []any{actor, operation, id}
				default:
					var id string
					e = rows.Scan(&id)
					key.values = []any{id}
				}
				if e != nil {
					rows.Close()
					return e
				}
				selected = append(selected, key)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, key := range selected {
				args := append(append([]any{}, key.values...), op.mutationArgs...)
				if tx.Dialect() == transaction.MySQL && op.mysqlMutationArgs != nil {
					args = op.mysqlMutationArgs(key.values)
				}
				count, e := tx.Exec(ctx, statement(tx, op.mutatePG, op.mutateMy, args...))
				if e != nil {
					return e
				}
				if count < 0 || count > 1 {
					return errors.New("RETENTION_MUTATION_COUNT_INVALID")
				}
				if count == 0 {
					continue
				}
				changed += count
				if op.action != "" {
					scope := workspace
					if key.workspace != "" {
						scope = key.workspace
					}
					if e = writeScopedAudit(ctx, tx, tenant, scope, op.action, op.target, key.values[0].(string)); e != nil {
						return e
					}
				}
			}
		}
		if changed > 0 {
			return audit.WriteWithin(ctx, tx, "", "", "system", "system.retention", "success", "installation", "", "", "")
		}
		return nil
	})
}

func statement(tx transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if tx.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}

func writeScopedAudit(ctx context.Context, tx transaction.Executor, tenant, workspace, action, target, id string) error {
	_, e := tx.Exec(ctx, statement(tx,
		`INSERT INTO audit_events(id,occurred_at,audience,action,outcome,target_type,target_id,request_id,tenant_id,workspace_id) VALUES($1,clock_timestamp(),'system',$2,'success',$3,$4,$5,$6,$7)`,
		`INSERT INTO audit_events(id,occurred_at,audience,action,outcome,target_type,target_id,request_id,tenant_id,workspace_id) VALUES(?,UTC_TIMESTAMP(6),'system',?,'success',?,?,?,?,?)`, uuid.NewString(), action, target, id, uuid.NewString(), tenant, workspace))
	return e
}

type retainedKey struct {
	values    []any
	workspace string
}
type retentionKeyKind int

const (
	idKey retentionKeyKind = iota
	scopedKey
	binaryKey
	compositeKey
)

type retentionOperation struct {
	selectPG, selectMy, mutatePG, mutateMy string
	selectArgs, selectMyArgs, mutationArgs []any
	mysqlMutationArgs                      func([]any) []any
	key                                    retentionKeyKind
	action, target                         string
}

// These are literal owning-vendor queries, not translated SQL. The cutoff is
// obtained from the actual database clock once; key locks protect the recheck.
func operations(p config.RetentionPolicy, tenant string, now time.Time) []retentionOperation {
	mailCutoff := now.AddDate(0, 0, -p.MailDays)
	resetCutoff := now.AddDate(0, 0, -p.ResetDays)
	invitationCutoff := now.AddDate(0, 0, -p.InvitationDays)
	sessionCutoff := now.AddDate(0, 0, -p.SessionDays)
	rateCutoff := now.Add(-config.Seconds(p.RateGraceSeconds))
	auditCutoff := now.AddDate(0, 0, -p.AuditDays)
	return []retentionOperation{
		{
			selectPG:   `SELECT id::text,workspace_id::text FROM invitations WHERE tenant_id=$1 AND status='pending' AND expires_at<=$2 ORDER BY expires_at,id LIMIT $3 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT id,workspace_id FROM invitations WHERE tenant_id=? AND status='pending' AND expires_at<=? ORDER BY expires_at,id LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `UPDATE invitations SET status='expired',token_hash=NULL,version=version+1,updated_at=$3 WHERE id=$1 AND tenant_id=$2 AND status='pending' AND expires_at<=$3`,
			mutateMy:   `UPDATE invitations SET status='expired',token_hash=NULL,version=version+1,updated_at=? WHERE id=? AND tenant_id=? AND status='pending' AND expires_at<=?`,
			selectArgs: []any{tenant, now, p.BatchSize}, mutationArgs: []any{tenant, now}, mysqlMutationArgs: func(key []any) []any { return []any{now, key[0], tenant, now} }, key: scopedKey, action: "invitation.expired", target: "invitation",
		},
		{
			selectPG:   `SELECT id::text FROM password_reset_tokens WHERE consumed_at IS NULL AND revoked_at IS NULL AND expires_at<=$1 ORDER BY expires_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT id FROM password_reset_tokens WHERE consumed_at IS NULL AND revoked_at IS NULL AND expires_at<=? ORDER BY expires_at,id LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=$2 WHERE id=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at<=$2`,
			mutateMy:   `UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=? WHERE id=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at<=?`,
			selectArgs: []any{now, p.BatchSize}, mutationArgs: []any{now}, mysqlMutationArgs: func(key []any) []any { return []any{now, key[0], now} },
		},
		{
			selectPG:   `SELECT d.id::text,COALESCE(i.workspace_id::text,'') FROM mail_deliveries d LEFT JOIN invitations i ON i.id=d.invitation_id WHERE (d.invitation_id IS NULL OR i.tenant_id=$1) AND d.state IN ('queued','sending') AND ((d.invitation_id IS NOT NULL AND (i.status<>'pending' OR i.expires_at<=$2 OR i.token_generation<>d.invitation_generation)) OR (d.reset_token_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM password_reset_tokens r WHERE r.id=d.reset_token_id AND r.token_hash IS NOT NULL AND r.expires_at>$2))) ORDER BY d.created_at,d.id LIMIT $3 FOR UPDATE OF d SKIP LOCKED`,
			selectMy:   `SELECT d.id,COALESCE(i.workspace_id,'') FROM mail_deliveries d LEFT JOIN invitations i ON i.id=d.invitation_id WHERE (d.invitation_id IS NULL OR i.tenant_id=?) AND d.state IN ('queued','sending') AND ((d.invitation_id IS NOT NULL AND (i.status<>'pending' OR i.expires_at<=? OR i.token_generation<>d.invitation_generation)) OR (d.reset_token_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM password_reset_tokens r WHERE r.id=d.reset_token_id AND r.token_hash IS NOT NULL AND r.expires_at>?))) ORDER BY d.created_at,d.id LIMIT ? FOR UPDATE OF d SKIP LOCKED`,
			mutatePG:   `UPDATE mail_deliveries SET state='cancelled',payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=$2,updated_at=$2,error_category='token_obsolete' WHERE id=$1 AND state IN ('queued','sending') AND ((invitation_id IS NOT NULL AND EXISTS(SELECT 1 FROM invitations i WHERE i.id=invitation_id AND i.tenant_id=$3 AND (i.status<>'pending' OR i.expires_at<=$2 OR i.token_generation<>invitation_generation))) OR (reset_token_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM password_reset_tokens r WHERE r.id=reset_token_id AND r.token_hash IS NOT NULL AND r.expires_at>$2)))`,
			mutateMy:   `UPDATE mail_deliveries SET state='cancelled',payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=?,updated_at=?,error_category='token_obsolete' WHERE id=? AND state IN ('queued','sending') AND ((invitation_id IS NOT NULL AND EXISTS(SELECT 1 FROM invitations i WHERE i.id=invitation_id AND i.tenant_id=? AND (i.status<>'pending' OR i.expires_at<=? OR i.token_generation<>invitation_generation))) OR (reset_token_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM password_reset_tokens r WHERE r.id=reset_token_id AND r.token_hash IS NOT NULL AND r.expires_at>?)))`,
			selectArgs: []any{tenant, now, p.BatchSize}, selectMyArgs: []any{tenant, now, now, p.BatchSize}, mutationArgs: []any{now, tenant}, mysqlMutationArgs: func(key []any) []any { return []any{now, now, key[0], tenant, now, now} }, key: scopedKey, action: "mail.cancelled", target: "mail_delivery",
		},
		{
			selectPG:   `SELECT d.id::text FROM mail_deliveries d WHERE d.completed_at<$1 AND (d.invitation_id IS NULL OR EXISTS(SELECT 1 FROM invitations i WHERE i.id=d.invitation_id AND i.tenant_id=$2)) ORDER BY d.completed_at,d.id LIMIT $3 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT d.id FROM mail_deliveries d WHERE d.completed_at<? AND (d.invitation_id IS NULL OR EXISTS(SELECT 1 FROM invitations i WHERE i.id=d.invitation_id AND i.tenant_id=?)) ORDER BY d.completed_at,d.id LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `DELETE FROM mail_deliveries WHERE id=$1 AND completed_at<$2 AND (invitation_id IS NULL OR EXISTS(SELECT 1 FROM invitations i WHERE i.id=invitation_id AND i.tenant_id=$3))`,
			mutateMy:   `DELETE FROM mail_deliveries WHERE id=? AND completed_at<? AND (invitation_id IS NULL OR EXISTS(SELECT 1 FROM invitations i WHERE i.id=invitation_id AND i.tenant_id=?))`,
			selectArgs: []any{mailCutoff, tenant, p.BatchSize}, mutationArgs: []any{mailCutoff, tenant},
		},
		{
			selectPG:   `SELECT r.id::text FROM password_reset_tokens r WHERE COALESCE(r.consumed_at,r.revoked_at,r.expires_at)<$1 AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.reset_token_id=r.id) ORDER BY COALESCE(r.consumed_at,r.revoked_at,r.expires_at),r.id LIMIT $2 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT r.id FROM password_reset_tokens r WHERE COALESCE(r.consumed_at,r.revoked_at,r.expires_at)<? AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.reset_token_id=r.id) ORDER BY COALESCE(r.consumed_at,r.revoked_at,r.expires_at),r.id LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `DELETE FROM password_reset_tokens WHERE id=$1 AND COALESCE(consumed_at,revoked_at,expires_at)<$2 AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.reset_token_id=password_reset_tokens.id)`,
			mutateMy:   `DELETE FROM password_reset_tokens WHERE id=? AND COALESCE(consumed_at,revoked_at,expires_at)<? AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.reset_token_id=password_reset_tokens.id)`,
			selectArgs: []any{resetCutoff, p.BatchSize}, mutationArgs: []any{resetCutoff},
		},
		{
			selectPG:   `SELECT i.id::text FROM invitations i WHERE i.tenant_id=$1 AND i.status IN ('expired','revoked') AND i.updated_at<$2 AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.invitation_id=i.id) ORDER BY i.updated_at,i.id LIMIT $3 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT i.id FROM invitations i WHERE i.tenant_id=? AND i.status IN ('expired','revoked') AND i.updated_at<? AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.invitation_id=i.id) ORDER BY i.updated_at,i.id LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `DELETE FROM invitations WHERE id=$1 AND tenant_id=$2 AND status IN ('expired','revoked') AND updated_at<$3 AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.invitation_id=invitations.id)`,
			mutateMy:   `DELETE FROM invitations WHERE id=? AND tenant_id=? AND status IN ('expired','revoked') AND updated_at<? AND NOT EXISTS(SELECT 1 FROM mail_deliveries d WHERE d.invitation_id=invitations.id)`,
			selectArgs: []any{tenant, invitationCutoff, p.BatchSize}, mutationArgs: []any{tenant, invitationCutoff},
		},
		{
			selectPG:   `SELECT id::text FROM sessions WHERE COALESCE(revoked_at,absolute_expires_at)<$1 ORDER BY COALESCE(revoked_at,absolute_expires_at),id LIMIT $2 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT id FROM sessions WHERE COALESCE(revoked_at,absolute_expires_at)<? ORDER BY COALESCE(revoked_at,absolute_expires_at),id LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `DELETE FROM sessions WHERE id=$1 AND COALESCE(revoked_at,absolute_expires_at)<$2`,
			mutateMy:   `DELETE FROM sessions WHERE id=? AND COALESCE(revoked_at,absolute_expires_at)<?`,
			selectArgs: []any{sessionCutoff, p.BatchSize}, mutationArgs: []any{sessionCutoff},
		},
		{
			selectPG:   `SELECT bucket_key FROM rate_limit_buckets WHERE expires_at<$1 ORDER BY expires_at,bucket_key LIMIT $2 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT bucket_key FROM rate_limit_buckets WHERE expires_at<? ORDER BY expires_at,bucket_key LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `DELETE FROM rate_limit_buckets WHERE bucket_key=$1 AND expires_at<$2`,
			mutateMy:   `DELETE FROM rate_limit_buckets WHERE bucket_key=? AND expires_at<?`,
			selectArgs: []any{rateCutoff, p.BatchSize}, mutationArgs: []any{rateCutoff}, key: binaryKey,
		},
		{
			selectPG:   `SELECT actor_user_id::text,operation_id,idempotency_key::text FROM idempotency_records WHERE expires_at<=$1 ORDER BY expires_at,actor_user_id,operation_id,idempotency_key LIMIT $2 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT actor_user_id,operation_id,idempotency_key FROM idempotency_records WHERE expires_at<=? ORDER BY expires_at,actor_user_id,operation_id,idempotency_key LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `DELETE FROM idempotency_records WHERE actor_user_id=$1 AND operation_id=$2 AND idempotency_key=$3 AND expires_at<=$4`,
			mutateMy:   `DELETE FROM idempotency_records WHERE actor_user_id=? AND operation_id=? AND idempotency_key=? AND expires_at<=?`,
			selectArgs: []any{now, p.BatchSize}, mutationArgs: []any{now}, key: compositeKey,
		},
		{
			selectPG:   `SELECT id::text FROM audit_events WHERE (tenant_id=$1 OR tenant_id IS NULL) AND occurred_at<$2 ORDER BY occurred_at,id LIMIT $3 FOR UPDATE SKIP LOCKED`,
			selectMy:   `SELECT id FROM audit_events WHERE (tenant_id=? OR tenant_id IS NULL) AND occurred_at<? ORDER BY occurred_at,id LIMIT ? FOR UPDATE SKIP LOCKED`,
			mutatePG:   `DELETE FROM audit_events WHERE id=$1 AND (tenant_id=$2 OR tenant_id IS NULL) AND occurred_at<$3`,
			mutateMy:   `DELETE FROM audit_events WHERE id=? AND (tenant_id=? OR tenant_id IS NULL) AND occurred_at<?`,
			selectArgs: []any{tenant, auditCutoff, p.BatchSize}, mutationArgs: []any{tenant, auditCutoff},
		},
	}
}
