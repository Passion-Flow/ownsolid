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
	"regexp"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const postgresBackupRootName = "postgresql"
const postgresDumpMagic = "PGDMP"
const maximumBackupArchiveBytes int64 = 1 << 40

var backupTenantIdentity = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type PostgresBackupSource struct {
	Database            string `json:"database"`
	Operator            string `json:"operator"`
	ServerVersionNumber int    `json:"server_version_number"`
	TenantID            string `json:"tenant_id"`
	SchemaVersion       int    `json:"schema_version"`
	VectorVersion       string `json:"vector_version"`
}

// Valid binds source metadata used by a new restore to the closed SQL client
// profile. Reading an inventory never grants an arbitrary connection override.
func (s PostgresBackupSource) Valid() bool {
	return backupConnectionPart(s.Database) && !strings.Contains(s.Database, "=") && !strings.Contains(s.Database, "://") && backupConnectionPart(s.Operator) && s.ServerVersionNumber >= 100000 && s.SchemaVersion >= 1 && backupTenantIdentity.MatchString(s.TenantID) && s.TenantID != "00000000-0000-0000-0000-000000000000" && len(s.VectorVersion) > 0 && len(s.VectorVersion) <= 100 && !strings.ContainsAny(s.VectorVersion, "\x00\r\n")
}

type BackupArchiveFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type PostgresBackupArchive struct {
	Status        string               `json:"status"`
	Bytes         int64                `json:"bytes"`
	SHA256        string               `json:"sha256"`
	Files         []BackupArchiveFile  `json:"files"`
	Source        PostgresBackupSource `json:"source"`
	Qualification string               `json:"qualification"`
}
type backupArchiveCounter struct {
	reader io.Reader
	bytes  int64
}

func (r *backupArchiveCounter) Read(raw []byte) (int, error) {
	n, e := r.reader.Read(raw)
	r.bytes += int64(n)
	return n, e
}

// The caller owns a private exclusive sink and its file/parent fsync/readback.
// An error preserves its partial bytes; this API never publishes or retries it.
func (c *Client) StreamPostgresBackupArchive(ctx context.Context, t PostgresBackupTask, id string, maximumBytes int64, seconds int, sink io.Writer) (PostgresBackupArchive, error) {
	if t.Restore != nil || sink == nil || maximumBytes < archiveReadbackReserveBytes || maximumBytes > maximumBackupArchiveBytes || seconds < c.config.DialSeconds || seconds > c.config.LoadSeconds {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_BACKUP_STREAM_BOUND_INVALID")
	}
	before, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return PostgresBackupArchive{}, e
	}
	if before.State.Status != "exited" || before.State.Running || before.State.PID != 0 || before.State.ExitCode != 0 || before.State.OOMKilled {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_SUCCESSFUL_EXITED_BACKUP_TASK_REQUIRED")
	}
	if e = c.databaseTaskNetwork(ctx, t.networkTask(), id, true); e != nil {
		return PostgresBackupArchive{}, e
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	address := unixHTTPAuthority + "/v" + c.policy.APIVersion + "/containers/" + id + "/archive?" + (url.Values{"path": {PostgresBackupOutputDestination}}).Encode()
	request, e := http.NewRequestWithContext(bounded, http.MethodGet, address, nil)
	if e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_BACKUP_STREAM_REQUEST_INVALID")
	}
	response, e := c.http.Do(request)
	if e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_BACKUP_STREAM_TRANSPORT_OR_DEADLINE")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maximumBytes {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_BACKUP_STREAM_RESPONSE_INVALID")
	}
	result, e := readPostgresBackupArchive(response.Body, sink, t, maximumBytes, c.config.ResponseBytes)
	if e != nil {
		return result, e
	}
	after, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil || after.State != before.State {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_BACKUP_TASK_CHANGED_DURING_READBACK")
	}
	if e = c.databaseTaskNetwork(ctx, t.networkTask(), id, true); e != nil {
		return PostgresBackupArchive{}, e
	}
	return result, nil
}

func readPostgresBackupArchive(source io.Reader, sink io.Writer, t PostgresBackupTask, maximumBytes, maximumMetadata int64) (PostgresBackupArchive, error) {
	return readPostgresArchiveWithRoot(source, sink, t, maximumBytes, maximumMetadata, 0, 0)
}

