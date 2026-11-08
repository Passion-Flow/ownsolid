package models

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// Reconcile one completed upload per iteration. Advance the cursor even when
// cleanup fails so that a damaged object cannot block cleanup of later imports.
func (s *Service) cleanupNextImport(ctx context.Context, after string) (string, error) {
	store, e := s.repository()
	if e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Models.ImportSeconds))
	defer cancel()
	if after == "" {
		after = uuid.Nil.String()
	}
	var id, tenant, workspace string
	e = store.QueryRow(ctx, modelNativeSQL(store,
		`SELECT M.ID,M.TENANT_ID,M.WORKSPACE_ID FROM MODEL_IMPORTS M JOIN INSTALLATION_STATE I ON I.TENANT_ID=M.TENANT_ID JOIN MODEL_IMPORT_CHUNKS C ON C.IMPORT_ID=M.ID AND C.BYTE_OFFSET=0 WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND M.STATE='ready' AND M.ID>? ORDER BY M.ID LIMIT 1`,
		`SELECT m.id::text,m.tenant_id::text,m.workspace_id::text FROM model_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id JOIN model_import_chunks c ON c.import_id=m.id AND c.byte_offset=0 WHERE i.id=1 AND i.bootstrap_complete AND m.state='ready' AND m.id>$1 ORDER BY m.id LIMIT 1`,
		`SELECT m.id,m.tenant_id,m.workspace_id FROM model_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id JOIN model_import_chunks c ON c.import_id=m.id AND c.byte_offset=0 WHERE i.id=1 AND i.bootstrap_complete=1 AND m.state='ready' AND m.id>? ORDER BY m.id LIMIT 1`, after)).Scan(&id, &tenant, &workspace)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	if e != nil {
		return "", normalizedWorkerError(ctx, store.Dialect(), e)
	}
	e = withImportFileLock(ctx, s.RootDirectory, id, func() error {
		return s.withImportSession(ctx, id, func(ctx context.Context, session transaction.Store) error {
			return s.cleanupPublishedImport(ctx, session, id, tenant, workspace)
		})
	})
	if e != nil && e.Error() == "MODEL_IMPORT_BUSY" {
		return id, nil
	}
	return id, normalizedWorkerError(ctx, store.Dialect(), e)
}

func (s *Service) cleanupPublishedImport(ctx context.Context, session transaction.Store, id, tenant, workspace string) error {
	var expectedBytes int64
	e := session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		current, e := workerImportRow(ctx, q, id, &workerImport{TenantID: tenant, Import: Import{WorkspaceID: workspace}})
		if e != nil {
			return e
		}
		ready, e := publicationReady(ctx, q, current)
		if e != nil {
			return e
		}
		if !ready || current.UploadedBytes != current.ExpectedBytes {
			return fail("MODEL_PUBLICATION_CONFLICT")
		}
		expectedBytes = current.ExpectedBytes
		return nil
	})
	if e != nil {
		return e
	}

	// A restored database may already describe a ready model without its
	// upload directory. Only verify files again when cleanup will remove data.
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return e
	}
	defer root.Close()
	if _, e = root.Lstat(filepath.Join("imports", id)); !os.IsNotExist(e) {
		if e != nil {
			return fail("MODEL_IO_FAILED")
		}
		if e = modelartifact.VerifyDirectory(ctx, filepath.Join(s.RootDirectory, "objects", id)); e != nil {
			return fail("MODEL_PUBLICATION_CONFLICT")
		}
	}
	if e = discardUpload(root, id, expectedBytes); e != nil {
		return e
	}

	return session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := q.Exec(ctx, modelNativeSQL(q,
			`DELETE FROM MODEL_IMPORT_CHUNKS WHERE IMPORT_ID=?`,
			`DELETE FROM model_import_chunks WHERE import_id=$1`,
			`DELETE FROM model_import_chunks WHERE import_id=?`, id))
		return e
	})
}
