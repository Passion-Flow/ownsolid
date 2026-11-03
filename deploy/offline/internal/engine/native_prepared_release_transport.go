package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type preparedReleaseConnection struct {
	client *Client
	close  func()
	proof  func() string
}

// Observe returns only an independently proven real host-local named-volume
// backing. Scope may compare original EngineSHA256/EndpointSHA256 to its journal.
func (c *Client) ObservePreparedReleaseStorage(ctx context.Context, o Ownership) (NativePostgreSQLStorageObservation, error) {
	if c == nil || ctx == nil {
		return NativePostgreSQLStorageObservation{}, failure("OFFLINE_ENGINE_PREPARED_CONTEXT_INVALID")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.config.RequestSeconds)*time.Second)
	defer cancel()
	channel, e := c.preparedReleaseChannel(bounded)
	if e != nil {
		return NativePostgreSQLStorageObservation{}, e
	}
	defer channel.close()
	return preparedReleaseStorage(bounded, channel, o)
}
func preparedReleaseSameTarget(a, b NativePostgreSQLStorageObservation) bool {
	return a.EngineSHA256 == b.EngineSHA256 && a.EndpointSHA256 == b.EndpointSHA256 && a.PeerProof == b.PeerProof && nativePreparedSameFilesystem(a.Filesystem, b.Filesystem) && len(a.SourceOwners) == 1 && len(b.SourceOwners) == 1 && a.SourceOwners[0].Owner == b.SourceOwners[0].Owner && a.SourceOwners[0].Mountpoint == b.SourceOwners[0].Mountpoint && nativePreparedSameFilesystem(a.SourceOwners[0].Filesystem, b.SourceOwners[0].Filesystem)
}
func nativePreparedSameFilesystem(a, b NativePostgreSQLStorageFilesystem) bool {
	return a.BackingRole == b.BackingRole && a.FilesystemType == b.FilesystemType && a.Device == b.Device && a.Inode == b.Inode && a.TotalBytes == b.TotalBytes && a.AllocationUnitBytes == b.AllocationUnitBytes
}
func preparedReleaseInput(o Ownership, image string, uid, gid int, p PreparedReleaseProfile) bool {
	return o.Role == RegistryPreparedReleaseRole && utilityIdentity(o, image, uid, gid) && preparedReleaseProfileValid(p)
}
func (c *Client) InspectPreparedRelease(ctx context.Context, o Ownership, image string, uid, gid int, p PreparedReleaseProfile) (ConfigurationObservation, error) {
	if c == nil || ctx == nil || !preparedReleaseInput(o, image, uid, gid, p) {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_PREPARED_INPUT_INVALID")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.config.LoadSeconds)*time.Second)
	defer cancel()
	channel, e := c.preparedReleaseChannel(bounded)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	defer channel.close()
	return inspectPreparedRelease(bounded, channel, o, image, uid, gid, p)
}
func inspectPreparedRelease(ctx context.Context, channel preparedReleaseConnection, o Ownership, image string, uid, gid int, p PreparedReleaseProfile) (ConfigurationObservation, error) {
	return inspectPreparedReleaseObserved(ctx, channel, o, image, uid, gid, p, preparedReleaseStorage)
}

type preparedReleaseObserver func(context.Context, preparedReleaseConnection, Ownership) (NativePostgreSQLStorageObservation, error)

func inspectPreparedReleaseObserved(ctx context.Context, channel preparedReleaseConnection, o Ownership, image string, uid, gid int, p PreparedReleaseProfile, observe preparedReleaseObserver) (ConfigurationObservation, error) {
	before, e := observe(ctx, channel, o)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	utility, e := channel.client.configurationUtility(ctx, o, image, uid, gid)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	result, e := readPreparedReleaseVolume(ctx, channel.client, utility.ID, uid, gid, p)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	afterUtility, e := channel.client.configurationUtility(ctx, o, image, uid, gid)
	if e != nil || !reflect.DeepEqual(utility, afterUtility) {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_PREPARED_UTILITY_CHANGED")
	}
	after, e := observe(ctx, channel, o)
	if e != nil || !preparedReleaseSameTarget(before, after) {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_PREPARED_TARGET_CHANGED")
	}
	return result, nil
}

// Put is a single stream into an entirely empty owned volume. Existing or
// uncertain contents are preserved; later reconciliation uses Inspect only.
func (c *Client) PutPreparedRelease(ctx context.Context, o Ownership, image string, uid, gid int, r PreparedReleaseRequest, p PreparedReleaseProfile) error {
	if c == nil || ctx == nil || !preparedReleaseInput(o, image, uid, gid, p) {
		return failure("OFFLINE_ENGINE_PREPARED_INPUT_INVALID")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.config.LoadSeconds)*time.Second)
	defer cancel()
	actual, snapshot, e := describePreparedRelease(bounded, r)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(actual, p) {
		return failure("OFFLINE_ENGINE_PREPARED_DESCRIPTOR_CHANGED")
	}
	channel, e := c.preparedReleaseChannel(bounded)
	if e != nil {
		return e
	}
	defer channel.close()
	return putPreparedRelease(bounded, channel, o, image, uid, gid, r, p, snapshot)
}
func putPreparedRelease(ctx context.Context, channel preparedReleaseConnection, o Ownership, image string, uid, gid int, r PreparedReleaseRequest, p PreparedReleaseProfile, snapshot preparedReleaseSnapshot) error {
	return putPreparedReleaseObserved(ctx, channel, o, image, uid, gid, r, p, snapshot, preparedReleaseStorage, preparedReleaseCapacityNow)
}

