package models

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// CancelImport persists cancellation intent before destructive upload cleanup.
// A failed/unknown SQL or audit outcome never returns a success DTO.
func (s *Service) CancelImport(ctx context.Context, a identity.Auth, id, reason, request string) (Import, error) {
	initial, e := s.GetImport(ctx, a, id)
	if e != nil {
		return Import{}, e
	}
	if !reasonValid(reason) {
		return Import{}, fail("VALIDATION_FAILED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var out Import
	var expectedBytes int64
	e = withImportFileLock(ctx, s.RootDirectory, id, func() error {
		return s.withImportSession(ctx, id, func(ctx context.Context, session transaction.Store) error {
			terminal := false
			e := s.importMutationOn(ctx, session, a, initial.WorkspaceID, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
				current, e := scopedImport(ctx, q, w.TenantID, w.ID, id)
				if e != nil {
					return e
				}
				expectedBytes = current.ExpectedBytes
				if current.State == "ready" {
					return fail("MODEL_IMPORT_STATE_CONFLICT")
				}
				if current.State == "cancelled" {
					terminal = true
					out = current
					return nil
				}
				if importCancellationPending(current) {
					return nil
				}
				if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET ERROR_CODE=?,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
					`UPDATE model_imports SET error_code=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`,
					`UPDATE model_imports SET error_code=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`,
					modelCancellationPending, w.TenantID, w.ID, id)); e != nil {
					return e
				}
				return importAuditWithin(ctx, q, a, w, "model.import_cancellation_requested", id, reason, request)
			})
			if e != nil || terminal {
				return e
			}
			root, e := openStore(s.RootDirectory)
			if e != nil {
				return e
			}
			e = discardUpload(root, id, expectedBytes)
			root.Close()
			if e != nil {
				return e
			}
			return s.importMutationOn(ctx, session, a, initial.WorkspaceID, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
				current, e := scopedImport(ctx, q, w.TenantID, w.ID, id)
				if e != nil {
					return e
				}
				if current.State == "cancelled" {
					out = current
					return nil
				}
				if current.State == "ready" || !importCancellationPending(current) {
					return fail("MODEL_IMPORT_STATE_CONFLICT")
				}
				if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET STATE='cancelled',ERROR_CODE=NULL,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
					`UPDATE model_imports SET state='cancelled',error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`,
					`UPDATE model_imports SET state='cancelled',error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`,
					w.TenantID, w.ID, id)); e != nil {
					return e
				}
				if e = importAuditWithin(ctx, q, a, w, "model.import_cancelled", id, reason, request); e != nil {
					return e
				}
				out, e = scopedImport(ctx, q, w.TenantID, w.ID, id)
				return e
			})
		})
	})
	if e != nil {
		return Import{}, importMutationError(e)
	}
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return out, nil
}
func discardUpload(root *os.Root, id string, expectedBytes int64) error {
	if !validID(id) {
		return fail("MODEL_PATH_INVALID")
	}
	scope := "imports/" + id
	if _, e := root.Lstat(scope); os.IsNotExist(e) {
		return nil
	}
	if e := directory(root, scope); e != nil {
		return e
	}
	file, e := root.Open(scope)
	if e != nil {
		return fail("MODEL_CLEANUP_CONFLICT")
	}
	entries, e := file.ReadDir(300)
	file.Close()
	if e != nil && e != io.EOF {
		return fail("MODEL_CLEANUP_CONFLICT")
	}
	if len(entries) >= 300 {
		return fail("MODEL_CLEANUP_CONFLICT")
	}
	files := []string{}
	directories := []string{}
	chunkName := regexp.MustCompile(`^chunk-([0-9]{12})$`)
	temporaryName := regexp.MustCompile(`^\.upload-[a-f0-9]{32}$`)
	regular := func(path string) bool {
		info, e := root.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 != 0 {
			return false
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		return ok && owner.Uid == uint32(os.Geteuid()) && owner.Nlink == 1
	}
	for _, entry := range entries {
		path := filepath.Join(scope, entry.Name())
		if matched := chunkName.FindStringSubmatch(entry.Name()); matched != nil {
			offset, e := strconv.ParseInt(matched[1], 10, 64)
			if e != nil || offset%ChunkBytes != 0 || offset >= expectedBytes || !regular(path) {
				return fail("MODEL_CLEANUP_CONFLICT")
			}
			files = append(files, path)
		} else if temporaryName.MatchString(entry.Name()) {
			if !regular(path) {
				return fail("MODEL_CLEANUP_CONFLICT")
			}
			files = append(files, path)
		} else if entry.Name() == "stage-1" || entry.Name() == "stage-2" || entry.Name() == "stage-3" {
			if e := directory(root, path); e != nil {
				return e
			}
			stage, e := root.Open(path)
			if e != nil {
				return fail("MODEL_CLEANUP_CONFLICT")
			}
			children, e := stage.ReadDir(11)
			stage.Close()
			if e != nil && e != io.EOF {
				return fail("MODEL_CLEANUP_CONFLICT")
			}
			if len(children) > 9 {
				return fail("MODEL_CLEANUP_CONFLICT")
			}
			for _, child := range children {
				item := filepath.Join(path, child.Name())
				if _, ok := wire.ModelFileForName(child.Name()); !ok || !regular(item) {
					return fail("MODEL_CLEANUP_CONFLICT")
				}
				files = append(files, item)
			}
			directories = append(directories, path)
		} else {
			return fail("MODEL_CLEANUP_CONFLICT")
		}
	}
	for _, path := range files {
		if root.Remove(path) != nil {
			return fail("MODEL_IO_FAILED")
		}
	}
	for _, path := range directories {
		if root.Remove(path) != nil {
			return fail("MODEL_IO_FAILED")
		}
	}
	if root.Remove(scope) != nil {
		return fail("MODEL_IO_FAILED")
	}
	return syncDirectory(root, "imports")
}
