//go:build repository_integration

package models_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func fileOperationSetup(t *testing.T) humanImportFixture {
	t.Helper()
	x := archiveSetup(t)
	// Remove only the metadata-only read fixture, not an actual stored version.
	x.exec(t, `DELETE FROM model_versions WHERE id=$1`, `DELETE FROM model_versions WHERE id=?`, x.model)
	return x
}
func fileOperationScope(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func fileOperationTarget(t *testing.T) string {
	return filepath.Join(fileOperationScope(t), "snapshot")
}
func fileOperationEmpty(t *testing.T, root string) {
	t.Helper()
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatal("refused operation changed its destination", e)
	}
}
func fileOperationClone(x humanImportFixture, store transaction.Store) *models.Service {
	owner := *x.owner
	owner.Storage = store
	s := *x.s
	s.Identity = &owner
	ws := *x.s.Workspaces
	ws.Identity = &owner
	s.Workspaces = &ws
	return &s
}
func fileOperationAudit(t *testing.T, x humanImportFixture, action string, expected int) {
	t.Helper()
	if got := x.count(t, action, x.f.Tenant); got != expected {
		t.Fatal("actual operator audit count mismatch", action, got, expected)
	}
}
func fileOperationReady(t *testing.T, x humanImportFixture) string {
	t.Helper()
	v := x.start(t)
	archiveRecoveryObjects(t, x, v.ID)
	archiveQueue(t, x, v.ID)
	if owned, e := x.s.ProcessNext(x.ctx); !owned || e != nil {
		t.Fatal("actual approved recovery publication", e)
	}
	archiveReady(t, x, v.ID, 1)
	return v.ID
}

