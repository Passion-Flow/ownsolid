package datasets

import (
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func (s *Service) readSnapshot(ctx context.Context, source string) (FileSnapshot, error) {
	var snapshot FileSnapshot
	if _, e := privatePath(source, "", true, 0); e != nil {
		return snapshot, failure("DATASET_SNAPSHOT_INVALID")
	}
	manifestPath := filepath.Join(source, snapshotName)
	if _, e := privatePath(manifestPath, source, false, s.policy().SnapshotManifestBytes); e != nil {
		return snapshot, failure("DATASET_SNAPSHOT_INVALID")
	}
	raw, e := os.ReadFile(manifestPath)
	if e != nil || wire.DecodeStrict(raw, &snapshot) != nil || snapshot.SchemaVersion != snapshotSchemaVersion(snapshot.References) || snapshot.Namespace.SchemaVersion != storeSchema || !wire.ValidID(snapshot.Namespace.StoreID) || snapshot.Namespace.TenantID != snapshot.References.TenantID {
		return snapshot, failure("DATASET_SNAPSHOT_INVALID")
	}
	expected, e := referenceFiles(snapshot.References)
	if e != nil {
		return snapshot, e
	}
	expected[markerFile] = SnapshotFile{Path: markerFile, SizeBytes: -1}
	if len(snapshot.Files) != len(expected) || len(snapshot.Files) > s.policy().SnapshotMaximumFiles {
		return snapshot, failure("DATASET_SNAPSHOT_INVALID")
	}
	var total int64
	allowedFiles := map[string]bool{snapshotName: true}
	allowedDirectories := map[string]bool{".": true}
	previous := ""
	for _, file := range snapshot.Files {
		required, exists := expected[file.Path]
		if !exists || file.Path <= previous || file.SizeBytes < 0 || file.SizeBytes > wire.MaximumResultBytesCeiling || !wire.ValidDigest(file.SHA256) || required.SHA256 != "" && required.SHA256 != file.SHA256 || required.SizeBytes >= 0 && required.SizeBytes != file.SizeBytes {
			return snapshot, failure("DATASET_SNAPSHOT_INVALID")
		}
		previous = file.Path
		delete(expected, file.Path)
		total += file.SizeBytes
		if total > s.policy().SnapshotMaximumBytes {
			return snapshot, failure("DATASET_SNAPSHOT_LIMIT_REACHED")
		}
		if file.Path == markerFile && file.SizeBytes > 4096 || strings.HasSuffix(file.Path, "/publication.json") && file.SizeBytes > 4096 {
			return snapshot, failure("DATASET_SNAPSHOT_INVALID")
		}
		absolute := filepath.Join(source, filepath.FromSlash(file.Path))
		if _, e = privatePath(absolute, source, false, file.SizeBytes); e != nil {
			return snapshot, failure("DATASET_SNAPSHOT_INVALID")
		}
		digest, n, e := contextFileDigest(ctx, absolute, file.SizeBytes)
		if e != nil || n != file.SizeBytes || digest != file.SHA256 {
			return snapshot, failure("DATASET_SNAPSHOT_INVALID")
		}
		allowedFiles[file.Path] = true
		for parent := path.Dir(file.Path); parent != "."; parent = path.Dir(parent) {
			allowedDirectories[parent] = true
		}
	}
	if len(expected) != 0 {
		return snapshot, failure("DATASET_SNAPSHOT_INVALID")
	}
	e = filepath.WalkDir(source, func(absolute string, entry fs.DirEntry, e error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(source, absolute)
		if e != nil {
			return e
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return failure("DATASET_SNAPSHOT_INVALID")
		}
		if entry.IsDir() {
			if !allowedDirectories[relative] {
				return failure("DATASET_SNAPSHOT_INVALID")
			}
			if relative != "." {
				_, e = privatePath(absolute, source, true, 0)
			}
			return e
		}
		if !allowedFiles[relative] {
			return failure("DATASET_SNAPSHOT_INVALID")
		}
		return nil
	})
	if e != nil {
		return snapshot, failure("DATASET_SNAPSHOT_INVALID")
	}
	raw, e = os.ReadFile(filepath.Join(source, markerFile))
	var marker storeNamespace
	if e != nil || wire.DecodeStrict(raw, &marker) != nil || !reflect.DeepEqual(marker, snapshot.Namespace) {
		return snapshot, failure("DATASET_SNAPSHOT_INVALID")
	}
	view := &Store{path: source, namespace: snapshot.Namespace}
	for _, v := range snapshot.References.Versions {
		if e = view.VerifyVersionContext(ctx, v.ID, v.DraftID, v.JobID, v.AttemptID, v.EffectiveSHA256, v.MappingSHA256, v.Manifest); e != nil {
			return snapshot, e
		}
	}
	return snapshot, nil
}

// ImportFiles only initializes an explicitly selected existing empty private root from a complete matching snapshot. The restored database must be in an isolated scope.
func (s *Service) ImportFiles(ctx context.Context, source, destination string) error {
	if s.Store != nil {
		return failure("DATASET_STORE_IN_USE")
	}
	if relatedDirectories(source, destination) {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.policy().SnapshotSeconds)*time.Second)
	defer cancel()
	snapshot, e := s.readSnapshot(bounded, source)
	if e != nil {
		return e
	}
	actual, e := s.snapshotReferences(bounded)
	if e != nil {
		return e
	}
	if !referencesEqual(actual, snapshot.References) {
		return failure("DATASET_SNAPSHOT_REFERENCE_CHANGED")
	}
	if _, e = privatePath(destination, "", true, 0); e != nil {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	entries, e := os.ReadDir(destination)
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if len(entries) != 0 {
		return failure("DATASET_SNAPSHOT_DESTINATION_EXISTS")
	}
	var required int64
	for _, file := range snapshot.Files {
		required += file.SizeBytes
	}
	if e = requireSnapshotSpace(destination, required, s.policy().MinimumFreeBytes); e != nil {
		return e
	}
	for _, name := range []string{"objects", "attempts", "versions"} {
		if os.Mkdir(filepath.Join(destination, name), 0700) != nil {
			return failure("DATASET_SNAPSHOT_DESTINATION_EXISTS")
		}
	}
	for _, file := range snapshot.Files {
		if e = copySnapshotFile(bounded, source, destination, file); e != nil {
			return e
		}
	}
	if e = writeNew(filepath.Join(destination, lockFile), []byte{}); e != nil {
		return e
	}
	if e = syncTree(destination); e != nil {
		return e
	}
	store, e := OpenStore(destination, snapshot.Namespace.TenantID, s.policy().MinimumFreeBytes)
	if e != nil {
		return e
	}
	defer store.Close()
	verifier := &Service{DB: s.DB, Identity: s.Identity, Store: store}
	if e = verifier.VerifyStored(bounded); e != nil {
		return e
	}
	latest, e := s.snapshotReferences(bounded)
	if e != nil {
		return e
	}
	if !referencesEqual(latest, snapshot.References) {
		return failure("DATASET_SNAPSHOT_REFERENCE_CHANGED")
	}
	return nil
}

func (s *Service) AuditFileOperation(ctx context.Context, operation, reason string) error {
	if ctx == nil || operation != "export" && operation != "import" || !validReason(reason) {
		return failure("VALIDATION_FAILED")
	}
	repo, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	e = repo.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		if e := q.QueryRow(ctx, datasetSQL(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete", nil, nil, transaction.SQL(transaction.Dameng, dmSnapshotImportTenant))).Scan(&tenant); e != nil {
			return e
		}
		id, request := uuid.NewString(), uuid.NewString()
		// Operator audit contains no backup paths, source bodies, keys or credentials.
		_, e := q.Exec(ctx, datasetSQL(q, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,reason,request_id) VALUES($1,clock_timestamp(),$2,'operator',$3,'success','dataset_store',$4,$5)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),?,'operator',?,'success','dataset_store',?,?)`, []any{id, tenant, "dataset.files_" + operation + "ed", reason, request}, []any{id, tenant, "dataset.files_" + operation + "ed", reason, request}, transaction.SQL(transaction.Dameng, dmSnapshotImportAudit, id, tenant, "dataset.files_"+operation+"ed", reason, request)))
		return e
	})
	return datasetRuntimeError(e)
}
