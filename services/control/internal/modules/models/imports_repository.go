package models

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type ImportResolutionStatus string

const (
	ImportResolutionFound  ImportResolutionStatus = "found"
	ImportResolutionAbsent ImportResolutionStatus = "absent"
)

type ImportResolution struct {
	Status ImportResolutionStatus `json:"status"`
	Import *Import                `json:"import"`
}

func normalizedImport(v Import) Import {
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return v
}

func importByKey(ctx context.Context, q transaction.Executor, tenant, workspace, key string) (Import, error) {
	return scanImport(q.QueryRow(ctx, modelNativeSQL(q, "SELECT "+nativeImportFields+" FROM MODEL_IMPORTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND IDEMPOTENCY_KEY=?",
		"SELECT "+importFields+" FROM model_imports WHERE tenant_id=$1 AND workspace_id=$2 AND idempotency_key=$3",
		"SELECT "+mysqlImportFields+" FROM model_imports WHERE tenant_id=? AND workspace_id=? AND idempotency_key=?",
		tenant, workspace, key)))
}

func (s *Service) startImport(ctx context.Context, a identity.Auth, input ImportRequest, key, request string) (Import, error) {
	if ctx == nil || !admin(a) {
		return Import{}, fail("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return Import{}, importMutationError(e)
	}
	name, e := security.Name(input.Name)
	if e != nil {
		return Import{}, e
	}
	input.Name = name
	if !validID(key) || !validID(input.WorkspaceID) || !reasonValid(input.Reason) || !input.LicenseAcknowledged || input.CatalogDigest != wire.ModelCatalogDigest() || input.ExpectedBytes < modelartifact.MinimumArchiveBytes() || input.ExpectedBytes > MaximumBundleBytes {
		return Import{}, fail("VALIDATION_FAILED")
	}
	intent, _ := json.Marshal(struct {
		Input ImportRequest
		Actor string
	}{input, a.Me.User.ID})
	fingerprint := sha256.Sum256(intent)
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var out Import
	e = s.importMutationOn(ctx, store, a, input.WorkspaceID, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		previous, e := importByKey(ctx, q, w.TenantID, w.ID, key)
		if e == nil {
			var original []byte
			if e = q.QueryRow(ctx, modelNativeSQL(q, `SELECT FINGERPRINT FROM MODEL_IMPORTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
				`SELECT fingerprint FROM model_imports WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`,
				`SELECT fingerprint FROM model_imports WHERE tenant_id=? AND workspace_id=? AND id=?`,
				w.TenantID, w.ID, previous.ID)).Scan(&original); e != nil {
				return e
			}
			if subtle.ConstantTimeCompare(original, fingerprint[:]) != 1 {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out = previous
			return nil
		}
		if e.Error() != "RESOURCE_NOT_FOUND" {
			return e
		}
		if input.ExpectedBytes > int64(s.Identity.Config.Policy().Models.MaximumBundleBytes) {
			return fail("VALIDATION_FAILED")
		}
		root, e := openStore(s.RootDirectory)
		if e != nil {
			return e
		}
		root.Close()
		var exists bool
		if e = q.QueryRow(ctx, modelNativeSQL(q, `SELECT CASE WHEN EXISTS(SELECT 1 FROM MODEL_IMPORTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND STATE NOT IN ('ready','cancelled')) THEN 1 ELSE 0 END FROM DUAL`,
			`SELECT EXISTS(SELECT 1 FROM model_imports WHERE tenant_id=$1 AND workspace_id=$2 AND state NOT IN ('ready','cancelled'))`,
			`SELECT EXISTS(SELECT 1 FROM model_imports WHERE tenant_id=? AND workspace_id=? AND state NOT IN ('ready','cancelled'))`, w.TenantID, w.ID)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return fail("MODEL_IMPORT_BUSY")
		}
		if e = q.QueryRow(ctx, modelNativeSQL(q, `SELECT CASE WHEN EXISTS(SELECT 1 FROM MODEL_VERSIONS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND CATALOG_DIGEST=? AND STATE='ready') THEN 1 ELSE 0 END FROM DUAL`,
			`SELECT EXISTS(SELECT 1 FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND catalog_digest=$3 AND state='ready')`,
			`SELECT EXISTS(SELECT 1 FROM model_versions WHERE tenant_id=? AND workspace_id=? AND catalog_digest=? AND state='ready')`, w.TenantID, w.ID, input.CatalogDigest)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return fail("MODEL_ALREADY_EXISTS")
		}
		id := uuid.NewString()
		if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `INSERT INTO MODEL_IMPORTS(ID,TENANT_ID,WORKSPACE_ID,NAME,CATALOG_DIGEST,EXPECTED_BYTES,STATE,REQUESTED_BY,IDEMPOTENCY_KEY,FINGERPRINT,REASON,REQUESTED_EPOCH) VALUES(?,?,?,?,?,?,'uploading',?,?,?,?,?)`,
			`INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,state,requested_by,idempotency_key,fingerprint,reason,requested_epoch) VALUES($1,$2,$3,$4,$5,$6,'uploading',$7,$8,$9,$10,$11)`,
			`INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,state,requested_by,idempotency_key,fingerprint,reason,requested_epoch) VALUES(?,?,?,?,?,?,'uploading',?,?,?,?,?)`,
			id, w.TenantID, w.ID, input.Name, input.CatalogDigest, input.ExpectedBytes, a.Me.User.ID, key, fingerprint[:], input.Reason, a.Epoch)); e != nil {
			return e
		}
		if e = importAuditWithin(ctx, q, a, w, "model.import_started", id, input.Reason, request); e != nil {
			return e
		}
		out, e = scopedImport(ctx, q, w.TenantID, w.ID, id)
		return e
	})
	if e != nil {
		return Import{}, e
	}
	return normalizedImport(out), nil
}

// ResolveImport observes a caller-retained operation key under the same Workspace
// fence as creation. It never resubmits a mutation or treats absence as success.
func (s *Service) ResolveImport(ctx context.Context, a identity.Auth, workspace, key string) (ImportResolution, error) {
	if !admin(a) {
		return ImportResolution{}, fail("ACCESS_DENIED")
	}
	if !validID(workspace) || !validID(key) {
		return ImportResolution{}, fail("VALIDATION_FAILED")
	}
	out := ImportResolution{Status: ImportResolutionAbsent}
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.modelScope(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		value, e := importByKey(ctx, q, w.TenantID, w.ID, key)
		if e != nil && e.Error() == "RESOURCE_NOT_FOUND" {
			return nil
		}
		if e != nil {
			return e
		}
		value = normalizedImport(value)
		out = ImportResolution{Status: ImportResolutionFound, Import: &value}
		return nil
	})
	if e != nil {
		return ImportResolution{}, e
	}
	return out, nil
}

func (s *Service) uploadChunk(ctx context.Context, a identity.Auth, id string, offset, size int64, digest string, source io.Reader) (Import, error) {
	initial, e := s.GetImport(ctx, a, id)
	if e != nil {
		return Import{}, e
	}
	if importCancellationPending(initial) {
		return Import{}, fail("MODEL_IMPORT_STATE_CONFLICT")
	}
	if source == nil || offset < 0 || offset%ChunkBytes != 0 || size < 1 || size > ChunkBytes || offset > initial.ExpectedBytes-size || size != min(ChunkBytes, initial.ExpectedBytes-offset) {
		return Import{}, fail("MODEL_CHUNK_INVALID")
	}
	store, e := s.repository()
	if e != nil {
		return Import{}, importMutationError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var out Import
	e = withImportFileLock(ctx, s.RootDirectory, id, func() error {
		return s.importMutationOn(ctx, store, a, initial.WorkspaceID, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			current, e := scopedImport(ctx, q, w.TenantID, w.ID, id)
			if e != nil {
				return e
			}
			if current.State != "uploading" || importCancellationPending(current) {
				return fail("MODEL_IMPORT_STATE_CONFLICT")
			}
			if offset > current.UploadedBytes {
				return fail("MODEL_CHUNK_OFFSET_MISMATCH")
			}
			var recorded string
			var recordedSize int64
			e = q.QueryRow(ctx, modelNativeSQL(q, `SELECT SHA256,SIZE_BYTES FROM MODEL_IMPORT_CHUNKS WHERE IMPORT_ID=? AND BYTE_OFFSET=?`,
				`SELECT sha256,size_bytes FROM model_import_chunks WHERE import_id=$1 AND byte_offset=$2`,
				`SELECT sha256,size_bytes FROM model_import_chunks WHERE import_id=? AND byte_offset=?`, id, offset)).Scan(&recorded, &recordedSize)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			exists := e == nil
			if exists && (recorded != digest || recordedSize != size || offset+size > current.UploadedBytes) {
				return fail("MODEL_CHUNK_CONFLICT")
			}
			if !exists && offset != current.UploadedBytes {
				return fail("MODEL_CHUNK_OFFSET_MISMATCH")
			}
			root, e := openStore(s.RootDirectory)
			if e != nil {
				return e
			}
			defer root.Close()
			if e = storeChunk(ctx, root, id, offset, size, digest, source); e != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return e
			}
			out = current
			if exists {
				return nil
			}
			if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `INSERT INTO MODEL_IMPORT_CHUNKS(IMPORT_ID,BYTE_OFFSET,SIZE_BYTES,SHA256) VALUES(?,?,?,?)`,
				`INSERT INTO model_import_chunks(import_id,byte_offset,size_bytes,sha256) VALUES($1,$2,$3,$4)`,
				`INSERT INTO model_import_chunks(import_id,byte_offset,size_bytes,sha256) VALUES(?,?,?,?)`, id, offset, size, digest)); e != nil {
				return e
			}
			if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET UPLOADED_BYTES=UPLOADED_BYTES+?,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
				`UPDATE model_imports SET uploaded_bytes=uploaded_bytes+$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`,
				`UPDATE model_imports SET uploaded_bytes=uploaded_bytes+?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, size, w.TenantID, w.ID, id)); e != nil {
				return e
			}
			out, e = scopedImport(ctx, q, w.TenantID, w.ID, id)
			return e
		})
	})
	if e != nil {
		return Import{}, importMutationError(e)
	}
	return normalizedImport(out), nil
}