func readPostgresArchiveWithRoot(source io.Reader, sink io.Writer, t PostgresBackupTask, maximumBytes, maximumMetadata int64, rootUID, rootGID int) (PostgresBackupArchive, error) {
	result := PostgresBackupArchive{}
	if source == nil || sink == nil || maximumBytes < archiveReadbackReserveBytes || maximumBytes > maximumBackupArchiveBytes || maximumMetadata < 1024 || maximumMetadata > 16777216 || !t.valid() {
		return result, failure("OFFLINE_ENGINE_BACKUP_ARCHIVE_CONTEXT_INVALID")
	}
	digest := sha256.New()
	// Tee every input byte, including tar padding, without retaining the dump.
	counter := &backupArchiveCounter{reader: io.TeeReader(io.LimitReader(source, maximumBytes+1), io.MultiWriter(sink, digest))}
	archive := tar.NewReader(counter)
	seen := map[string]bool{}
	metadata := map[string][]byte{}
	wanted := map[string]bool{"database.dump": true, "contents.list": true, "source.json": true, "tools.txt": true}
	remainingMetadata := maximumMetadata
	for {
		beforeHeader := counter.bytes
		header, e := archive.Next()
		if e == io.EOF {
			if counter.bytes-beforeHeader < tarEndBytes {
				return result, failure("OFFLINE_ENGINE_BACKUP_TAR_END_REQUIRED")
			}
			break
		}
		if e != nil {
			return result, failure("OFFLINE_ENGINE_BACKUP_TAR_INVALID")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] || !wire.SafePath(name) || header.Linkname != "" || len(header.PAXRecords) > 0 || len(header.Xattrs) > 0 || len(seen) >= 6 {
			return result, failure("OFFLINE_ENGINE_BACKUP_ARCHIVE_ENTRY_INVALID")
		}
		seen[name] = true
		if name == postgresBackupRootName || name == postgresBackupRootName+"/private" {
			uid, gid, mode := rootUID, rootGID, int64(0755)
			if strings.HasSuffix(name, "/private") {
				uid, gid, mode = t.UID, t.GID, 0700
			}
			if header.Typeflag != tar.TypeDir || header.Mode != mode || header.Uid != uid || header.Gid != gid || header.Size != 0 {
				return result, failure("OFFLINE_ENGINE_BACKUP_ARCHIVE_DIRECTORY_INVALID")
			}
			continue
		}
		prefix := postgresBackupRootName + "/private/"
		file := strings.TrimPrefix(name, prefix)
		if !strings.HasPrefix(name, prefix) || !wanted[file] || header.Typeflag != tar.TypeReg || header.Mode != 0600 || header.Uid != t.UID || header.Gid != t.GID || header.Size < 1 || header.Size > maximumBytes || counter.bytes > maximumBytes-header.Size {
			return result, failure("OFFLINE_ENGINE_BACKUP_ARCHIVE_FILE_INVALID")
		}
		fileDigest := sha256.New()
		if file == "database.dump" {
			if header.Size < int64(len(postgresDumpMagic)) {
				return result, failure("OFFLINE_ENGINE_BACKUP_CUSTOM_DUMP_REQUIRED")
			}
			magic := make([]byte, len(postgresDumpMagic))
			if _, e = io.ReadFull(io.TeeReader(archive, fileDigest), magic); e != nil || string(magic) != postgresDumpMagic {
				return result, failure("OFFLINE_ENGINE_BACKUP_CUSTOM_DUMP_REQUIRED")
			}
			if n, e := io.CopyBuffer(fileDigest, archive, make([]byte, 64<<10)); e != nil || n != header.Size-int64(len(magic)) {
				return result, failure("OFFLINE_ENGINE_BACKUP_DUMP_READ_FAILED")
			}
		} else {
			if header.Size > remainingMetadata {
				return result, failure("OFFLINE_ENGINE_BACKUP_METADATA_LIMIT")
			}
			value, e := io.ReadAll(io.TeeReader(archive, fileDigest))
			if e != nil || int64(len(value)) != header.Size {
				return result, failure("OFFLINE_ENGINE_BACKUP_METADATA_READ_FAILED")
			}
			remainingMetadata -= int64(len(value))
			metadata[file] = value
		}
		result.Files = append(result.Files, BackupArchiveFile{Name: file, Bytes: header.Size, SHA256: hex.EncodeToString(fileDigest.Sum(nil))})
	}
	if len(seen) != 6 || !seen[postgresBackupRootName] || !seen[postgresBackupRootName+"/private"] || len(result.Files) != 4 {
		return result, failure("OFFLINE_ENGINE_BACKUP_ARCHIVE_INCOMPLETE")
	}
	for file := range wanted {
		if !seen[postgresBackupRootName+"/private/"+file] {
			return result, failure("OFFLINE_ENGINE_BACKUP_ARCHIVE_INCOMPLETE")
		}
	}
	tail, e := io.ReadAll(io.LimitReader(counter, archiveReadbackReserveBytes+1))
	if e != nil || len(tail) > archiveReadbackReserveBytes || len(bytes.Trim(tail, "\x00")) != 0 || counter.bytes > maximumBytes {
		return result, failure("OFFLINE_ENGINE_BACKUP_ARCHIVE_TRAILER_OR_LIMIT")
	}
	if wire.StrictJSON(metadata["source.json"], &result.Source) != nil || result.Source.Database != t.Database || result.Source.Operator != t.User || result.Source.ServerVersionNumber < 1 || result.Source.SchemaVersion < 1 || !backupTenantIdentity.MatchString(result.Source.TenantID) || result.Source.TenantID == "00000000-0000-0000-0000-000000000000" || result.Source.VectorVersion == "" {
		return result, failure("OFFLINE_ENGINE_BACKUP_SOURCE_IDENTITY_INVALID")
	}
	version := postgresToolVersion(result.Source.ServerVersionNumber)
	tools := strings.Split(strings.TrimSpace(string(metadata["tools.txt"])), "\n")
	toolMatches := func(line, tool string) bool {
		prefix := tool + " (PostgreSQL) " + version
		return line == prefix || strings.HasPrefix(line, prefix+" (")
	}
	if len(tools) != 2 || !toolMatches(tools[0], "pg_dump") || !toolMatches(tools[1], "pg_restore") {
		return result, failure("OFFLINE_ENGINE_BACKUP_TOOL_VERSION_MISMATCH")
	}
	if !bytes.Contains(metadata["contents.list"], []byte("; Archive created at")) || !bytes.Contains(metadata["contents.list"], []byte(";     dbname: "+t.Database)) {
		return result, failure("OFFLINE_ENGINE_BACKUP_TOC_INVALID")
	}
	result.Status = "verified_exited_postgres_custom_archive_bytes"
	result.Bytes = counter.bytes
	result.SHA256 = hex.EncodeToString(digest.Sum(nil))
	result.Qualification = "logical_dump_bytes_only_not_durable_host_artifact_full_platform_backup_restore_or_native_target"
	return result, nil
}

func postgresToolVersion(number int) string {
	// PostgreSQL10+ encodes major*10000 + minor; this profile is18.x.
	return strconv.Itoa(number/10000) + "." + strconv.Itoa(number%10000)
}
