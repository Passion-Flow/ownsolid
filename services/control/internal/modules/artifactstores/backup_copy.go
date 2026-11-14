package artifactstores

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
)

// BackupCopy binds an exact staged PAX archive to the verified SQL inventory.
type BackupCopy struct {
	Inventory BackupInventory
	SizeBytes int64
	SHA256    string
}

// CopyInventory writes and verifies a portable object archive under the fence.
// The trusted coordinator supplies an exclusively opened, empty private file,
// the operation deadline, and limits on object count and total object bytes.
// It owns external-writer exclusion, disposal of a partial file, and durable
// publication with the SQL backup. A failed call always returns a zero copy.
func (f *BackupFence) CopyInventory(ctx context.Context, dst *os.File, maximumObjects int, maximumContentBytes int64) (BackupCopy, error) {
	// Leave space for tar headers, padding and the inventory in the int64
	// archive size, even when the caller sets an extreme content budget.
	if dst == nil || maximumContentBytes < 1 || maximumContentBytes > math.MaxInt64/2 {
		return BackupCopy{}, fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	info, e := dst.Stat()
	if e != nil || info.Mode() != 0600 || info.Size() != 0 {
		return BackupCopy{}, fail("OBJECT_BACKUP_COPY_DESTINATION_INVALID")
	}
	offset, e := dst.Seek(0, io.SeekCurrent)
	if e != nil || offset != 0 {
		return BackupCopy{}, fail("OBJECT_BACKUP_COPY_DESTINATION_INVALID")
	}
	var copy BackupCopy
	inventory, e := f.withBackupInventory(ctx, maximumObjects, func(run context.Context, inventory BackupInventory) error {
		remaining := maximumContentBytes
		for _, entry := range inventory.Objects {
			if entry.State != "ready" {
				continue
			}
			if entry.SizeBytes < 1 || entry.SizeBytes > remaining {
				return fail("OBJECT_BACKUP_COPY_LIMIT")
			}
			remaining -= entry.SizeBytes
		}
		size, digest, e := f.writeBackupCopy(run, dst, inventory)
		if e != nil {
			return e
		}
		copy.SizeBytes, copy.SHA256 = size, digest
		return nil
	})
	if e != nil {
		return BackupCopy{}, e
	}
	copy.Inventory = inventory
	return copy, nil
}

func (f *BackupFence) writeBackupCopy(ctx context.Context, dst *os.File, inventory BackupInventory) (int64, string, error) {
	produced := sha256.New()
	stream := &countWriter{writer: io.MultiWriter(backupCopyFile{dst}, produced)}
	archive := tar.NewWriter(snapshotContextWriter{ctx, stream})
	for _, entry := range inventory.Objects {
		if entry.State == "ready" {
			if !id(entry.WorkspaceID) || !id(entry.ObjectID) {
				return 0, "", fail("OBJECT_BACKUP_SCOPE_MISMATCH")
			}
			name := "objects/" + entry.WorkspaceID + "/" + entry.ObjectID
			if e := writeBackupHeader(archive, name, entry.SizeBytes); e != nil {
				return 0, "", safe(e)
			}
		}
		if e := f.service.verifyBackupObject(ctx, inventory.TenantID, entry, archive); e != nil {
			return 0, "", e
		}
	}
	manifest, e := json.Marshal(inventory)
	if e != nil {
		return 0, "", fail("OBJECT_BACKUP_COPY_UNAVAILABLE")
	}
	if e := writeBackupHeader(archive, "inventory.json", int64(len(manifest))); e != nil {
		return 0, "", safe(e)
	}
	if _, e := archive.Write(manifest); e != nil {
		return 0, "", safe(e)
	}
	if e := archive.Close(); e != nil {
		return 0, "", safe(e)
	}
	if e := dst.Sync(); e != nil {
		return 0, "", fail("OBJECT_BACKUP_COPY_UNAVAILABLE")
	}
	readback := sha256.New()
	input := io.NewSectionReader(dst, 0, stream.count+1)
	size, e := io.Copy(readback, snapshotContextReader{ctx, input})
	if ctx.Err() != nil {
		return 0, "", safe(ctx.Err())
	}
	if e != nil {
		return 0, "", fail("OBJECT_BACKUP_COPY_UNAVAILABLE")
	}
	digest := readback.Sum(nil)
	if size != stream.count || !bytes.Equal(produced.Sum(nil), digest) {
		return 0, "", fail("OBJECT_BACKUP_COPY_CHANGED")
	}
	return size, hex.EncodeToString(digest), nil
}

func writeBackupHeader(archive *tar.Writer, name string, size int64) error {
	return archive.WriteHeader(&tar.Header{Name: name, Size: size, Mode: 0600, Typeflag: tar.TypeReg, Format: tar.FormatPAX})
}

type backupCopyFile struct{ file *os.File }

func (w backupCopyFile) Write(p []byte) (int, error) {
	n, e := w.file.Write(p)
	if e != nil {
		return n, fail("OBJECT_BACKUP_COPY_UNAVAILABLE")
	}
	return n, nil
}
