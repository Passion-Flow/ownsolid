package models

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

const (
	modelImportExpiryPending = "MODEL_IMPORT_EXPIRY_PENDING"
	modelImportExpired       = "MODEL_IMPORT_EXPIRED"
	importExpiryRequested    = "model.import_expiry_requested"
	importExpired            = "model.import_expired"
)

// ImportExpiryPolicy opts one workspace into reclaiming abandoned uploads on
// PostgreSQL. InactiveFor must be a whole number of seconds from one hour to
// 365 days. The caller must set the policy before starting the model worker.
type ImportExpiryPolicy struct {
	WorkspaceID string
	InactiveFor time.Duration
}

func (p ImportExpiryPolicy) valid() bool {
	return validID(p.WorkspaceID) && p.WorkspaceID != uuid.Nil.String() &&
		p.InactiveFor >= time.Hour && p.InactiveFor <= 365*24*time.Hour && p.InactiveFor%time.Second == 0
}

type importExpiryTarget struct {
	id, tenant, workspace string
}

func importExpiryPending(value Import) bool {
	return value.ErrorCode != nil && *value.ErrorCode == modelImportExpiryPending
}

func importUploadClosed(value Import) bool {
	return importCancellationPending(value) || importExpiryPending(value) ||
		value.ErrorCode != nil && *value.ErrorCode == modelImportExpired
}

