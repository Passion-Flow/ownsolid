package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

type restoreContextReader struct {
	context context.Context
	reader  io.Reader
}

func (r restoreContextReader) Read(raw []byte) (int, error) {
	if e := r.context.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(raw)
}

func restorePrivateFile(input *os.File, bytes int64) (os.FileInfo, error) {
	if input == nil || !filepath.IsAbs(input.Name()) {
		return nil, failure("OFFLINE_ENGINE_RESTORE_PRIVATE_ARCHIVE_REQUIRED")
	}
	resolved, e := filepath.EvalSymlinks(input.Name())
	if e != nil || resolved != input.Name() {
		return nil, failure("OFFLINE_ENGINE_RESTORE_PRIVATE_ARCHIVE_REQUIRED")
	}
	info, e := input.Stat()
	named, ne := os.Lstat(input.Name())
	if e != nil || ne != nil || !os.SameFile(info, named) || info.Mode() != 0600 || info.Size() != bytes {
		return nil, failure("OFFLINE_ENGINE_RESTORE_PRIVATE_ARCHIVE_REQUIRED")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() || st.Nlink != 1 {
		return nil, failure("OFFLINE_ENGINE_RESTORE_PRIVATE_ARCHIVE_REQUIRED")
	}
	return info, nil
}

func (c *Client) ValidatePostgresRestoreArchive(ctx context.Context, t PostgresBackupTask, input *os.File) (PostgresBackupArchive, error) {
	if !t.valid() || t.Restore == nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_PROFILE_REQUIRED")
	}
	before, e := restorePrivateFile(input, t.Restore.ArchiveBytes)
	if e != nil {
		return PostgresBackupArchive{}, e
	}
	if _, e = input.Seek(0, io.SeekStart); e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_SEEK_FAILED")
	}
	if ctx.Err() != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_INTERRUPTED")
	}
	result, e := readPostgresBackupArchive(restoreContextReader{ctx, input}, io.Discard, t.restoreSourceArchiveTask(t.Restore.SourceUID, t.Restore.SourceGID), t.Restore.ArchiveBytes, c.config.ResponseBytes)
	if e != nil || result.SHA256 != t.Restore.ArchiveSHA256 || result.Bytes != t.Restore.ArchiveBytes || !reflect.DeepEqual(result.Source, t.Restore.ExpectedSource) || !restoreFilesMatch(result.Files, t.Restore.ExpectedFiles) {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_BYTES_OR_METADATA_INVALID")
	}
	after, e := restorePrivateFile(input, t.Restore.ArchiveBytes)
	if e != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_CHANGED")
	}
	return result, nil
}

// The scope owns the durable input-generation intent and external-volume file.
// Preserve the verified archive unchanged; the task admits only its fixed
// readonly postgresql subdirectory, with the same approved numeric PG owner.
func (c *Client) PutPostgresRestoreArchive(ctx context.Context, t PostgresBackupTask, initializer string, input *os.File) (PostgresBackupArchive, error) {
	if _, e := c.ValidatePostgresRestoreArchive(ctx, t, input); e != nil {
		return PostgresBackupArchive{}, e
	}
	state, e := c.InspectConfiguration(ctx, t.Restore.SourceOwner, initializer, t.UID, t.GID, nil)
	if e != nil || state.Status != "observed_empty_owned_volume" {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_NONEMPTY_RESTORE_INPUT_PRESERVED")
	}
	utility, e := c.configurationUtility(ctx, t.Restore.SourceOwner, initializer, t.UID, t.GID)
	if e != nil {
		return PostgresBackupArchive{}, e
	}
	before, e := restorePrivateFile(input, t.Restore.ArchiveBytes)
	if e != nil {
		return PostgresBackupArchive{}, e
	}
	if _, e = input.Seek(0, io.SeekStart); e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_SEEK_FAILED")
	}
	hash := sha256.New()
	counter := &backupArchiveCounter{reader: io.TeeReader(io.LimitReader(restoreContextReader{ctx, input}, t.Restore.ArchiveBytes+1), hash)}
	_, status, e := c.request(ctx, http.MethodPut, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination}, "copyUIDGID": {"true"}, "noOverwriteDirNonDir": {"true"}}, counter, c.config.LoadSeconds)
	after, se := restorePrivateFile(input, t.Restore.ArchiveBytes)
	if e != nil || status != http.StatusOK || se != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || counter.bytes != t.Restore.ArchiveBytes || hex.EncodeToString(hash.Sum(nil)) != t.Restore.ArchiveSHA256 {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_COPY_UNCERTAIN")
	}
	return c.InspectPostgresRestoreArchiveInput(ctx, t, initializer)
}

func (c *Client) InspectPostgresRestoreArchiveInput(ctx context.Context, t PostgresBackupTask, initializer string) (PostgresBackupArchive, error) {
	if !t.valid() || t.Restore == nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_PROFILE_REQUIRED")
	}
	before, e := c.configurationUtility(ctx, t.Restore.SourceOwner, initializer, t.UID, t.GID)
	if e != nil {
		return PostgresBackupArchive{}, e
	}
	if e = c.noForeignConsumers(ctx, t.Restore.SourceOwner, before.ID); e != nil {
		return PostgresBackupArchive{}, e
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.config.LoadSeconds)*time.Second)
	defer cancel()
	address := unixHTTPAuthority + "/v" + c.policy.APIVersion + "/containers/" + before.ID + "/archive?" + (url.Values{"path": {UtilityDestination + "/" + postgresBackupRootName}}).Encode()
	req, e := http.NewRequestWithContext(bounded, http.MethodGet, address, nil)
	if e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_INPUT_REQUEST_INVALID")
	}
	response, e := c.http.Do(req)
	if e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_INPUT_READBACK_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_INPUT_RESPONSE_INVALID")
	}
	maximum := t.Restore.ArchiveBytes + archiveReadbackReserveBytes
	if maximum > maximumBackupArchiveBytes {
		maximum = maximumBackupArchiveBytes
	}
	result, e := readPostgresArchiveWithRoot(response.Body, io.Discard, t.restoreSourceArchiveTask(t.UID, t.GID), maximum, c.config.ResponseBytes, t.UID, t.GID)
	if e != nil {
		return PostgresBackupArchive{}, e
	}
	if !reflect.DeepEqual(result.Source, t.Restore.ExpectedSource) || !restoreFilesMatch(result.Files, t.Restore.ExpectedFiles) {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_INPUT_BYTES_OR_METADATA_CHANGED")
	}
	after, e := c.configurationUtility(ctx, t.Restore.SourceOwner, initializer, t.UID, t.GID)
	if e != nil || before.ID != after.ID || before.State != after.State {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_UTILITY_CHANGED_DURING_READBACK")
	}
	return result, nil
}
