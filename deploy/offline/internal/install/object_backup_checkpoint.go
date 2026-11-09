package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const objectBackupFormatVersion = 2

var (
	errObjectBackupManifest = errors.New("INSTALL_OBJECT_BACKUP_MANIFEST_INVALID: preserve the object checkpoint for inspection")
	errObjectBackupDump     = errors.New("INSTALL_OBJECT_BACKUP_DUMP_INVALID: use the checked logical dump from the same operation")
	errObjectBackupFiles    = errors.New("INSTALL_OBJECT_BACKUP_FILES_INVALID: verify the protected local backup inventory")
	errObjectBackupRecovery = errors.New("INSTALL_OBJECT_BACKUP_RECOVERY_HELD: reconcile the checkpoint and durable fence with the coordinator")
)

type objectBackupScope struct {
	SiteSHA256    string `json:"site_sha256"`
	OperationID   string `json:"operation_id"`
	Cluster       int64  `json:"cluster"`
	DatabaseOID   uint32 `json:"database_oid"`
	TenantID      string `json:"tenant_id"`
	StoreID       string `json:"store_id"`
	BackendSHA256 string `json:"backend_sha256"`
}

// objectBackupPin is obtained independently from the trusted operation. The
// fence digest identifies a durable hold and never contains a release token.
type objectBackupPin struct {
	Scope           objectBackupScope `json:"scope"`
	FenceSHA256     string            `json:"fence_sha256"`
	InventorySHA256 string            `json:"inventory_sha256"`
	CopyID          string            `json:"copy_id"`
	DumpSHA256      string            `json:"dump_sha256"`
}

// objectCopyConfirmation is supplied by the trusted provider owner only after
// a durable copy has been verified under provider and control exclusion.
type objectCopyConfirmation struct {
	Scope           objectBackupScope
	InventorySHA256 string
	ID              string
}

type objectBackupCheckpoint struct {
	FormatVersion int             `json:"format_version"`
	Binding       objectBackupPin `json:"object_recovery"`
	LocalBackup   backupManifest  `json:"local_backup"`
}

func (pin objectBackupPin) valid() bool {
	s := pin.Scope
	return wire.ValidSHA256(s.SiteSHA256) && validObjectBackupID(s.OperationID) &&
		s.Cluster != 0 && s.DatabaseOID != 0 && validObjectBackupID(s.TenantID) && validObjectBackupID(s.StoreID) &&
		wire.ValidSHA256(s.BackendSHA256) && wire.ValidSHA256(pin.FenceSHA256) &&
		wire.ValidSHA256(pin.InventorySHA256) && validObjectBackupID(pin.CopyID) && wire.ValidSHA256(pin.DumpSHA256)
}

