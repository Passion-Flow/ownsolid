package scope

import (
	"bytes"
	"context"
	"io"
	"os"
	"syscall"
)

// The source task is read again without repeating its export/dump. Only an
// exact independently remeasured payload can recover an uncertain host copy.
func reconcileBackupPublication(ctx context.Context, c backupEngine, p BackupPlan, root *os.Root, role, id string, job BackupJob) error {
	var observation any
	var size int64
	var hash string
	if job.Store != nil {
		a, e := c.StreamStoreBackupArchive(ctx, *job.Store, id, p.Request.MaximumBytes, p.Request.MaximumEntries, p.Request.StreamSeconds, io.Discard)
		if e != nil {
			return e
		}
		observation, size, hash = a, a.Bytes, a.SHA256
	} else {
		a, e := c.StreamPostgresBackupArchive(ctx, *job.Postgres, id, p.Request.MaximumBytes, p.Request.StreamSeconds, io.Discard)
		if e != nil {
			return e
		}
		observation, size, hash = a, a.Bytes, a.SHA256
	}
	return finishBackupPublication(ctx, root, role+".tar", role+".json", size, hash, encode(observation))
}

func finishBackupPublication(ctx context.Context, root *os.Root, name, metadata string, size int64, hash string, raw []byte) error {
	if size < 1 || int64(len(raw)) > limitsBackupMetadataBytes {
		return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_CONTEXT_INVALID")
	}
	partial := name + ".partial"
	finalInfo, finalErr := root.Lstat(name)
	partialInfo, partialErr := root.Lstat(partial)
	finalExists, partialExists := finalErr == nil, partialErr == nil
	if finalErr != nil && !os.IsNotExist(finalErr) || partialErr != nil && !os.IsNotExist(partialErr) || !finalExists && !partialExists {
		return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_UNRESOLVED")
	}
	checkName, links := name, uint64(1)
	if partialExists {
		checkName = partial
	}
	if partialExists && finalExists {
		if !os.SameFile(finalInfo, partialInfo) || finalInfo.Mode() != 0600 || partialInfo.Mode() != 0600 || !owned(finalInfo) || !owned(partialInfo) {
			return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_PAIR_MISMATCH")
		}
		links = 2
	}
	observed, e := backupHashFileLinks(ctx, root, checkName, size, links)
	if e != nil || observed.Bytes != size || observed.SHA256 != hash {
		return failure("OFFLINE_SCOPE_BACKUP_PARTIAL_BYTES_UNRESOLVED")
	}
	f, e := root.OpenFile(checkName, os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_SYNC_FAILED")
	}
	e = f.Sync()
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_SYNC_FAILED")
	}
	if !finalExists {
		if e = root.Link(partial, name); e != nil {
			return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_UNCERTAIN")
		}
		if e = syncBackupDirectory(root, "."); e != nil {
			return e
		}
	}
	if partialExists {
		if e = root.Remove(partial); e != nil {
			return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_UNCERTAIN")
		}
		if e = syncBackupDirectory(root, "."); e != nil {
			return e
		}
	}
	if current, e := backupHashFile(ctx, root, name, size); e != nil || current.Bytes != size || current.SHA256 != hash {
		return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_READBACK_FAILED")
	}
	if _, e = root.Lstat(metadata); os.IsNotExist(e) {
		return writeBackupFile(root, metadata, raw)
	}
	existing, e := regular(root, metadata, limitsBackupMetadataBytes)
	if e != nil || !bytes.Equal(existing, raw) {
		return failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_METADATA_CHANGED")
	}
	return syncBackupDirectory(root, ".")
}