func (s *Service) expireNextImport(ctx context.Context, after string) (string, error) {
	if s.ImportExpiry == nil {
		return "", nil
	}
	policy := *s.ImportExpiry
	if !policy.valid() {
		return "", fail("MODEL_IMPORT_EXPIRY_POLICY_INVALID")
	}
	store, e := s.repository()
	if e != nil {
		return "", e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return "", fail("MODEL_IMPORT_EXPIRY_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Models.ImportSeconds))
	defer cancel()
	if after == "" {
		after = uuid.Nil.String()
	}
	var target importExpiryTarget
	e = store.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
		SELECT m.id::text,m.tenant_id::text,m.workspace_id::text
		FROM model_imports m
		JOIN workspaces w ON (w.tenant_id,w.id)=(m.tenant_id,m.workspace_id)
		JOIN tenants t ON t.id=w.tenant_id
		JOIN installation_state i ON i.tenant_id=t.id AND i.id=1 AND i.bootstrap_complete
		WHERE w.id=$1 AND w.status='active' AND t.status='active' AND m.id>$2
		  AND m.state='uploading' AND m.uploaded_bytes<m.expected_bytes AND m.model_id IS NULL AND m.attempts=0
		  AND (m.error_code=$3 OR (m.error_code IS NULL AND m.updated_at<clock_timestamp()-make_interval(secs=>$4)))
		ORDER BY m.id LIMIT 1`, policy.WorkspaceID, after, modelImportExpiryPending, policy.InactiveFor.Seconds())).Scan(&target.id, &target.tenant, &target.workspace)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	if e != nil {
		return "", normalizedWorkerError(ctx, store.Dialect(), e)
	}
	e = s.expireImport(ctx, policy, target)
	if e != nil && e.Error() == "MODEL_IMPORT_BUSY" {
		return target.id, nil
	}
	return target.id, normalizedWorkerError(ctx, store.Dialect(), e)
}

func (s *Service) expireImport(ctx context.Context, policy ImportExpiryPolicy, target importExpiryTarget) error {
	if target.workspace != policy.WorkspaceID {
		return fail("MODEL_IMPORT_STATE_CONFLICT")
	}
	return withImportFileLock(ctx, s.RootDirectory, target.id, func() error {
		root, e := openStore(s.RootDirectory)
		if e != nil {
			return e
		}
		defer root.Close()
		return s.withImportSession(ctx, target.id, func(ctx context.Context, session transaction.Store) error {
			var current workerImport
			var manifest *importManifest
			var authorized bool
			e := session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
				active, e := target.lockActiveWorkspace(ctx, q)
				if e != nil || !active {
					return e
				}
				current, e = workerImportRow(ctx, q, target.id, target.scope())
				if e != nil {
					return e
				}
				if current.State != "uploading" || current.UploadedBytes >= current.ExpectedBytes ||
					current.ModelID != nil || current.Attempts != 0 {
					return nil
				}
				pending := importExpiryPending(current.Import)
				if !pending {
					if current.ErrorCode != nil {
						return nil
					}
					var stale bool
					e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
						`SELECT $1::timestamptz<clock_timestamp()-make_interval(secs=>$2)`,
						current.UpdatedAt, policy.InactiveFor.Seconds())).Scan(&stale)
					if e != nil || !stale {
						return e
					}
				}
				if _, e = publicationReady(ctx, q, current); e != nil {
					return e
				}
				manifest = resolveImportManifest(current.CatalogDigest)
				if manifest == nil || !manifest.admitsArchiveSize(current.ExpectedBytes) {
					return fail("MODEL_IMPORT_INCOMPLETE")
				}
				if _, e = root.Lstat(filepath.Join("objects", current.ID)); !os.IsNotExist(e) {
					if e != nil {
						return fail("MODEL_IO_FAILED")
					}
					return fail("MODEL_PUBLICATION_CONFLICT")
				}
				authorized = true
				if pending {
					return nil
				}
				if _, e = modelExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL,
					`UPDATE model_imports SET error_code=$1 WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`,
					modelImportExpiryPending, target.tenant, target.workspace, target.id)); e != nil {
					return e
				}
				return target.auditExpiry(ctx, q, importExpiryRequested)
			})
			// A commit error, including an unknown acknowledgement, leaves the
			// staging untouched. Recovery must read the committed intent first.
			if e != nil || !authorized {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			if e = discardUpload(root, target.id, current.ExpectedBytes, manifest); e != nil {
				return e
			}
			return session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
				active, e := target.lockActiveWorkspace(ctx, q)
				if e != nil {
					return e
				}
				if !active {
					return fail("MODEL_IMPORT_STATE_CONFLICT")
				}
				locked, e := workerImportRow(ctx, q, target.id, target.scope())
				if e != nil {
					return e
				}
				if !sameWorkerBinding(current, locked) || locked.State != "uploading" ||
					locked.ModelID != nil || locked.Attempts != 0 || !importExpiryPending(locked.Import) {
					return fail("MODEL_IMPORT_STATE_CONFLICT")
				}
				if _, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
					`DELETE FROM model_import_chunks WHERE import_id=$1`, target.id)); e != nil {
					return e
				}
				if _, e = modelExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL,
					`UPDATE model_imports SET state='cancelled',error_code=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`,
					modelImportExpired, target.tenant, target.workspace, target.id)); e != nil {
					return e
				}
				return target.auditExpiry(ctx, q, importExpired)
			})
		})
	})
}

func (t importExpiryTarget) scope() *workerImport {
	return &workerImport{TenantID: t.tenant, Import: Import{WorkspaceID: t.workspace}}
}

func (t importExpiryTarget) lockActiveWorkspace(ctx context.Context, q transaction.Executor) (bool, error) {
	var active bool
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
		SELECT w.status='active' AND t.status='active'
		FROM workspaces w
		JOIN tenants t ON t.id=w.tenant_id
		JOIN installation_state i ON i.tenant_id=t.id AND i.id=1 AND i.bootstrap_complete
		WHERE w.tenant_id=$1 AND w.id=$2 FOR NO KEY UPDATE OF w`, t.tenant, t.workspace)).Scan(&active)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return active, e
}

func (t importExpiryTarget) auditExpiry(ctx context.Context, q transaction.Executor, action string) error {
	// One expiry per import. Correlate the audit pair across retries without
	// depending on the audit retention window for authorization.
	_, e := modelExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL, `
		INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id)
		VALUES($1,clock_timestamp(),$2,$3,'system',$4,'success','model_import',$5,$6,$7)`,
		uuid.NewString(), t.tenant, t.workspace, action, t.id, modelImportExpired, t.id))
	return e
}
