package training

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// VerifyInitializedStore inspects only a fresh empty namespace with the explicitly requested runtime owner.
// A root initializer need not impersonate the runtime UID or open its long-lived private store.
func VerifyInitializedStore(path, tenant string, uid, gid int) (string, error) {
	if !datawire.ValidID(tenant) || uid < 0 || gid < 0 || int64(uid) > 2147483647 || int64(gid) > 2147483647 || uid != os.Geteuid() && os.Geteuid() != 0 {
		return "", fail("TRAINING_STORAGE_PATH_INVALID")
	}
	check := func(path string, directory bool, maximum int64) (os.FileInfo, error) {
		actual, e := filepath.EvalSymlinks(path)
		if e != nil || actual != path || !filepath.IsAbs(path) {
			return nil, fail("TRAINING_STORAGE_PATH_INVALID")
		}
		info, e := os.Lstat(path)
		if e != nil {
			return nil, fail("TRAINING_STORAGE_PATH_INVALID")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(uid) || owner.Gid != uint32(gid) || directory && (!info.IsDir() || info.Mode().Perm() != 0700) || !directory && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600 || owner.Nlink != 1 || info.Size() > maximum) {
			return nil, fail("TRAINING_STORAGE_PATH_INVALID")
		}
		return info, nil
	}
	if _, e := check(path, true, 0); e != nil {
		return "", e
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return "", fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	defer root.Close()
	dir, e := root.Open(".")
	if e != nil {
		return "", fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	entries, e := dir.ReadDir(6)
	dir.Close()
	if e != nil && e != io.EOF || len(entries) != 5 {
		return "", fail("TRAINING_STORE_NAMESPACE_INVALID")
	}
	allowed := map[string]bool{namespaceFile: true, ownerLock: true, "uploads": true, "checkpoints": true, "artifacts": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return "", fail("TRAINING_STORE_NAMESPACE_INVALID")
		}
	}
	for _, name := range []string{"uploads", "checkpoints", "artifacts"} {
		if _, e = check(filepath.Join(path, name), true, 0); e != nil {
			return "", e
		}
		file, e := root.Open(name)
		if e != nil {
			return "", fail("TRAINING_STORAGE_UNAVAILABLE")
		}
		children, readError := file.ReadDir(1)
		file.Close()
		if len(children) > 0 || readError != nil && readError != io.EOF {
			return "", fail("TRAINING_STORE_NOT_EMPTY")
		}
	}
	if _, e = check(filepath.Join(path, ownerLock), false, 0); e != nil {
		return "", e
	}
	info, e := check(filepath.Join(path, namespaceFile), false, maximumNamespaceBytes)
	if e != nil {
		return "", e
	}
	file, e := root.Open(namespaceFile)
	if e != nil {
		return "", fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return "", fail("TRAINING_STORE_NAMESPACE_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximumNamespaceBytes+1))
	var namespace storeNamespace
	if e != nil || wire.DecodeStrict(raw, &namespace) != nil || namespace.SchemaVersion != 1 || namespace.Kind != "training" || namespace.TenantID != tenant || !datawire.ValidID(namespace.StoreID) {
		return "", fail("TRAINING_STORE_NAMESPACE_INVALID")
	}
	return namespace.StoreID, nil
}
func (s *Service) AuditInitializedStore(ctx context.Context, path string, uid, gid int, reason string) error {
	if !validReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	var tenant string
	check := func(ctx context.Context, q transaction.Executor) error {
		var current string
		var required bool
		e := q.QueryRow(ctx, acceptedAdapterSQL(q, `SELECT tenant_id::text,EXISTS(SELECT 1 FROM training_uploads u WHERE u.tenant_id=i.tenant_id) OR EXISTS(SELECT 1 FROM training_jobs j WHERE j.tenant_id=i.tenant_id AND j.state NOT IN ('succeeded','failed','cancelled')) FROM installation_state i WHERE id=1 AND bootstrap_complete`, `SELECT tenant_id,EXISTS(SELECT 1 FROM training_uploads u WHERE u.tenant_id=i.tenant_id) OR EXISTS(SELECT 1 FROM training_jobs j WHERE j.tenant_id=i.tenant_id AND j.state NOT IN ('succeeded','failed','cancelled')) FROM installation_state i WHERE id=1 AND bootstrap_complete=1`)).Scan(&current, &required)
		if e != nil {
			return fail("TRAINING_INSTALLATION_REQUIRED")
		}
		if required {
			return fail("TRAINING_STORAGE_REQUIRED")
		}
		if tenant != "" && tenant != current {
			return fail("TRAINING_STORE_NAMESPACE_INVALID")
		}
		tenant = current
		return nil
	}
	if e := s.trainingStorageTransaction(ctx, true, check); e != nil {
		return e
	}
	store, e := VerifyInitializedStore(path, tenant, uid, gid)
	if e != nil {
		return e
	}
	metadata, _ := json.Marshal(map[string]any{"store_id": store, "accepted_checkpoints": 0, "accepted_artifacts": 0, "source": "protected_local_operator", "referenced_files_verified": true, "runtime_uid": uid, "runtime_gid": gid})
	return s.trainingStorageTransaction(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := check(ctx, q); e != nil {
			return e
		}
		n, e := q.Exec(ctx, acceptedAdapterSQL(q, "INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,'operator','training.store_initialize_verified','success','training_store',$3,$4,$5,$6)", "INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator','training.store_initialize_verified','success','training_store',?,?,?,CAST(? AS JSON))", uuid.NewString(), tenant, store, reason, uuid.NewString(), metadata))
		if e == nil && n != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		return e
	})
}