// Dependencies are lexical inputs for protocol verification. Public entrypoints
// always bind the real native observer/capacity reader, never a mutable hook.
func putPreparedReleaseObserved(ctx context.Context, channel preparedReleaseConnection, o Ownership, image string, uid, gid int, r PreparedReleaseRequest, p PreparedReleaseProfile, snapshot preparedReleaseSnapshot, observe preparedReleaseObserver, capacity func(NativePostgreSQLStorageOwner, int64, int64) error) error {
	observed, e := inspectPreparedReleaseObserved(ctx, channel, o, image, uid, gid, p, observe)
	if e != nil {
		return e
	}
	if observed.Status != "observed_empty_owned_volume" {
		return failure("OFFLINE_ENGINE_PREPARED_NONEMPTY_VOLUME_PRESERVED")
	}
	storage, e := observe(ctx, channel, o)
	if e != nil {
		return e
	}
	if e = preparedReleaseAllocation(p, storage.SourceOwners[0].Filesystem, r.Verification.MinimumFreeBytes); e != nil {
		return e
	}
	utility, e := channel.client.configurationUtility(ctx, o, image, uid, gid)
	if e != nil {
		return e
	}
	fs := storage.SourceOwners[0].Filesystem
	floor := fs.TotalBytes/10*3 + (fs.TotalBytes%10*3+9)/10
	reserve := max(floor, r.Verification.MinimumFreeBytes)
	input, output := io.Pipe()
	done := make(chan error, 1)
	go func() {
		e := writePreparedReleaseArchive(ctx, output, r, p, snapshot, uid, gid, func(chunk int64) error { return capacity(storage.SourceOwners[0], reserve, chunk) })
		output.CloseWithError(e)
		done <- e
	}()
	_, status, copyError := channel.client.request(ctx, http.MethodPut, "/v"+channel.client.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination}, "copyUIDGID": {"true"}, "noOverwriteDirNonDir": {"true"}}, input, channel.client.config.LoadSeconds)
	input.Close()
	writeError := <-done
	if copyError != nil || status != http.StatusOK || writeError != nil || !snapshot.unchanged() {
		return failure("OFFLINE_ENGINE_PREPARED_COPY_UNCERTAIN")
	}
	verified, e := inspectPreparedReleaseObserved(ctx, channel, o, image, uid, gid, p, observe)
	if e != nil {
		return e
	}
	if verified.Status != ConfigurationPayloadVerified {
		return failure("OFFLINE_ENGINE_PREPARED_COPY_UNCERTAIN")
	}
	final, e := observe(ctx, channel, o)
	if e != nil || !preparedReleaseSameTarget(storage, final) {
		return failure("OFFLINE_ENGINE_PREPARED_TARGET_CHANGED")
	}
	return nil
}