func TestActualModelFileOperations(t *testing.T) {
	for _, name := range []string{"empty_private_roundtrip_and_audit", "boundary_context_and_mixed_identity", "bootstrap_and_manifest_refusal", "pending_import_refuses_export_before_creation", "unknown_inventory_suppresses_files", "atomic_audit_rollback_and_unknown", "approved_objects_roundtrip_and_startup", "mismatch_tamper_and_populated_restore_refusal"} {
		t.Run(name, func(t *testing.T) {
			x := fileOperationSetup(t)
			switch name {
			case "empty_private_roundtrip_and_audit":
				backup := fileOperationTarget(t)
				if e := x.s.ExportFiles(x.ctx, backup); e != nil {
					t.Fatal(e)
				}
				raw, e := os.ReadFile(filepath.Join(backup, "model-files.json"))
				var inventory models.FileSnapshot
				if e != nil || json.Unmarshal(raw, &inventory) != nil || inventory.SchemaVersion != 1 || inventory.TenantID != x.f.Tenant || inventory.Models == nil || len(inventory.Models) != 0 {
					t.Fatal("canonical empty private snapshot required", e)
				}
				restored := *x.s
				restored.RootDirectory = fileOperationScope(t)
				if e = restored.ImportFiles(x.ctx, backup); e != nil {
					t.Fatal(e)
				}
				if e = restored.VerifyStoredVersions(x.ctx); e != nil {
					t.Fatal(e)
				}
				if e = x.s.AuditFileOperation(x.ctx, "export", "Synthetic complete empty snapshot"); e != nil {
					t.Fatal(e)
				}
				fileOperationAudit(t, x, "model.files_exported", 1)
				var metadata []byte
				if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT metadata FROM audit_events WHERE action='model.files_exported'`, `SELECT metadata FROM audit_events WHERE action='model.files_exported'`)).Scan(&metadata); e != nil {
					t.Fatal(e)
				}
				var recorded struct {
					InventorySHA256 string `json:"inventory_sha256"`
					Versions        int    `json:"model_versions"`
				}
				if json.Unmarshal(metadata, &recorded) != nil {
					t.Fatal("operator audit metadata invalid")
				}
				canonical, _ := json.Marshal(inventory)
				sum := sha256.Sum256(canonical)
				if recorded.InventorySHA256 != hex.EncodeToString(sum[:]) || recorded.Versions != 0 {
					t.Fatal("operator audit inventory digest mismatch")
				}
			case "boundary_context_and_mixed_identity":
				path := fileOperationTarget(t)
				modelReadError(t, x.s.ExportFiles(nil, path), "VALIDATION_FAILED")
				if _, e := os.Lstat(path); !os.IsNotExist(e) {
					t.Fatal("nil context created scope")
				}
				cancelled, cancel := context.WithCancel(x.ctx)
				cancel()
				modelReadError(t, x.s.ExportFiles(cancelled, path), string(transaction.Cancelled))
				bad := *x.s
				bad.Identity = &identity.Service{Storage: x.f.Store}
				modelReadError(t, bad.ExportFiles(x.ctx, path), "MODEL_STORAGE_MISMATCH")
				// An explicitly identified invalid pool pointer must be refused
				// before any method on this test double can be called.
				bad = *x.s
				bad.DB = &pgxpool.Pool{}
				modelReadError(t, bad.ExportFiles(x.ctx, path), "MODEL_STORAGE_MISMATCH")
				legacy := &models.Service{DB: x.f.PostgreSQL, RootDirectory: x.root}
				if x.f.Store.Dialect() == transaction.PostgreSQL {
					if e := legacy.ExportFiles(x.ctx, path); e != nil {
						t.Fatal("legacy PostgreSQL private constructor rejected", e)
					}
				} else {
					modelReadError(t, legacy.ExportFiles(x.ctx, path), "STORAGE_UNAVAILABLE")
				}
			case "bootstrap_and_manifest_refusal":
				x.exec(t, `UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1`, `UPDATE installation_state SET bootstrap_complete=FALSE,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1`)
				path := fileOperationTarget(t)
				modelReadError(t, x.s.ExportFiles(x.ctx, path), "STORAGE_UNAVAILABLE")
				if _, e := os.Lstat(path); !os.IsNotExist(e) {
					t.Fatal("unbootstrapped export created files")
				}
				x.exec(t, `UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, `UPDATE installation_state SET bootstrap_complete=TRUE,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1`, x.f.Tenant, x.f.Workspace, x.f.User)
				manifest, _ := json.Marshal(wire.ApprovedModel())
				x.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Invalid snapshot digest',$4,$5,$6::jsonb,'ready',$7)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Invalid snapshot digest',?,?,CAST(? AS JSON),'ready',?)`, uuid.NewString(), x.f.Tenant, x.f.Workspace, wire.ApprovedModel().ModelKey, strings.Repeat("a", 64), manifest, x.f.User)
				modelReadError(t, x.s.ExportFiles(x.ctx, path), "MODEL_FILE_INVALID")
				if _, e := os.Lstat(path); !os.IsNotExist(e) {
					t.Fatal("invalid metadata created files")
				}
			case "pending_import_refuses_export_before_creation":
				v := x.start(t)
				path := fileOperationTarget(t)
				modelReadError(t, x.s.ExportFiles(x.ctx, path), "MODEL_IMPORT_BUSY")
				if _, e := os.Lstat(path); !os.IsNotExist(e) {
					t.Fatal("pending import omitted from backup")
				}
				if _, e := x.s.CancelImport(x.ctx, x.admin, v.ID, "Remove actual isolated pending snapshot import", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				if e := x.s.ExportFiles(x.ctx, path); e != nil {
					t.Fatal("confirmed cancellation did not unblock snapshot", e)
				}
			case "unknown_inventory_suppresses_files":
				s := fileOperationClone(x, modelUnknownStore{Store: x.f.Store})
				path := fileOperationTarget(t)
				modelReadError(t, s.ExportFiles(x.ctx, path), string(transaction.CommitUnknown))
				if _, e := os.Lstat(path); !os.IsNotExist(e) {
					t.Fatal("unknown metadata transaction published filesystem scope")
				}
				if e := x.s.ExportFiles(x.ctx, path); e != nil {
					t.Fatal(e)
				}
				restored := *s
				restored.RootDirectory = fileOperationScope(t)
				modelReadError(t, restored.ImportFiles(x.ctx, path), string(transaction.CommitUnknown))
				fileOperationEmpty(t, restored.RootDirectory)
			case "atomic_audit_rollback_and_unknown":
				modelReadError(t, x.s.AuditFileOperation(x.ctx, "unapproved", "Synthetic reason"), "VALIDATION_FAILED")
				modelReadError(t, x.s.AuditFileOperation(x.ctx, "export", ""), "VALIDATION_FAILED")
				cf := cancellationFixture{modelReadFixture: x.modelReadFixture}
				repair := cf.auditFault(t, "model.files_exported")
				repaired := false
				defer func() {
					if !repaired {
						repair()
					}
				}()
				modelReadError(t, x.s.AuditFileOperation(x.ctx, "export", "Synthetic actual audit rollback"), "STORAGE_UNAVAILABLE")
				fileOperationAudit(t, x, "model.files_exported", 0)
				repair()
				repaired = true
				s := fileOperationClone(x, modelUnknownStore{Store: x.f.Store})
				modelReadError(t, s.AuditFileOperation(x.ctx, "export", "Explicit lost-outcome double after real audit commit"), string(transaction.CommitUnknown))
				fileOperationAudit(t, x, "model.files_exported", 1)
			case "approved_objects_roundtrip_and_startup":
				id := fileOperationReady(t, x)
				backup := fileOperationTarget(t)
				if e := x.s.ExportFiles(x.ctx, backup); e != nil {
					t.Fatal(e)
				}
				restored := *x.s
				restored.RootDirectory = fileOperationScope(t)
				if e := restored.ImportFiles(x.ctx, backup); e != nil {
					t.Fatal(e)
				}
				if e := restored.VerifyStoredVersions(x.ctx); e != nil {
					t.Fatal("actual restored files failed startup integrity", e)
				}
				if e := x.s.AuditFileOperation(x.ctx, "export", "Actual approved-object export"); e != nil {
					t.Fatal(e)
				}
				if e := restored.AuditFileOperation(x.ctx, "import", "Actual approved-object restore"); e != nil {
					t.Fatal(e)
				}
				fileOperationAudit(t, x, "model.files_exported", 1)
				fileOperationAudit(t, x, "model.files_imported", 1)
				archiveReady(t, x, id, 1)
			case "mismatch_tamper_and_populated_restore_refusal":
				id := fileOperationReady(t, x)
				backup := fileOperationTarget(t)
				if e := x.s.ExportFiles(x.ctx, backup); e != nil {
					t.Fatal(e)
				}
				metadata := filepath.Join(backup, "model-files.json")
				raw, e := os.ReadFile(metadata)
				if e != nil {
					t.Fatal(e)
				}
				var changed models.FileSnapshot
				if json.Unmarshal(raw, &changed) != nil {
					t.Fatal("actual snapshot metadata decode")
				}
				changed.TenantID = uuid.NewString()
				other, _ := json.Marshal(changed)
				if e = os.WriteFile(metadata, other, 0600); e != nil {
					t.Fatal(e)
				}
				restored := *x.s
				restored.RootDirectory = fileOperationScope(t)
				modelReadError(t, restored.ImportFiles(x.ctx, backup), "MODEL_SNAPSHOT_DATABASE_MISMATCH")
				fileOperationEmpty(t, restored.RootDirectory)
				if e = os.WriteFile(metadata, raw, 0600); e != nil {
					t.Fatal(e)
				}
				file := filepath.Join(backup, "objects", id, "config.json")
				original, e := os.ReadFile(file)
				if e != nil {
					t.Fatal(e)
				}
				// The actual export is immutable. Authorize corruption only on
				// this owned synthetic backup copy for the negative fixture.
				if e = os.Chmod(file, 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(file, []byte("Synthetic corrupted snapshot configuration"), 0600); e != nil {
					t.Fatal(e)
				}
				if e = restored.ImportFiles(x.ctx, backup); e == nil {
					t.Fatal("tampered snapshot accepted")
				}
				fileOperationEmpty(t, restored.RootDirectory)
				if e = os.WriteFile(file, original, 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.Chmod(file, 0444); e != nil {
					t.Fatal(e)
				}
				keep := filepath.Join(restored.RootDirectory, "keep.txt")
				if e = os.WriteFile(keep, []byte("Synthetic retained user data"), 0600); e != nil {
					t.Fatal(e)
				}
				modelReadError(t, restored.ImportFiles(x.ctx, backup), "MODEL_STORE_NOT_EMPTY")
				if b, e := os.ReadFile(keep); e != nil || string(b) != "Synthetic retained user data" {
					t.Fatal("populated restore changed existing data", e)
				}
			}
		})
	}
}
