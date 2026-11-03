package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	currentConfigurationInputInvalid   = "OFFLINE_ENGINE_CURRENT_CONFIGURATION_INPUT_INVALID"
	currentConfigurationReadbackFailed = "OFFLINE_ENGINE_CURRENT_CONFIGURATION_READBACK_FAILED"
	currentConfigurationChanged        = "OFFLINE_ENGINE_CURRENT_CONFIGURATION_CHANGED"
)

// ConfigurationFileDigest comes from independently trusted immutable scope
// history. It is not authority supplied by an HTTP client or Engine response.
type ConfigurationFileDigest struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// ReadCurrentControlConfiguration returns a complete verified private payload
// only. The caller separately owns generation history and activation authority.
func (c *Client) ReadCurrentControlConfiguration(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFileDigest) ([]ConfigurationFile, error) {
	return c.readControlConfiguration(ctx, o, id, destination, uid, gid, files, false)
}

// ReadFailedControlConfiguration is the explicit failed-exit recovery entrance;
// it never broadens the current or legacy stopped-control predicates.
func (c *Client) ReadFailedControlConfiguration(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFileDigest) ([]ConfigurationFile, error) {
	return c.readControlConfiguration(ctx, o, id, destination, uid, gid, files, true)
}

func (c *Client) configurationDigestManifest(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFileDigest) ([]ConfigurationFileDigest, error) {
	bad := failure(currentConfigurationInputInvalid)
	if ctx == nil || c == nil || c.http == nil || !currentConfigurationOwner(o) || !containerIdentity.MatchString(id) || !utf8.ValidString(destination) || path.Clean(destination) != destination || !strings.HasPrefix(destination, "/run/projectone/") || strings.Contains(destination, "\\") || strings.ContainsFunc(destination, unicode.IsControl) || uid < 1 || gid < 1 || uid > math.MaxInt32 || gid > math.MaxInt32 || len(files) == 0 {
		return nil, bad
	}
	minimumArchive := int64(tarBlockBytes + tarEndBytes)
	perFileOverhead := int64(2 * tarBlockBytes)
	if c.config.ResponseBytes <= archiveReadbackReserveBytes+minimumArchive {
		return nil, bad
	}
	remaining := c.config.ResponseBytes - archiveReadbackReserveBytes
	needed := minimumArchive
	// Each positive file needs at least one byte plus its conservative header/
	// padding reserve. Bound the clone before allocating an unbounded manifest.
	if int64(len(files)) > (remaining-needed)/(perFileOverhead+1) {
		return nil, bad
	}
	frozen := append([]ConfigurationFileDigest(nil), files...)
	seen := make(map[string]bool, len(frozen))
	for _, file := range frozen {
		if !wire.SafePath(file.Name) || strings.Contains(file.Name, "/") || seen[file.Name] || file.Bytes < 1 || !wire.ValidSHA256(file.SHA256) || file.Bytes > remaining || needed > remaining-file.Bytes || remaining-needed-file.Bytes < perFileOverhead {
			return nil, bad
		}
		seen[file.Name] = true
		needed += file.Bytes + perFileOverhead
	}
	sort.Slice(frozen, func(i, j int) bool { return frozen[i].Name < frozen[j].Name })
	return frozen, nil
}

func configurationReadbackMount(object RuntimeContainer, o Ownership, destination string, uid, gid int, failed bool) bool {
	owned := currentOwnedControl(o.ScopeID, object)
	if failed {
		owned = failedOwnedControl(o.ScopeID, object)
	}
	if !owned || object.Config.User != strconv.Itoa(uid)+":"+strconv.Itoa(gid) {
		return false
	}
	overlap := func(target string) bool {
		return target == "/" || target == destination || strings.HasPrefix(target, destination+"/") || strings.HasPrefix(destination, target+"/")
	}
	configured, actual := 0, 0
	for _, mount := range object.HostConfig.Mounts {
		if mount.Source != o.VolumeName() && !overlap(mount.Target) {
			continue
		}
		if mount.Target != destination || mount.Type != "volume" || mount.Source != o.VolumeName() || !mount.ReadOnly || mount.VolumeOptions == nil || !mount.VolumeOptions.NoCopy || mount.VolumeOptions.Subpath != "" {
			return false
		}
		configured++
	}
	for _, mount := range object.Mounts {
		if mount.Name != o.VolumeName() && !overlap(mount.Destination) {
			continue
		}
		if mount.Destination != destination || mount.Type != "volume" || mount.Name != o.VolumeName() || mount.Driver != "local" || mount.RW {
			return false
		}
		actual++
	}
	return configured == 1 && actual == 1
}

