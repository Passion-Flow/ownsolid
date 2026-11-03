package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"syscall"
)

func openArtifactSnapshot(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("OBJECT_SNAPSHOT_INVALID")
	}
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, errors.New("OBJECT_SNAPSHOT_INVALID")
	}
	defer root.Close()
	f, e := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errors.New("OBJECT_SNAPSHOT_INVALID")
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, errors.New("OBJECT_SNAPSHOT_INVALID")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		f.Close()
		return nil, errors.New("OBJECT_SNAPSHOT_INVALID")
	}
	return f, nil
}

// VerifyArtifactSnapshot grants no import/publication authority. Expected scope,
// namespace and whole digest are read from the actual selected product owner.
// The operator must hold the installation process lease, as the command does.
func VerifyArtifactSnapshot(ctx context.Context, accounts *identity.Service, workspace, object, path, reason string) (result error) {
	if accounts == nil || !operatorReason(reason) {
		return errors.New("OPERATOR_REASON_REQUIRED")
	}
	c := accounts.Config.ObjectStorage
	if c == nil || !c.Enabled {
		return errors.New("OBJECT_NOT_ADMITTED")
	}
	if !wire.ValidID(workspace) || !wire.ValidID(object) {
		return errors.New("VALIDATION_FAILED")
	}
	var expected artifactstores.SnapshotManifest
	read := func(ctx context.Context, q transaction.Executor) (artifactstores.SnapshotManifest, error) {
		var value artifactstores.SnapshotManifest
		if q.Dialect() != transaction.PostgreSQL {
			return value, errors.New("OBJECT_SCHEMA_NOT_ACTIVE")
		}
		e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), `SELECT o.tenant_id,o.workspace_id,o.id,o.store_id,o.purpose,o.sha256,o.expected_bytes FROM artifact_objects o JOIN installation_state i ON i.id=1 AND i.bootstrap_complete AND i.tenant_id=o.tenant_id WHERE o.workspace_id=$1 AND o.id=$2 AND o.state IN ('ready','deleted') FOR UPDATE OF o`, workspace, object)).Scan(&value.TenantID, &value.WorkspaceID, &value.ObjectID, &value.NamespaceID, &value.Purpose, &value.SHA256, &value.Bytes)
		value.Schema = 1
		value.BackendSHA256 = objectstore.ConfigurationIdentity(*c)
		return value, e
	}
	if e := restoreTransaction(ctx, accounts, func(ctx context.Context, q transaction.Executor) error {
		var e error
		expected, e = read(ctx, q)
		return e
	}); e != nil {
		return e
	}
	stage, e := objectstore.OpenStage(*c, expected.TenantID)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, stage.Close()) }()
	if stage.Namespace() != expected.NamespaceID || expected.Bytes > c.MaximumObjectBytes {
		return errors.New("OBJECT_STORE_CHANGED")
	}
	f, e := openArtifactSnapshot(path)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, f.Close()) }()
	before, e := f.Stat()
	if e != nil {
		return errors.New("OBJECT_SNAPSHOT_INVALID")
	}
	if e = artifactstores.VerifySnapshot(ctx, f, expected); e != nil {
		return e
	}
	after, e := f.Stat()
	if e != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("OBJECT_SNAPSHOT_INVALID")
	}
	return restoreTransaction(ctx, accounts, func(ctx context.Context, q transaction.Executor) error {
		current, e := read(ctx, q)
		if e != nil {
			return e
		}
		if current != expected {
			return errors.New("OBJECT_CHANGED")
		}
		return operatorAuditWithin(ctx, q, expected.TenantID, workspace, "object.snapshot_verified", "artifact_object", object, reason, map[string]any{"sha256": expected.SHA256, "bytes": expected.Bytes, "namespace_id": expected.NamespaceID})
	})
}
