package artifactstores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

// Keep the existing retryable conflict on the public API during a hold.
var errBackupFenced = fail("OBJECT_BUSY")

const (
	backupInventoryFormat = 1
	backupInventoryLimit  = 100_000
)

// BackupFence is an opaque process-local hold for a privileged coordinator
// that also excludes external writers.
type BackupFence struct {
	service *Service
	drained chan struct{}
	inUse   bool
}

// AcquireBackupFence blocks new artifactstores operations and waits for
// admitted ones within the caller's deadline. A canceled acquisition reopens
// admission. A successful acquisition stays held until Release.
func (s *Service) AcquireBackupFence(ctx context.Context) (*BackupFence, error) {
	if s == nil || s.lifecycle == nil || ctx == nil {
		return nil, fail("OBJECT_NOT_ADMITTED")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return nil, fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	if err := ctx.Err(); err != nil {
		return nil, safe(err)
	}
	l := s.lifecycle
	l.mu.Lock()
	if !s.admittedLocked() {
		l.mu.Unlock()
		return nil, fail("OBJECT_NOT_ADMITTED")
	}
	if l.backup != nil {
		l.mu.Unlock()
		return nil, errBackupFenced
	}
	f := &BackupFence{service: s, drained: make(chan struct{})}
	l.backup = f
	if l.calls == 0 {
		close(f.drained)
	}
	l.mu.Unlock()

	select {
	case <-f.drained:
	case <-ctx.Done():
	case <-l.ctx.Done():
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		l.backup = nil
		return nil, safe(err)
	}
	if !s.admittedLocked() {
		l.backup = nil
		return nil, fail("OBJECT_NOT_ADMITTED")
	}
	return f, nil
}

// Release reopens admission for this acquisition only. Retrying a completed
// release succeeds even if a later acquisition is held. A running verification
// must finish or be canceled first.
func (f *BackupFence) Release() error {
	if f == nil || f.service == nil {
		return fail("OBJECT_BACKUP_TOKEN_INVALID")
	}
	l := f.service.lifecycle
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.backup != f {
		return nil
	}
	if f.inUse {
		return fail("OBJECT_BACKUP_BUSY")
	}
	l.backup = nil
	return nil
}

// BackupObjectVersion identifies the expected content in the unversioned S3
// profile by SQL revision, ETag, size and SHA-256.
type BackupObjectVersion struct {
	WorkspaceID string  `json:"workspace_id"`
	ObjectID    string  `json:"object_id"`
	State       string  `json:"state"`
	Revision    int64   `json:"revision"`
	SizeBytes   int64   `json:"size_bytes"`
	SHA256      string  `json:"sha256"`
	ETag        *string `json:"etag"`
}

// BackupInventory binds ready content and terminal tombstones to a SQL
// inventory. The digest covers its JSON with an empty inventory_sha256 field.
type BackupInventory struct {
	FormatVersion   int                   `json:"format_version"`
	Database        string                `json:"database"`
	DatabaseOID     int64                 `json:"database_oid"`
	Schema          string                `json:"schema"`
	TenantID        string                `json:"tenant_id"`
	StoreID         string                `json:"store_id"`
	BackendSHA256   string                `json:"backend_sha256"`
	Objects         []BackupObjectVersion `json:"objects"`
	InventorySHA256 string                `json:"inventory_sha256"`
}

// VerifyInventory checks object bytes and settled operations under the fence.
// The caller supplies the deadline and maximum row count, including tombstones.
func (f *BackupFence) VerifyInventory(ctx context.Context, maximumObjects int) (BackupInventory, error) {
	return f.withBackupInventory(ctx, maximumObjects, func(run context.Context, inventory BackupInventory) error {
		for _, entry := range inventory.Objects {
			if err := f.service.verifyBackupObject(run, inventory.TenantID, entry, io.Discard); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f *BackupFence) withBackupInventory(ctx context.Context, maximumObjects int, verify func(context.Context, BackupInventory) error) (BackupInventory, error) {
	if f == nil || f.service == nil || ctx == nil || maximumObjects < 1 || maximumObjects > backupInventoryLimit {
		return BackupInventory{}, fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return BackupInventory{}, fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	s := f.service
	l := s.lifecycle
	l.mu.Lock()
	if l.backup != f || !s.admittedLocked() {
		l.mu.Unlock()
		return BackupInventory{}, fail("OBJECT_BACKUP_TOKEN_INVALID")
	}
	if f.inUse {
		l.mu.Unlock()
		return BackupInventory{}, fail("OBJECT_BACKUP_BUSY")
	}
	f.inUse = true
	l.active.Add(1)
	l.mu.Unlock()
	run, finish := l.operationContext(ctx)
	defer func() {
		l.mu.Lock()
		f.inUse = false
		l.mu.Unlock()
		finish()
	}()

	before, err := s.readBackupInventory(run, maximumObjects)
	if err != nil {
		return BackupInventory{}, err
	}
	if err := verify(run, before); err != nil {
		return BackupInventory{}, err
	}
	after, err := s.readBackupInventory(run, maximumObjects)
	if err != nil {
		return BackupInventory{}, err
	}
	if before.InventorySHA256 != after.InventorySHA256 {
		return BackupInventory{}, fail("OBJECT_BACKUP_INVENTORY_CHANGED")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := run.Err(); err != nil {
		return BackupInventory{}, safe(err)
	}
	if !s.admittedLocked() {
		return BackupInventory{}, fail("OBJECT_BACKUP_TOKEN_INVALID")
	}
	return before, nil
}

func (s *Service) readBackupInventory(ctx context.Context, limit int) (BackupInventory, error) {
	r, err := s.repository()
	if err != nil {
		return BackupInventory{}, err
	}
	if r.Dialect() != transaction.PostgreSQL {
		return BackupInventory{}, fail("OBJECT_BACKUP_DIALECT_UNSUPPORTED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	out := BackupInventory{
		FormatVersion: backupInventoryFormat,
		TenantID:      s.Stage.Tenant(),
		StoreID:       s.Stage.Namespace(),
		BackendSHA256: s.Stage.Identity(),
		Objects:       []BackupObjectVersion{},
	}
	err = r.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT d.datname::text, d.oid::bigint, n.nspname::text, i.tenant_id::text
			FROM pg_catalog.pg_database d
			JOIN pg_catalog.pg_class c ON c.oid='artifact_objects'::regclass
			JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
			CROSS JOIN installation_state i
			WHERE d.datname=current_database() AND i.id=1 AND i.bootstrap_complete`,
		)).Scan(&out.Database, &out.DatabaseOID, &out.Schema, &tenant); err != nil {
			return err
		}
		if tenant != out.TenantID {
			return fail("OBJECT_BACKUP_SCOPE_MISMATCH")
		}
		var unsettled bool
		if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT EXISTS(SELECT 1 FROM artifact_object_ingestions WHERE state IN ('intent','running'))`,
		)).Scan(&unsettled); err != nil {
			return err
		}
		if unsettled {
			return fail("OBJECT_BACKUP_UNSETTLED")
		}
		rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT o.tenant_id::text, o.store_id::text, o.workspace_id::text, o.id::text,
				o.state, o.revision, o.expected_bytes, o.sha256::text, o.remote_etag,
				o.operation_id IS NOT NULL OR (o.state='ready' AND EXISTS (
					SELECT 1 FROM artifact_object_parts p WHERE p.tenant_id=o.tenant_id
						AND p.workspace_id=o.workspace_id AND p.object_id=o.id AND p.state='intent'
				))
			FROM artifact_objects o ORDER BY o.workspace_id,o.id LIMIT $1`, limit+1))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Objects) == limit {
				return fail("OBJECT_BACKUP_INVENTORY_LIMIT")
			}
			var entry BackupObjectVersion
			var rowTenant, store string
			if err := rows.Scan(&rowTenant, &store, &entry.WorkspaceID, &entry.ObjectID, &entry.State,
				&entry.Revision, &entry.SizeBytes, &entry.SHA256, &entry.ETag, &unsettled); err != nil {
				return err
			}
			if rowTenant != out.TenantID || store != out.StoreID {
				return fail("OBJECT_BACKUP_SCOPE_MISMATCH")
			}
			if unsettled || entry.State != "ready" && entry.State != "deleted" && entry.State != "aborted" {
				return fail("OBJECT_BACKUP_UNSETTLED")
			}
			if entry.State == "ready" && (entry.ETag == nil || *entry.ETag == "") {
				return fail("OBJECT_BACKUP_UNSETTLED")
			}
			out.Objects = append(out.Objects, entry)
		}
		return rows.Err()
	})
	if err != nil {
		return BackupInventory{}, safe(err)
	}
	raw, _ := json.Marshal(out)
	digest := sha256.Sum256(raw)
	out.InventorySHA256 = hex.EncodeToString(digest[:])
	return out, nil
}

func (s *Service) verifyBackupObject(ctx context.Context, tenant string, entry BackupObjectVersion, destination io.Writer) error {
	ref := objectstore.Ref{Tenant: tenant, Workspace: entry.WorkspaceID, Object: entry.ObjectID}
	uploads, err := s.Transport.FindUploads(ctx, ref)
	if err != nil {
		return safe(err)
	}
	if len(uploads) != 0 {
		return fail("OBJECT_BACKUP_UNSETTLED")
	}
	if entry.State == "ready" {
		o := storedObject{Tenant: tenant, Object: Object{
			ID: entry.ObjectID, WorkspaceID: entry.WorkspaceID, ExpectedBytes: entry.SizeBytes, SHA256: entry.SHA256,
		}}
		_, err := s.verifyWholeTo(ctx, o, *entry.ETag, destination)
		return err
	}
	if _, err := s.Transport.Head(ctx, ref); err == nil {
		return fail("OBJECT_CHANGED")
	} else if err.Error() != "OBJECT_NOT_FOUND" {
		return safe(err)
	}
	return nil
}