// sealObjectBackup is the checkpoint hook for a future coordinator holding
// the operation lock and both fences. No ordinary local manifest is written.
func sealObjectBackup(ctx context.Context, stage string, local backupManifest, pin objectBackupPin, copy objectCopyConfirmation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !pin.valid() || copy.Scope != pin.Scope || copy.InventorySHA256 != pin.InventorySHA256 || copy.ID != pin.CopyID {
		return "", errObjectBackupScope
	}
	root, err := openObjectBackupRoot(stage)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := checkObjectBackupContents(ctx, root, local, pin); err != nil {
		return "", err
	}
	raw, err := json.Marshal(objectBackupCheckpoint{objectBackupFormatVersion, pin, local})
	if err != nil || len(raw) > encryptedManifestLimit {
		return "", errObjectBackupManifest
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := publishObjectBackupCheckpoint(ctx, root, raw); err != nil {
		return "", err
	}
	return wire.Digest(raw), nil
}

// verifyObjectBackup checks a sealed checkpoint against the exact manifest
// digest and pin held in the trusted coordinator's durable record. It does not
// stage provider writes or authorize reopening either writer.
func verifyObjectBackup(ctx context.Context, stage string, pin objectBackupPin, manifestSHA256 string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !pin.valid() || !wire.ValidSHA256(manifestSHA256) {
		return errObjectBackupRecovery
	}
	root, err := openObjectBackupRoot(stage)
	if err != nil {
		return err
	}
	defer root.Close()
	raw, err := readObjectBackupMetadata(ctx, root, backupManifestName, encryptedManifestLimit)
	if errors.Is(err, os.ErrNotExist) {
		return errObjectBackupRecovery
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var checkpoint objectBackupCheckpoint
	if err != nil || wire.Digest(raw) != manifestSHA256 ||
		jsonv2.Unmarshal(raw, &checkpoint, jsonv2.RejectUnknownMembers(true)) != nil ||
		checkpoint.FormatVersion != objectBackupFormatVersion {
		return errObjectBackupManifest
	}
	if checkpoint.Binding != pin {
		return errObjectBackupScope
	}
	return checkObjectBackupContents(ctx, root, checkpoint.LocalBackup, pin)
}

func checkObjectBackupContents(ctx context.Context, root *os.Root, local backupManifest, pin objectBackupPin) error {
	if local.FormatVersion != backupFormatVersion || local.SchemaVersion < 1 || len(local.Tables) == 0 ||
		local.Recovery != nil || local.Database.SHA256 != pin.DumpSHA256 {
		return errObjectBackupDump
	}
	members, err := encryptedMembers(local)
	if err != nil || checkRequiredBackupKeys(local) != nil {
		return errObjectBackupFiles
	}
	for _, member := range members {
		if err := verifyObjectBackupFile(ctx, root, member.Path, member.Bytes, member.SHA256); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if member.Path == backupDatabaseName {
				return errObjectBackupDump
			}
			return errObjectBackupFiles
		}
	}
	expected, err := readObjectBackupInventory(ctx, root, pin)
	if err != nil {
		return err
	}
	return verifyObjectBackupCopy(ctx, root, expected)
}

func openObjectBackupRoot(name string) (*os.Root, error) {
	real, err := filepath.EvalSymlinks(name)
	if err != nil || !canonicalPath(name) || real != name {
		return nil, errObjectBackupManifest
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, errObjectBackupManifest
	}
	if !privateObjectBackupDirectory(root, ".") {
		root.Close()
		return nil, errObjectBackupManifest
	}
	return root, nil
}

func privateObjectBackupDirectory(root *os.Root, name string) bool {
	info, err := root.Lstat(name)
	return err == nil && info.IsDir() && info.Mode().Perm() == 0o700 && objectBackupOwner(info)
}

func objectBackupOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func openObjectBackupFile(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	if name == "." || !fs.ValidPath(name) {
		return nil, nil, errObjectBackupManifest
	}
	for dir := filepath.Dir(name); dir != "."; dir = filepath.Dir(dir) {
		if !privateObjectBackupDirectory(root, dir) {
			return nil, nil, errObjectBackupManifest
		}
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 || !objectBackupOwner(before) ||
		// The checkpoint itself may still have its temporary link after an
		// interrupted atomic publication. Its exact digest is independently pinned.
		(name != backupManifestName && before.Sys().(*syscall.Stat_t).Nlink != 1) {
		return nil, nil, errObjectBackupManifest
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !sameObjectBackupFile(before, opened) {
		file.Close()
		return nil, nil, errObjectBackupManifest
	}
	return file, before, nil
}

func sameObjectBackupFile(before, after os.FileInfo) bool {
	first, firstOK := before.Sys().(*syscall.Stat_t)
	last, lastOK := after.Sys().(*syscall.Stat_t)
	return firstOK && lastOK && first.Nlink == last.Nlink && first.Uid == last.Uid && first.Gid == last.Gid &&
		os.SameFile(before, after) && before.Size() == after.Size() && before.Mode() == after.Mode() &&
		before.ModTime().Equal(after.ModTime())
}

func unchangedObjectBackupFile(root *os.Root, name string, file *os.File, before os.FileInfo) bool {
	after, statErr := file.Stat()
	current, pathErr := root.Lstat(name)
	return statErr == nil && pathErr == nil && sameObjectBackupFile(before, after) && sameObjectBackupFile(before, current)
}

func readObjectBackupMetadata(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	file, info, err := openObjectBackupFile(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() < 1 || info.Size() > limit {
		return nil, errObjectBackupManifest
	}
	raw, err := io.ReadAll(io.LimitReader(archiveReader{ctx, file}, info.Size()+1))
	if err != nil || int64(len(raw)) != info.Size() || !unchangedObjectBackupFile(root, name, file, info) {
		return nil, errObjectBackupManifest
	}
	return raw, nil
}

func verifyObjectBackupFile(ctx context.Context, root *os.Root, name string, bytes int64, digest string) error {
	if bytes < 0 || bytes == math.MaxInt64 || !wire.ValidSHA256(digest) {
		return errObjectBackupManifest
	}
	file, info, err := openObjectBackupFile(root, name)
	if err != nil {
		return err
	}
	defer file.Close()
	if info.Size() != bytes {
		return errObjectBackupManifest
	}
	if name == backupDatabaseName {
		var header [5]byte
		if _, err := file.ReadAt(header[:], 0); err != nil || string(header[:]) != "PGDMP" {
			return errObjectBackupDump
		}
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(archiveReader{ctx, file}, bytes+1))
	if err != nil || size != bytes || hex.EncodeToString(hash.Sum(nil)) != digest ||
		!unchangedObjectBackupFile(root, name, file, info) {
		return errObjectBackupManifest
	}
	return nil
}

func publishObjectBackupCheckpoint(ctx context.Context, root *os.Root, raw []byte) error {
	suffix, err := randomBytes(12)
	if err != nil {
		return err
	}
	name := ".object-manifest-" + hex.EncodeToString(suffix)
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errObjectBackupManifest
	}
	defer root.Remove(name)
	_, writeErr := file.Write(raw)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return errObjectBackupManifest
	}
	if err := root.Link(name, backupManifestName); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return errObjectBackupManifest
		}
		existing, err := readObjectBackupMetadata(ctx, root, backupManifestName, encryptedManifestLimit)
		if err != nil || !bytes.Equal(existing, raw) {
			return errObjectBackupManifest
		}
	}
	if err := root.Remove(name); err != nil {
		return errObjectBackupManifest
	}
	dir, err := root.Open(".")
	if err != nil {
		return errObjectBackupManifest
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return errObjectBackupManifest
	}
	return nil
}