// Unlike the existing no-foreign-consumer helper, payload readback additionally
// requires the exact current control to remain the single observed consumer.
func (c *Client) configurationReadbackVolume(ctx context.Context, o Ownership, id string) (Volume, error) {
	volume, found, e := c.InspectVolume(ctx, o)
	if e != nil {
		return Volume{}, e
	}
	if !found {
		return Volume{}, failure(currentConfigurationReadbackFailed)
	}
	filters, _ := json.Marshal(map[string][]string{"volume": {o.VolumeName()}})
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/json", url.Values{"all": {"1"}, "filters": {string(filters)}}, nil, c.config.RequestSeconds)
	if e != nil {
		return Volume{}, e
	}
	var consumers []RuntimeObject
	if status != http.StatusOK || wire.UniqueJSON(raw, &consumers) != nil || len(consumers) != 1 || consumers[0].ID != id {
		return Volume{}, failure(currentConfigurationReadbackFailed)
	}
	return volume, nil
}

func configurationReadbackPayload(ctx context.Context, raw []byte, files []ConfigurationFileDigest, uid, gid int, root string) ([]ConfigurationFile, error) {
	bad := failure("OFFLINE_ENGINE_CONFIGURATION_INVENTORY_MISMATCH")
	expected := make(map[string]ConfigurationFileDigest, len(files))
	for _, file := range files {
		expected[file.Name] = file
	}
	contents := make(map[string][]byte, len(files))
	archive := tar.NewReader(bytes.NewReader(raw))
	for {
		if ctx.Err() != nil {
			return nil, failure(currentConfigurationReadbackFailed)
		}
		header, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil || header.Linkname != "" {
			return nil, bad
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == root || name == root+"/"+UtilityPrivateDirectory {
			if header.Typeflag != tar.TypeDir || header.Size != 0 {
				return nil, bad
			}
			continue
		}
		prefix := root + "/" + UtilityPrivateDirectory + "/"
		if !strings.HasPrefix(header.Name, prefix) || header.Typeflag != tar.TypeReg {
			return nil, bad
		}
		fileName := strings.TrimPrefix(header.Name, prefix)
		file, found := expected[fileName]
		if !found || header.Size != file.Bytes || contents[fileName] != nil {
			return nil, bad
		}
		data, e := io.ReadAll(io.LimitReader(archive, file.Bytes+1))
		if e != nil || int64(len(data)) != file.Bytes || wire.Digest(data) != file.SHA256 {
			return nil, bad
		}
		contents[fileName] = data
	}
	if len(contents) != len(files) {
		return nil, bad
	}
	result := make([]ConfigurationFile, 0, len(files))
	for _, file := range files {
		result = append(result, ConfigurationFile{Name: file.Name, Contents: contents[file.Name]})
	}
	observed, e := inspectPrivateArchive(raw, result, uid, gid, root)
	if e != nil || observed.Status != ConfigurationPayloadVerified {
		return nil, bad
	}
	return result, nil
}

func (c *Client) readControlConfiguration(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFileDigest, failed bool) ([]ConfigurationFile, error) {
	frozen, e := c.configurationDigestManifest(ctx, o, id, destination, uid, gid, files)
	if e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		return nil, failure(currentConfigurationReadbackFailed)
	}
	before, found, e := c.InspectRuntime(ctx, o.ScopeID, "control")
	if e != nil {
		return nil, e
	}
	if !found || before.ID != id || !configurationReadbackMount(before, o, destination, uid, gid, failed) {
		return nil, failure(currentConfigurationReadbackFailed)
	}
	volume, e := c.configurationReadbackVolume(ctx, o, id)
	if e != nil {
		return nil, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {destination}}, nil, c.config.RequestSeconds)
	if e != nil {
		return nil, e
	}
	if status != http.StatusOK {
		return nil, failure(currentConfigurationReadbackFailed)
	}
	result, e := configurationReadbackPayload(ctx, raw, frozen, uid, gid, path.Base(destination))
	if e != nil {
		return nil, e
	}
	after, found, e := c.InspectRuntime(ctx, o.ScopeID, "control")
	if e != nil || !found || after.ID != id || !configurationReadbackMount(after, o, destination, uid, gid, failed) {
		return nil, failure(currentConfigurationChanged)
	}
	first, fe := RuntimeContainerDigest(before)
	last, le := RuntimeContainerDigest(after)
	if fe != nil || le != nil || first != last {
		return nil, failure(currentConfigurationChanged)
	}
	afterVolume, e := c.configurationReadbackVolume(ctx, o, id)
	if e != nil || !reflect.DeepEqual(volume, afterVolume) || ctx.Err() != nil {
		return nil, failure(currentConfigurationChanged)
	}
	for index := range result {
		result[index].Contents = bytes.Clone(result[index].Contents)
	}
	if ctx.Err() != nil {
		return nil, failure(currentConfigurationChanged)
	}
	return result, nil
}
