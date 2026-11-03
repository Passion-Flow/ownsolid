package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const NativePostgreSQLStorageObserved = "observed_direct_local_native_postgresql_storage"
const NativePostgreSQLDurableBacking = "native_direct_block"

type NativePostgreSQLStorageFilesystem struct {
	BackingRole         string `json:"backing_role"`
	FilesystemType      int64  `json:"filesystem_type"`
	Device              uint64 `json:"device"`
	Inode               uint64 `json:"inode"`
	TotalBytes          int64  `json:"total_bytes"`
	AvailableBytes      int64  `json:"available_bytes"`
	AllocationUnitBytes int64  `json:"allocation_unit_bytes"`
}

// Durable artifact/journal roots use the same proven native backing boundary;
// a RAM filesystem or an unverified loop/VM backing cannot become durable storage.
func ObserveNativePostgreSQLDurableDirectory(path string) (NativePostgreSQLStorageFilesystem, error) {
	return nativePostgreSQLDurableDirectory(path)
}

type NativePostgreSQLStorageOwner struct {
	Owner      Ownership                         `json:"owner"`
	Filesystem NativePostgreSQLStorageFilesystem `json:"filesystem"`
	Mountpoint string                            `json:"-"`
}

type NativePostgreSQLStorageObservation struct {
	PeerProof      string                            `json:"peer_proof"`
	Status         string                            `json:"status"`
	EngineSHA256   string                            `json:"engine_sha256"`
	EndpointSHA256 string                            `json:"endpoint_sha256"`
	Filesystem     NativePostgreSQLStorageFilesystem `json:"filesystem"`
	SourceOwners   []NativePostgreSQLStorageOwner    `json:"source_owners"`
	Qualification  string                            `json:"qualification"`
	AllocationRoot string                            `json:"-"`
}

type nativePostgreSQLStorageInfo struct {
	ID              string
	DockerRootDir   string
	OSType          string
	SecurityOptions []string
}

func (c *Client) ObserveNativePostgreSQLStorage(ctx context.Context, t DatabaseTask) (NativePostgreSQLStorageObservation, error) {
	if c == nil || ctx == nil || ctx.Err() != nil || c.config.Profile != c.policy.NativeProfile || t.Kind != NativePostgreSQLBackup || ValidateNativePostgreSQLRecoveryTask(t) != nil || t.NativePostgreSQLRecovery.Registry == nil || t.NativePostgreSQLRecovery.PreparedRelease != nil {
		return NativePostgreSQLStorageObservation{}, failure("OFFLINE_ENGINE_NATIVE_STORAGE_CONTEXT_INVALID")
	}
	return c.observeDirectNativePostgreSQLStorage(ctx, t)
}

// Only this proven direct-host allocation root may feed Scope's physical statfs
// admission. No path from a remote/VM daemon is returned as a local substitute.
func (c *Client) NativePostgreSQLStorageRoot(ctx context.Context, t DatabaseTask) (string, error) {
	o, e := c.ObserveNativePostgreSQLStorage(ctx, t)
	return o.AllocationRoot, e
}

func (c *Client) nativePostgreSQLStorageInfo(ctx context.Context) (nativePostgreSQLStorageInfo, error) {
	var out nativePostgreSQLStorageInfo
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/info", nil, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK || wire.UniqueJSON(raw, &out) != nil || out.ID == "" || len(out.ID) > 256 || out.OSType != "linux" || !filepath.IsAbs(out.DockerRootDir) || filepath.Clean(out.DockerRootDir) != out.DockerRootDir || out.DockerRootDir == "/" || strings.ContainsAny(out.DockerRootDir, "\x00\r\n") {
		return out, failure("OFFLINE_ENGINE_NATIVE_STORAGE_IDENTITY_INVALID")
	}
	for _, option := range out.SecurityOptions {
		if strings.Contains(strings.ToLower(option), "rootless") || strings.Contains(strings.ToLower(option), "userns") {
			return out, failure("OFFLINE_ENGINE_NATIVE_STORAGE_USER_NAMESPACE_UNSUPPORTED")
		}
	}
	return out, nil
}

func nativePostgreSQLStorageSourceOwners(t DatabaseTask) []Ownership {
	p := t.NativePostgreSQLRecovery
	if p == nil || p.Registry == nil {
		return nil
	}
	out := []Ownership{}
	for _, store := range p.Stores {
		out = append(out, store.Owner)
	}
	return append(out, p.Registry.Owner)
}

// Use the original InspectVolume ownership/driver policy before and after the
// extra readonly mountpoint projection. No shared Volume shape is broadened.
func (c *Client) nativePostgreSQLStorageVolume(ctx context.Context, owner Ownership, allocationRoot string) (string, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_VOLUME_MAPPING_INVALID")
	before, found, e := c.InspectVolume(ctx, owner)
	if e != nil || !found {
		return "", bad
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/volumes/"+url.PathEscape(owner.VolumeName()), nil, nil, c.config.RequestSeconds)
	var fields struct {
		Name, Driver, Scope, Mountpoint string
		Labels                          map[string]string
		Options                         map[string]string
		Status                          map[string]json.RawMessage
	}
	if e != nil || status != http.StatusOK || wire.UniqueJSON(raw, &fields) != nil || fields.Name != owner.VolumeName() || fields.Driver != "local" || fields.Scope != "local" || len(fields.Options) != 0 || len(fields.Status) != 0 || !owner.matches(fields.Labels) || !reflect.DeepEqual(before.Labels, fields.Labels) || fields.Mountpoint != filepath.Join(allocationRoot, owner.VolumeName(), "_data") {
		return "", bad
	}
	after, found, e := c.InspectVolume(ctx, owner)
	if e != nil || !found || !reflect.DeepEqual(before, after) {
		return "", bad
	}
	return fields.Mountpoint, nil
}