type preparedReleaseCapacityWriter struct {
	context context.Context
	writer  io.Writer
	check   func(int64) error
}

func (w preparedReleaseCapacityWriter) Write(raw []byte) (int, error) {
	if e := w.context.Err(); e != nil {
		return 0, e
	}
	if w.check != nil {
		if e := w.check(int64(len(raw))); e != nil {
			return 0, e
		}
	}
	return w.writer.Write(raw)
}
func writePreparedReleaseArchive(ctx context.Context, sink io.Writer, r PreparedReleaseRequest, p PreparedReleaseProfile, snapshot preparedReleaseSnapshot, uid, gid int, capacity func(int64) error) error {
	if !snapshot.unchanged() {
		return failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
	}
	writer := tar.NewWriter(preparedReleaseCapacityWriter{ctx, sink, capacity})
	for _, entry := range p.Entries {
		if e := ctx.Err(); e != nil {
			return e
		}
		header := &tar.Header{Name: entry.Name, Uid: uid, Gid: gid, Mode: 0600, Typeflag: tar.TypeReg, Size: entry.Bytes, Format: tar.FormatPAX, ModTime: time.Unix(0, 0)}
		if entry.Type == "directory" {
			header.Typeflag = tar.TypeDir
			header.Mode = 0700
		}
		if e := writer.WriteHeader(header); e != nil {
			return e
		}
		if entry.Type == "directory" {
			continue
		}
		source := r.Root + ".journal.jsonl"
		if strings.HasPrefix(entry.Name, "private/") {
			source = filepath.Join(r.Root, filepath.FromSlash(strings.TrimPrefix(entry.Name, "private/")))
		}
		before := snapshot.paths[source]
		now, e := os.Lstat(source)
		if e != nil || !preparedReleaseSame(before, now) {
			return failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
		}
		file, e := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		opened, se := file.Stat()
		if se != nil || !preparedReleaseSame(before, opened) {
			file.Close()
			return failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
		}
		hash := sha256.New()
		n, re := io.CopyBuffer(io.MultiWriter(writer, hash), io.LimitReader(restoreContextReader{ctx, file}, entry.Bytes+1), make([]byte, 64<<10))
		after, ae := file.Stat()
		ce := file.Close()
		named, ne := os.Lstat(source)
		if re != nil || ae != nil || ce != nil || ne != nil || n != entry.Bytes || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 || !preparedReleaseSame(before, after) || !preparedReleaseSame(before, named) {
			return failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
		}
	}
	if !snapshot.unchanged() {
		return failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
	}
	return writer.Close()
}
func readPreparedReleaseVolume(ctx context.Context, c *Client, id string, uid, gid int, p PreparedReleaseProfile) (out ConfigurationObservation, err error) {
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, unixHTTPAuthority+"/v"+c.policy.APIVersion+"/containers/"+id+"/archive?"+(url.Values{"path": {UtilityDestination}}).Encode(), nil)
	if e != nil {
		return out, e
	}
	response, e := c.http.Do(request)
	if e != nil {
		return out, failure("OFFLINE_ENGINE_PREPARED_READBACK_TRANSPORT")
	}
	defer func() {
		if e := response.Body.Close(); e != nil && err == nil {
			out = ConfigurationObservation{}
			err = failure("OFFLINE_ENGINE_PREPARED_READBACK_CLOSE_FAILED")
		}
	}()
	maximum := preparedReleaseMaximumTar(p)
	if response.StatusCode != http.StatusOK || response.ContentLength > maximum {
		return out, failure("OFFLINE_ENGINE_PREPARED_READBACK_RESPONSE_INVALID")
	}
	return readPreparedReleaseArchive(restoreContextReader{ctx, response.Body}, uid, gid, p, maximum)
}
func readPreparedReleaseArchive(input io.Reader, uid, gid int, p PreparedReleaseProfile, maximum int64) (ConfigurationObservation, error) {
	bad := failure("OFFLINE_ENGINE_PREPARED_READBACK_INVENTORY_INVALID")
	if input == nil || !preparedReleaseProfileValid(p) || uid < 1 || gid < 1 || uid > math.MaxInt32 || gid > math.MaxInt32 || maximum < tarEndBytes {
		return ConfigurationObservation{}, bad
	}
	expected := map[string]PreparedReleaseEntry{}
	for _, entry := range p.Entries {
		expected[entry.Name] = entry
	}
	source := &backupArchiveCounter{reader: io.LimitReader(input, maximum+1)}
	reader := tar.NewReader(source)
	seen := map[string]bool{}
	wrapper := path.Base(UtilityDestination)
	for {
		before := source.bytes
		header, e := reader.Next()
		if e == io.EOF {
			if source.bytes-before < tarEndBytes {
				return ConfigurationObservation{}, bad
			}
			break
		}
		if e != nil {
			return ConfigurationObservation{}, bad
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] || header.Linkname != "" || len(header.Xattrs) > 0 || !nativeArchivePath(name) {
			return ConfigurationObservation{}, bad
		}
		seen[name] = true
		for key, value := range header.PAXRecords {
			if key == "path" && value == header.Name || key == "size" && value == strconv.FormatInt(header.Size, 10) {
				continue
			}
			return ConfigurationObservation{}, bad
		}
		if name == wrapper {
			if header.Typeflag != tar.TypeDir || header.Mode != 0755 || header.Uid != 0 || header.Gid != 0 || header.Size != 0 {
				return ConfigurationObservation{}, bad
			}
			continue
		}
		if !seen[wrapper] || !strings.HasPrefix(name, wrapper+"/") {
			return ConfigurationObservation{}, bad
		}
		entry, ok := expected[strings.TrimPrefix(name, wrapper+"/")]
		if !ok || header.Uid != uid || header.Gid != gid || header.Size != entry.Bytes {
			return ConfigurationObservation{}, bad
		}
		if entry.Type == "directory" {
			if header.Typeflag != tar.TypeDir || header.Mode != 0700 {
				return ConfigurationObservation{}, bad
			}
		} else {
			if header.Typeflag != tar.TypeReg || header.Mode != 0600 || strings.HasSuffix(header.Name, "/") {
				return ConfigurationObservation{}, bad
			}
			hash := sha256.New()
			n, e := io.CopyBuffer(hash, reader, make([]byte, 64<<10))
			if e != nil || n != entry.Bytes || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
				return ConfigurationObservation{}, bad
			}
		}
	}
	tail, e := io.ReadAll(io.LimitReader(source, archiveReadbackReserveBytes+1))
	if e != nil || len(tail) > archiveReadbackReserveBytes || len(bytes.Trim(tail, "\x00")) != 0 || source.bytes > maximum || !seen[wrapper] {
		return ConfigurationObservation{}, bad
	}
	if len(seen) == 1 {
		return ConfigurationObservation{Status: "observed_empty_owned_volume", Qualification: "observed_entire_empty_owned_registry_release_volume_only"}, nil
	}
	if len(seen) != len(p.Entries)+1 {
		return ConfigurationObservation{}, bad
	}
	return ConfigurationObservation{Status: ConfigurationPayloadVerified, Files: p.Files, Bytes: p.Bytes, Qualification: fmt.Sprintf("verified_complete_registry_release_projection_%d_entries_only_not_application_or_continuous_source_custody", len(p.Entries))}, nil
}
