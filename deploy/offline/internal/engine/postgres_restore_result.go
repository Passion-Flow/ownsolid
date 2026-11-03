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
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type PostgresRestoreBefore struct {
	Database            string `json:"database"`
	Operator            string `json:"operator"`
	ServerVersionNumber int    `json:"server_version_number"`
	Empty               bool   `json:"empty"`
}
type PostgresRestoreObservation struct {
	Status        string                `json:"status"`
	Before        PostgresRestoreBefore `json:"before"`
	Source        PostgresBackupSource  `json:"source"`
	Files         []BackupArchiveFile   `json:"files"`
	ArchiveSHA256 string                `json:"archive_sha256"`
	Qualification string                `json:"qualification"`
}

func (c *Client) InspectPostgresRestoreResult(ctx context.Context, t PostgresBackupTask, id string) (PostgresRestoreObservation, error) {
	if !t.valid() || t.Restore == nil {
		return PostgresRestoreObservation{}, failure("OFFLINE_ENGINE_RESTORE_PROFILE_REQUIRED")
	}
	before, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return PostgresRestoreObservation{}, e
	}
	if before.State.Status != "exited" || before.State.ExitCode != 0 || before.State.OOMKilled {
		return PostgresRestoreObservation{}, failure("OFFLINE_ENGINE_SUCCESSFUL_RESTORE_TASK_REQUIRED")
	}
	if e = c.databaseTaskNetwork(ctx, t.networkTask(), id, true); e != nil {
		return PostgresRestoreObservation{}, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {PostgresRestoreOutputDestination}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return PostgresRestoreObservation{}, failure("OFFLINE_ENGINE_RESTORE_RESULT_READBACK_FAILED")
	}
	result, e := postgresRestoreResultArchive(raw, t)
	if e != nil {
		return result, e
	}
	after, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil || after.State != before.State {
		return PostgresRestoreObservation{}, failure("OFFLINE_ENGINE_RESTORE_TASK_CHANGED_DURING_READBACK")
	}
	if e = c.databaseTaskNetwork(ctx, t.networkTask(), id, true); e != nil {
		return PostgresRestoreObservation{}, e
	}
	return result, nil
}

func postgresRestoreResultArchive(raw []byte, t PostgresBackupTask) (PostgresRestoreObservation, error) {
	result := PostgresRestoreObservation{Files: []BackupArchiveFile{}}
	if !t.valid() || t.Restore == nil || len(raw) < tarEndBytes || len(raw) > 16777216 {
		return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_CONTEXT_INVALID")
	}
	count := &backupArchiveCounter{reader: bytes.NewReader(raw)}
	reader := tar.NewReader(count)
	seen := map[string]bool{}
	metadata := map[string][]byte{}
	rootName := "postgres-restore"
	for {
		before := count.bytes
		h, e := reader.Next()
		if e == io.EOF {
			if count.bytes-before < tarEndBytes {
				return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_END_REQUIRED")
			}
			break
		}
		if e != nil {
			return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_ARCHIVE_INVALID")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if seen[name] || !wire.SafePath(name) || len(seen) >= 5 || h.Linkname != "" || len(h.PAXRecords) > 0 || len(h.Xattrs) > 0 {
			return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_ENTRY_INVALID")
		}
		seen[name] = true
		if name == rootName || name == rootName+"/private" {
			uid, gid, mode := 0, 0, int64(0755)
			if name != rootName {
				uid, gid, mode = t.UID, t.GID, 0700
			}
			if h.Typeflag != tar.TypeDir || h.Uid != uid || h.Gid != gid || h.Mode != mode || h.Size != 0 {
				return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_DIRECTORY_INVALID")
			}
			continue
		}
		prefix := rootName + "/private/"
		file := strings.TrimPrefix(name, prefix)
		if !strings.HasPrefix(name, prefix) || file != "before.json" && file != "source.json" && file != "tools.txt" || h.Typeflag != tar.TypeReg || h.Mode != 0600 || h.Uid != t.UID || h.Gid != t.GID || h.Size < 1 || h.Size > int64(len(raw)) {
			return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_FILE_INVALID")
		}
		body, e := io.ReadAll(reader)
		if e != nil || int64(len(body)) != h.Size {
			return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_FILE_READ_FAILED")
		}
		metadata[file] = body
		hash := sha256.Sum256(body)
		result.Files = append(result.Files, BackupArchiveFile{Name: file, Bytes: int64(len(body)), SHA256: hex.EncodeToString(hash[:])})
	}
	if len(seen) != 5 || !seen[rootName] || !seen[rootName+"/private"] || len(metadata) != 3 {
		return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_INCOMPLETE")
	}
	tail, e := io.ReadAll(count)
	if e != nil || len(tail) > archiveReadbackReserveBytes || len(bytes.Trim(tail, "\x00")) != 0 {
		return result, failure("OFFLINE_ENGINE_RESTORE_RESULT_TRAILER_INVALID")
	}
	if wire.StrictJSON(metadata["before.json"], &result.Before) != nil || !result.Before.Empty || result.Before.Database != t.Database || result.Before.Operator != t.User || result.Before.ServerVersionNumber != t.Restore.ExpectedSource.ServerVersionNumber {
		return result, failure("OFFLINE_ENGINE_RESTORE_EMPTY_TARGET_NOT_PROVEN")
	}
	if wire.StrictJSON(metadata["source.json"], &result.Source) != nil || result.Source.Database != t.Database || result.Source.Operator != t.User || result.Source.ServerVersionNumber != result.Before.ServerVersionNumber || result.Source.TenantID != t.Restore.ExpectedSource.TenantID || result.Source.SchemaVersion != t.Restore.ExpectedSource.SchemaVersion || result.Source.VectorVersion != t.Restore.ExpectedSource.VectorVersion {
		return result, failure("OFFLINE_ENGINE_RESTORE_SOURCE_IDENTITY_MISMATCH")
	}
	version := "pg_restore (PostgreSQL) " + postgresToolVersion(result.Source.ServerVersionNumber)
	tool := strings.TrimSpace(string(metadata["tools.txt"]))
	if tool != version && !strings.HasPrefix(tool, version+" (") {
		return result, failure("OFFLINE_ENGINE_RESTORE_TOOL_VERSION_MISMATCH")
	}
	hash := sha256.Sum256(raw)
	result.ArchiveSHA256 = hex.EncodeToString(hash[:])
	result.Status = "verified_exited_postgres_restore_process_and_source_metadata"
	result.Qualification = "SQL_restore_process_metadata_only_not_schema_access_auth_files_runtime_or_native_qualification"
	return result, nil
}
