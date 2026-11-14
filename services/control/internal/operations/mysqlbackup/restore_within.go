package mysqlbackup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type restoreMetadata struct {
	RestoreID           string `json:"restore_id"`
	BackupID            string `json:"backup_id"`
	IntentSHA256        string `json:"intent_sha256"`
	FilesSHA256         string `json:"files_sha256"`
	ManifestSHA256      string `json:"manifest_sha256"`
	SQLSHA256           string `json:"sql_sha256"`
	SourceDataSHA256    string `json:"source_data_sha256"`
	PostDataSHA256      string `json:"post_data_sha256"`
	TargetBindingSHA256 string `json:"target_binding_sha256"`
	SchemaSteps         int    `json:"schema_steps"`
}

func restoreAuditMetadata(i RestoreIntent, intentSHA, filesSHA, postSHA string) restoreMetadata {
	return restoreMetadata{i.RestoreID, i.BackupID, intentSHA, filesSHA, i.ManifestSHA256, i.SQLSHA256, i.SourceDataSHA256, postSHA, i.TargetBindingSHA256, i.SchemaSteps}
}
func metadataMap(value restoreMetadata) (map[string]any, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	var result map[string]any
	e = json.Unmarshal(raw, &result)
	return result, e
}
func loadRestoreAudit(ctx context.Context, q transaction.Executor, i RestoreIntent, intentSHA, filesSHA string) (string, error) {
	var raw string
	e := q.QueryRow(ctx, transaction.SQL(transaction.MySQL, "SELECT CAST(metadata AS CHAR CHARACTER SET utf8mb4) FROM audit_events WHERE id=?", i.RestoreID)).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return "", errors.New("MYSQL_RESTORE_COMMITTED_AUDIT_REQUIRED")
	}
	if e != nil {
		return "", e
	}
	var stored restoreMetadata
	if strict.DecodeStrict([]byte(raw), &stored) != nil || !digestPattern.MatchString(stored.PostDataSHA256) || stored != restoreAuditMetadata(i, intentSHA, filesSHA, stored.PostDataSHA256) {
		return "", errors.New("MYSQL_RESTORE_AUDIT_CONFLICT")
	}
	metadata, e := metadataMap(stored)
	if e != nil {
		return "", e
	}
	exact, e := auditMatches(ctx, q, i.TenantID, restoreAuditAction, "mysql_restore", i.RestoreID, i.Reason, metadata)
	if e != nil {
		return "", e
	}
	if !exact {
		return "", errors.New("MYSQL_RESTORE_COMMITTED_AUDIT_REQUIRED")
	}
	return stored.PostDataSHA256, nil
}

// invalidateRestore commits the actual original owners, the post-image computed
// from those writes, and its exact audit in one transaction. Error/unknown never
// projects a post-image that a caller could treat as committed proof.
func invalidateRestore(ctx context.Context, o *mysqloperator.Context, accounts *identity.Service, i RestoreIntent, intentSHA, filesSHA string, m BackupManifest, p config.MySQLBackupConfiguration) (string, error) {
	if e := o.VerifyIdentity(ctx, accounts); e != nil {
		return "", e
	}
	lease, e := o.Lease(ctx)
	if e != nil {
		return "", e
	}
	target, connection, e := connectionTarget(ctx, lease)
	if e != nil {
		return "", e
	}
	if target != i.Target {
		return "", errors.New("MYSQL_RESTORE_TARGET_CHANGED")
	}
	store, e := o.Store(ctx)
	if e != nil {
		return "", e
	}
	postSHA := ""
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := verifyTransactionTarget(ctx, q, target, connection); e != nil {
			return e
		}
		var id int
		if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, "SELECT id FROM installation_state WHERE id=1 FOR UPDATE")).Scan(&id) != nil || id != 1 {
			return errors.New("MYSQL_BACKUP_ACTIVE_INSTALLATION_REQUIRED")
		}
		tenant, e := installedTenant(ctx, q)
		if e != nil || tenant != i.TenantID {
			return errors.New("MYSQL_RESTORE_INSTALLATION_CHANGED")
		}
		var count int
		if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, "SELECT COUNT(*) FROM audit_events WHERE request_id=? OR id=?", i.RestoreID, i.RestoreID)).Scan(&count) != nil || count != 0 {
			return errors.New("MYSQL_RESTORE_AUDIT_CONFLICT")
		}
		if e = recovery.InvalidateRestoredWithin(ctx, accounts, q, i.Reason); e != nil {
			return e
		}
		post, e := readDatabaseWithin(ctx, q, m.Data, p)
		if e != nil {
			return e
		}
		metadata, e := metadataMap(restoreAuditMetadata(i, intentSHA, filesSHA, post.SHA256))
		if e != nil {
			return e
		}
		if e = writeAudit(ctx, q, i.TenantID, restoreAuditAction, "mysql_restore", i.RestoreID, i.Reason, metadata); e != nil {
			return e
		}
		if e = verifyTransactionTarget(ctx, q, target, connection); e != nil {
			return e
		}
		postSHA = post.SHA256
		return nil
	})
	if e != nil {
		return "", e
	}
	if e = o.Verify(ctx); e != nil {
		return "", e
	}
	return postSHA, nil
}

// readCommittedRestore is always read-only. It requires the original recorded
// post-image; no absent or later audit can be synthesized from current state.
func readCommittedRestore(ctx context.Context, o *mysqloperator.Context, i RestoreIntent, intentSHA, filesSHA string, m BackupManifest, p config.MySQLBackupConfiguration, requireFresh bool) (string, error) {
	lease, e := o.Lease(ctx)
	if e != nil {
		return "", e
	}
	target, connection, e := connectionTarget(ctx, lease)
	if e != nil {
		return "", e
	}
	if target != i.Target {
		return "", errors.New("MYSQL_RESTORE_TARGET_CHANGED")
	}
	store, e := o.Store(ctx)
	if e != nil {
		return "", e
	}
	postSHA := ""
	e = store.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		if e := verifyTransactionTarget(ctx, q, target, connection); e != nil {
			return e
		}
		tenant, e := installedTenant(ctx, q)
		if e != nil || tenant != i.TenantID {
			return errors.New("MYSQL_RESTORE_INSTALLATION_CHANGED")
		}
		postSHA, e = loadRestoreAudit(ctx, q, i, intentSHA, filesSHA)
		if e != nil {
			return e
		}
		if requireFresh {
			actual, e := readDatabaseWithin(ctx, q, m.Data, p)
			if e != nil {
				return e
			}
			if actual.SHA256 != postSHA {
				return errors.New("MYSQL_RESTORE_POST_IMAGE_CHANGED")
			}
		}
		return verifyTransactionTarget(ctx, q, target, connection)
	})
	if e != nil {
		return "", e
	}
	if e = o.Verify(ctx); e != nil {
		return "", e
	}
	return postSHA, nil
}
