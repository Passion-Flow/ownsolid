package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const storeSnapshotRoot = "snapshot"
const maximumStoreArchiveEntries = 1000000

type StoreBackupArchive struct {
	Status        string              `json:"status"`
	Role          string              `json:"role"`
	Bytes         int64               `json:"bytes"`
	SHA256        string              `json:"sha256"`
	Files         []BackupArchiveFile `json:"files"`
	Qualification string              `json:"qualification"`
}

// The backend verifies snapshot semantics; this boundary verifies safe opaque
// transport. The caller owns exclusive host publication, fsync and readback.
func (c *Client) StreamStoreBackupArchive(ctx context.Context, t DatabaseTask, id string, maximumBytes int64, maximumEntries, seconds int, sink io.Writer) (StoreBackupArchive, error) {
	if _, ok := storeBackupTask(t.Kind); !ok || sink == nil || maximumBytes < archiveReadbackReserveBytes || maximumBytes > maximumBackupArchiveBytes || maximumEntries < 2 || maximumEntries > maximumStoreArchiveEntries || seconds < c.config.DialSeconds || seconds > c.config.LoadSeconds {
		return StoreBackupArchive{}, failure("OFFLINE_ENGINE_STORE_BACKUP_STREAM_BOUND_INVALID")
	}
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return StoreBackupArchive{}, e
	}
	if before.State.Status != "exited" || before.State.ExitCode != 0 || before.State.OOMKilled {
		return StoreBackupArchive{}, failure("OFFLINE_ENGINE_SUCCESSFUL_STORE_BACKUP_TASK_REQUIRED")
	}
	if e = c.databaseTaskNetwork(ctx, t, id, true); e != nil {
		return StoreBackupArchive{}, e
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	address := unixHTTPAuthority + "/v" + c.policy.APIVersion + "/containers/" + id + "/archive?" + (url.Values{"path": {StoreBackupDestination + "/private/" + storeSnapshotRoot}}).Encode()
	request, e := http.NewRequestWithContext(bounded, http.MethodGet, address, nil)
	if e != nil {
		return StoreBackupArchive{}, failure("OFFLINE_ENGINE_STORE_BACKUP_STREAM_REQUEST_INVALID")
	}
	response, e := c.http.Do(request)
	if e != nil {
		return StoreBackupArchive{}, failure("OFFLINE_ENGINE_STORE_BACKUP_STREAM_TRANSPORT_OR_DEADLINE")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maximumBytes {
		return StoreBackupArchive{}, failure("OFFLINE_ENGINE_STORE_BACKUP_STREAM_RESPONSE_INVALID")
	}
	result, e := readStoreBackupArchive(response.Body, sink, t, maximumBytes, maximumEntries)
	if e != nil {
		return result, e
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || after.State != before.State {
		return StoreBackupArchive{}, failure("OFFLINE_ENGINE_STORE_BACKUP_CHANGED_DURING_READBACK")
	}
	if e = c.databaseTaskNetwork(ctx, t, id, true); e != nil {
		return StoreBackupArchive{}, e
	}
	return result, nil
}

func readStoreBackupArchive(source io.Reader, sink io.Writer, t DatabaseTask, maximumBytes int64, maximumEntries int) (StoreBackupArchive, error) {
	profile, ok := storeBackupTask(t.Kind)
	if !ok || !t.valid() {
		return StoreBackupArchive{}, failure("OFFLINE_ENGINE_STORE_BACKUP_ARCHIVE_CONTEXT_INVALID")
	}
	return readStoreSnapshotArchive(source, sink, profile.role, t.UID, t.GID, maximumBytes, maximumEntries, nil, nil, "")
}

func readStoreSnapshotArchive(source io.Reader, sink io.Writer, role string, uid, gid int, maximumBytes int64, maximumEntries int, directories *[]string, fileModes map[string]int64, outerRoot string) (StoreBackupArchive, error) {
	result := StoreBackupArchive{Role: role, Files: []BackupArchiveFile{}}
	validRole := role == "models" || role == "datasets" || role == "training" || role == "preparations"
	if !validRole || uid < 1 || gid < 1 || source == nil || sink == nil || maximumBytes < archiveReadbackReserveBytes || maximumBytes > maximumBackupArchiveBytes || maximumEntries < 2 || maximumEntries > maximumStoreArchiveEntries {
		return result, failure("OFFLINE_ENGINE_STORE_BACKUP_ARCHIVE_CONTEXT_INVALID")
	}
	digest := sha256.New()
	counter := &backupArchiveCounter{reader: io.TeeReader(io.LimitReader(source, maximumBytes+1), io.MultiWriter(sink, digest))}
	reader := tar.NewReader(counter)
	seen := map[string]byte{}
	outerSeen := map[string]bool{}
	for {
		beforeHeader := counter.bytes
		h, e := reader.Next()
		if e == io.EOF {
			if counter.bytes-beforeHeader < tarEndBytes {
				return result, failure("OFFLINE_ENGINE_STORE_BACKUP_TAR_END_REQUIRED")
			}
			break
		}
		if e != nil {
			return result, failure("OFFLINE_ENGINE_STORE_BACKUP_TAR_INVALID")
		}
		name := strings.TrimSuffix(h.Name, "/")
		paxSafe := true
		for key, value := range h.PAXRecords {
			if key != "path" || value != h.Name {
				paxSafe = false
			}
		}
		if outerRoot != "" {
			if name == outerRoot {
				ownerUID, ownerGID, mode := 0, 0, int64(0755)
				if outerSeen[name] || h.Typeflag != tar.TypeDir || h.Mode != mode || h.Size != 0 || h.Uid != ownerUID || h.Gid != ownerGID || h.Linkname != "" || len(h.PAXRecords) > 0 || len(h.Xattrs) > 0 {
					return result, failure("OFFLINE_ENGINE_STORE_RESTORE_OUTER_INVALID")
				}
				outerSeen[name] = true
				continue
			}
			if !outerSeen[outerRoot] || !strings.HasPrefix(name, outerRoot+"/") {
				return result, failure("OFFLINE_ENGINE_STORE_RESTORE_OUTER_INVALID")
			}
			name = strings.TrimPrefix(name, outerRoot+"/")
		}
		if _, exists := seen[name]; exists || !wire.SafePath(name) || name != storeSnapshotRoot && !strings.HasPrefix(name, storeSnapshotRoot+"/") || h.Linkname != "" || !paxSafe || len(h.Xattrs) > 0 || len(seen) >= maximumEntries || h.Uid != uid || h.Gid != gid {
			return result, failure("OFFLINE_ENGINE_STORE_BACKUP_ENTRY_INVALID")
		}
		if name != storeSnapshotRoot && seen[path.Dir(name)] != tar.TypeDir {
			return result, failure("OFFLINE_ENGINE_STORE_BACKUP_PARENT_REQUIRED")
		}
		seen[name] = h.Typeflag
		if h.Typeflag == tar.TypeDir {
			if h.Mode != 0700 || h.Size != 0 {
				return result, failure("OFFLINE_ENGINE_STORE_BACKUP_DIRECTORY_INVALID")
			}
			if directories != nil {
				*directories = append(*directories, name)
			}
			continue
		}
		if name == storeSnapshotRoot || h.Typeflag != tar.TypeReg || h.Mode != 0600 && h.Mode != 0444 || h.Size < 0 || h.Size > maximumBytes || counter.bytes > maximumBytes-h.Size {
			return result, failure("OFFLINE_ENGINE_STORE_BACKUP_FILE_INVALID")
		}
		fileDigest := sha256.New()
		if n, e := io.CopyBuffer(fileDigest, reader, make([]byte, 64<<10)); e != nil || n != h.Size {
			return result, failure("OFFLINE_ENGINE_STORE_BACKUP_FILE_READ_FAILED")
		}
		result.Files = append(result.Files, BackupArchiveFile{Name: strings.TrimPrefix(name, storeSnapshotRoot+"/"), Bytes: h.Size, SHA256: hex.EncodeToString(fileDigest.Sum(nil))})
		if fileModes != nil {
			fileModes[strings.TrimPrefix(name, storeSnapshotRoot+"/")] = h.Mode
		}
	}
	manifestName := "snapshot.json"
	if role == "models" {
		manifestName = "model-files.json"
	}
	if seen[storeSnapshotRoot] != tar.TypeDir || seen[storeSnapshotRoot+"/"+manifestName] != tar.TypeReg {
		return result, failure("OFFLINE_ENGINE_STORE_BACKUP_MANIFEST_REQUIRED")
	}
	tail, e := io.ReadAll(io.LimitReader(counter, archiveReadbackReserveBytes+1))
	if e != nil || len(tail) > archiveReadbackReserveBytes || len(bytes.Trim(tail, "\x00")) != 0 || counter.bytes > maximumBytes {
		return result, failure("OFFLINE_ENGINE_STORE_BACKUP_TRAILER_OR_LIMIT")
	}
	result.Status = "verified_exited_store_snapshot_archive_bytes"
	result.Bytes = counter.bytes
	result.SHA256 = hex.EncodeToString(digest.Sum(nil))
	result.Qualification = "safe_opaque_existing_backend_export_only_not_durable_host_artifact_full_backup_restore_or_native_target"
	return result, nil
}
