package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"reflect"
	"time"
)

// StreamNativePostgreSQLBackupArchive streams the original complete native
// tree from one exact successful terminal backup task. The caller owns durable
// private host publication, fsync, source signature and original native Verify.
func (c *Client) StreamNativePostgreSQLBackupArchive(ctx context.Context, t DatabaseTask, id string, maximumBytes int64, maximumEntries, seconds int, sink io.Writer) (NativePostgreSQLArchive, error) {
	t = t.freezeNativeRecovery()
	if ctx == nil || !t.validNativePostgreSQLRecovery() || t.Kind != NativePostgreSQLBackup || sink == nil || maximumBytes < tarEndBytes || maximumBytes > maximumNativePostgreSQLArchiveBytes || maximumEntries < 6 || maximumEntries > maximumNativePostgreSQLArchiveEntries || seconds < c.config.DialSeconds || seconds > c.config.LoadSeconds {
		return NativePostgreSQLArchive{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_STREAM_INPUT_INVALID")
	}
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return NativePostgreSQLArchive{}, e
	}
	if before.State.Status != "exited" || before.State.Running || before.State.PID != 0 || before.State.ExitCode != 0 || before.State.OOMKilled {
		return NativePostgreSQLArchive{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_SUCCESSFUL_TASK_REQUIRED")
	}
	if e = c.databaseTaskNetwork(ctx, t, id, true); e != nil {
		return NativePostgreSQLArchive{}, e
	}
	r := t.NativePostgreSQLRecovery.BackupRequest
	a := NativePostgreSQLArchiveArtifact{RootName: path.Base(r.BackupRoot), UID: t.UID, GID: t.GID, MaximumEntries: maximumEntries, MaximumManifestBytes: r.Policy.MaximumManifestBytes}
	result, e := c.readNativePostgreSQLContainerArchive(ctx, id, r.BackupRoot, a, maximumBytes, seconds, sink)
	if e != nil {
		return result, e
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return NativePostgreSQLArchive{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_TASK_CHANGED")
	}
	if e = c.databaseTaskNetwork(ctx, t, id, true); e != nil {
		return NativePostgreSQLArchive{}, e
	}
	return result, nil
}

func (c *Client) readNativePostgreSQLContainerArchive(ctx context.Context, id, destination string, a NativePostgreSQLArchiveArtifact, maximumBytes int64, seconds int, sink io.Writer) (NativePostgreSQLArchive, error) {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	address := unixHTTPAuthority + "/v" + c.policy.APIVersion + "/containers/" + id + "/archive?" + (url.Values{"path": {destination}}).Encode()
	request, e := http.NewRequestWithContext(bounded, http.MethodGet, address, nil)
	if e != nil {
		return NativePostgreSQLArchive{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_READBACK_INPUT_INVALID")
	}
	response, e := c.http.Do(request)
	if e != nil {
		return NativePostgreSQLArchive{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_TRANSPORT_OR_DEADLINE")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maximumBytes {
		return NativePostgreSQLArchive{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_RESPONSE_INVALID")
	}
	return readNativePostgreSQLArchive(response.Body, sink, a, maximumBytes)
}

func (t DatabaseTask) nativeArchiveInput(p NativePostgreSQLArchiveProfile) (Ownership, bool) {
	if !p.valid() || p.Artifact.UID != t.UID || p.Artifact.GID != t.GID {
		return Ownership{}, false
	}
	var root string
	var owner Ownership
	var metadata int64
	if t.NativePostgreSQLBackupVerifier != nil && t.validNativePostgreSQLVerifier() && t.NativePostgreSQLBackupVerifier.BackupParent {
		q := t.NativePostgreSQLBackupVerifier
		root, owner, metadata = q.Request.BackupRoot, q.BackupOwner, q.Request.Policy.MaximumManifestBytes
	} else if t.NativePostgreSQLRecovery != nil && t.validNativePostgreSQLRecovery() && t.Kind != NativePostgreSQLBackup {
		q := t.NativePostgreSQLRecovery
		root, owner, metadata = q.RestoreRequest.BackupRoot, q.BackupOwner, q.RestoreRequest.Policy.MaximumManifestBytes
	} else {
		return Ownership{}, false
	}
	return owner, p.Artifact.RootName == path.Base(root) && p.Artifact.MaximumManifestBytes <= metadata
}

// PutNativePostgreSQLBackupArchive copies the exact protected original tar once
// into an empty private parent of an exact owned never-started initializer.
// The caller first creates the private empty parent through PutConfiguration
// with nil files and journals this copy before invoking it. Unknown copies are
// inspected only; existing content is never overwritten or deleted here.
func (c *Client) PutNativePostgreSQLBackupArchive(ctx context.Context, t DatabaseTask, initializer string, input *os.File, p NativePostgreSQLArchiveProfile) (NativePostgreSQLArchiveProfile, error) {
	t = t.freezeNativeVerifier().freezeNativeRecovery()
	owner, ok := t.nativeArchiveInput(p)
	if !ok {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_INPUT_PROFILE_INVALID")
	}
	described, e := DescribeNativePostgreSQLArchive(ctx, input, p.Artifact)
	if e != nil || described != p {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_DESCRIPTOR_CHANGED")
	}
	empty, e := c.InspectConfiguration(ctx, owner, initializer, t.UID, t.GID, nil)
	if e != nil || empty.Status != ConfigurationPayloadVerified {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_NONEMPTY_INPUT_PRESERVED")
	}
	utility, e := c.configurationUtility(ctx, owner, initializer, t.UID, t.GID)
	if e != nil {
		return NativePostgreSQLArchiveProfile{}, e
	}
	before, e := restorePrivateFile(input, p.Artifact.Bytes)
	if e != nil {
		return NativePostgreSQLArchiveProfile{}, e
	}
	if _, e = input.Seek(0, io.SeekStart); e != nil {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_SEEK_FAILED")
	}
	hash := sha256.New()
	count := &backupArchiveCounter{reader: io.TeeReader(io.LimitReader(restoreContextReader{ctx, input}, p.Artifact.Bytes+1), hash)}
	_, status, e := c.request(ctx, http.MethodPut, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination + "/" + UtilityPrivateDirectory}, "copyUIDGID": {"true"}, "noOverwriteDirNonDir": {"true"}}, count, c.config.LoadSeconds)
	after, se := restorePrivateFile(input, p.Artifact.Bytes)
	if e != nil || status != http.StatusOK || se != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || count.bytes != p.Artifact.Bytes || hex.EncodeToString(hash.Sum(nil)) != p.Artifact.SHA256 {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_COPY_UNCERTAIN")
	}
	return c.InspectNativePostgreSQLBackupArchiveInput(ctx, t, initializer, p)
}

// InspectNativePostgreSQLBackupArchiveInput reopens the exact never-started
// copied input using GETs only. Full name/type/mode/size/content inventory must
// match; daemon tar header/timestamp changes are not an original host tar hash.
func (c *Client) InspectNativePostgreSQLBackupArchiveInput(ctx context.Context, t DatabaseTask, initializer string, p NativePostgreSQLArchiveProfile) (NativePostgreSQLArchiveProfile, error) {
	t = t.freezeNativeVerifier().freezeNativeRecovery()
	owner, ok := t.nativeArchiveInput(p)
	if ctx == nil || !ok {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_INPUT_PROFILE_INVALID")
	}
	before, e := c.configurationUtility(ctx, owner, initializer, t.UID, t.GID)
	if e != nil {
		return NativePostgreSQLArchiveProfile{}, e
	}
	// PAX path/large size headers can differ in a recreated daemon tar. Entry
	// count bounds this padding overhead, while inventory binds every payload.
	maximum := p.Artifact.Bytes + int64(p.Entries)*3*tarBlockBytes + archiveReadbackReserveBytes
	if maximum > maximumNativePostgreSQLArchiveBytes {
		maximum = maximumNativePostgreSQLArchiveBytes
	}
	result, e := c.readNativePostgreSQLContainerArchive(ctx, before.ID, UtilityDestination+"/"+UtilityPrivateDirectory+"/"+p.Artifact.RootName, p.Artifact, maximum, c.config.LoadSeconds, io.Discard)
	if e != nil || result.Profile.Entries != p.Entries || result.Profile.EntriesSHA256 != p.EntriesSHA256 {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_COPIED_INVENTORY_CHANGED")
	}
	after, e := c.configurationUtility(ctx, owner, initializer, t.UID, t.GID)
	if e != nil || !reflect.DeepEqual(before, after) {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_INITIALIZER_CHANGED")
	}
	return p, nil
}