func (s *Service) queueImport(ctx context.Context, a identity.Auth, id, reason, request string) (Import, error) {
	initial, e := s.GetImport(ctx, a, id)
	if e != nil {
		return Import{}, e
	}
	if importCancellationPending(initial) {
		return Import{}, fail("MODEL_IMPORT_STATE_CONFLICT")
	}
	if !reasonValid(reason) {
		return Import{}, fail("VALIDATION_FAILED")
	}
	store, e := s.repository()
	if e != nil {
		return Import{}, importMutationError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var out Import
	e = s.importMutationOn(ctx, store, a, initial.WorkspaceID, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := scopedImport(ctx, q, w.TenantID, w.ID, id)
		if e != nil {
			return e
		}
		if importCancellationPending(current) {
			return fail("MODEL_IMPORT_STATE_CONFLICT")
		}
		out = current
		if current.State == "ready" || current.State == "queued" || current.State == "verifying" {
			return nil
		}
		if current.State != "uploading" && current.State != "failed" || current.Attempts >= s.Identity.Config.Policy().Models.MaximumAttempts || current.UploadedBytes != current.ExpectedBytes {
			return fail("MODEL_IMPORT_INCOMPLETE")
		}
		if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET STATE='queued',REQUESTED_BY=?,REQUESTED_EPOCH=?,ERROR_CODE=NULL,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
			`UPDATE model_imports SET state='queued',requested_by=$1,requested_epoch=$2,error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND id=$5`,
			`UPDATE model_imports SET state='queued',requested_by=?,requested_epoch=?,error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, a.Me.User.ID, a.Epoch, w.TenantID, w.ID, id)); e != nil {
			return e
		}
		if e = importAuditWithin(ctx, q, a, w, "model.import_queued", id, reason, request); e != nil {
			return e
		}
		out, e = scopedImport(ctx, q, w.TenantID, w.ID, id)
		return e
	})
	if e != nil {
		return Import{}, e
	}
	return normalizedImport(out), nil
}
