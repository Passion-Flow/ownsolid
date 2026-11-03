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

func (c *Client) PutStoreRestoreArchive(ctx context.Context, t DatabaseTask, initializer string, input *os.File) (StoreRestoreProfile, error) {
	if !t.valid() || t.StoreRestore == nil {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_PROFILE_REQUIRED")
	}
	profile, e := DescribeStoreRestoreArchive(ctx, input, t.StoreRestore.Artifact)
	if e != nil || profile != *t.StoreRestore {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_DESCRIPTOR_CHANGED")
	}
	state, e := c.InspectConfiguration(ctx, *t.SourceOwner, initializer, t.UID, t.GID, nil)
	if e != nil || state.Status != "observed_empty_owned_volume" {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_NONEMPTY_STORE_RESTORE_INPUT_PRESERVED")
	}
	utility, e := c.configurationUtility(ctx, *t.SourceOwner, initializer, t.UID, t.GID)
	if e != nil {
		return StoreRestoreProfile{}, e
	}
	before, e := restorePrivateFile(input, profile.Artifact.Bytes)
	if e != nil {
		return StoreRestoreProfile{}, e
	}
	if _, e = input.Seek(0, io.SeekStart); e != nil {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_SEEK_FAILED")
	}
	hash := sha256.New()
	counter := &backupArchiveCounter{reader: io.TeeReader(io.LimitReader(restoreContextReader{ctx, input}, profile.Artifact.Bytes+1), hash)}
	_, status, e := c.request(ctx, http.MethodPut, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination}, "copyUIDGID": {"true"}, "noOverwriteDirNonDir": {"true"}}, counter, c.config.LoadSeconds)
	after, se := restorePrivateFile(input, profile.Artifact.Bytes)
	if e != nil || status != http.StatusOK || se != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || counter.bytes != profile.Artifact.Bytes || hex.EncodeToString(hash.Sum(nil)) != profile.Artifact.SHA256 {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_COPY_UNCERTAIN")
	}
	return c.InspectStoreRestoreArchiveInput(ctx, t, initializer)
}

func (c *Client) readStoreRestoreCopied(ctx context.Context, t DatabaseTask, id, destination string) (StoreRestoreProfile, error) {
	r := t.StoreRestore.Artifact
	maximum := r.Bytes + archiveReadbackReserveBytes
	if maximum > maximumBackupArchiveBytes {
		maximum = maximumBackupArchiveBytes
	}
	if maximum < archiveReadbackReserveBytes {
		maximum = archiveReadbackReserveBytes
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.config.LoadSeconds)*time.Second)
	defer cancel()
	address := unixHTTPAuthority + "/v" + c.policy.APIVersion + "/containers/" + id + "/archive?" + (url.Values{"path": {destination}}).Encode()
	request, e := http.NewRequestWithContext(bounded, http.MethodGet, address, nil)
	if e != nil {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_READBACK_REQUEST_INVALID")
	}
	response, e := c.http.Do(request)
	if e != nil {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_READBACK_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maximum {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_READBACK_RESPONSE_INVALID")
	}
	directories := []string{}
	modes := map[string]int64{}
	archive, e := readStoreSnapshotArchive(response.Body, io.Discard, r.Role, r.UID, r.GID, maximum, r.MaximumEntries, &directories, modes, path.Base(destination))
	if e != nil {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_COPIED_ARCHIVE_INVALID")
	}
	f, d := storeRestoreInventoryDigests(archive.Files, directories, modes)
	observed := StoreRestoreProfile{Artifact: r, FilesSHA256: f, DirectoriesSHA256: d}
	if observed != *t.StoreRestore {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_COPIED_INVENTORY_CHANGED")
	}
	return observed, nil
}

func (c *Client) InspectStoreRestoreArchiveInput(ctx context.Context, t DatabaseTask, initializer string) (StoreRestoreProfile, error) {
	if !t.valid() || t.StoreRestore == nil {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_PROFILE_REQUIRED")
	}
	before, e := c.configurationUtility(ctx, *t.SourceOwner, initializer, t.UID, t.GID)
	if e != nil {
		return StoreRestoreProfile{}, e
	}
	result, e := c.readStoreRestoreCopied(ctx, t, before.ID, UtilityDestination)
	if e != nil {
		return result, e
	}
	after, e := c.configurationUtility(ctx, *t.SourceOwner, initializer, t.UID, t.GID)
	if e != nil || !reflect.DeepEqual(after, before) {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_UTILITY_CHANGED")
	}
	return result, nil
}

func (c *Client) inspectStoreRestoreTaskSource(ctx context.Context, t DatabaseTask, id string) error {
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return e
	}
	if before.State.Running {
		return failure("OFFLINE_ENGINE_NONRUNNING_DATABASE_TASK_REQUIRED")
	}
	if _, e = c.readStoreRestoreCopied(ctx, t, id, StoreRestoreSourceDestination); e != nil {
		return e
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || after.State != before.State {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_CHANGED_DURING_READBACK")
	}
	return nil
}

func (c *Client) inspectEmptyStoreRestoreTarget(ctx context.Context, t DatabaseTask, id string) error {
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return e
	}
	if before.State.Status != "created" {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_EXECUTION_PRESERVED")
	}
	destination, _ := t.outputProfile()
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {destination}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return failure("OFFLINE_ENGINE_STORE_RESTORE_EMPTY_TARGET_READBACK_FAILED")
	}
	value, e := inspectPrivateArchive(raw, nil, t.UID, t.GID, path.Base(destination))
	if e != nil || value.Status != "verified_private_volume_payload" {
		return failure("OFFLINE_ENGINE_NONEMPTY_STORE_RESTORE_TARGET_PRESERVED")
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || after.State != before.State {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_CHANGED_DURING_READBACK")
	}
	return nil
}
