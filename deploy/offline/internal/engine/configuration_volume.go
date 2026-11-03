package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const archiveReadbackReserveBytes = 10240
const tarBlockBytes = 512
const tarEndBytes = 2 * tarBlockBytes
const ConfigurationPayloadVerified = "verified_private_volume_payload"

// Contents never enter JSON diagnostics. The scope loader owns protected input files.
const TLSConfigurationRole = "tls-control"
const CumulativeControlRole = "upgrade-control"
const CumulativeOperatorRole = "upgrade-operator"

func currentConfigurationOwner(o Ownership) bool {
	return o.valid() && (o.Role == TLSConfigurationRole+"-"+o.OperationID || o.Role == CumulativeControlRole+"-"+o.OperationID)
}

type ConfigurationFile struct {
	Name     string `json:"name"`
	Contents []byte `json:"-"`
}
type ConfigurationObservation struct {
	Status        string `json:"status"`
	Files         int    `json:"files"`
	Bytes         int64  `json:"bytes"`
	Qualification string `json:"qualification"`
}

func (c *Client) configurationTar(files []ConfigurationFile, uid, gid int) ([]byte, error) {
	if uid < 1 || gid < 1 || uid > math.MaxInt32 || gid > math.MaxInt32 {
		return nil, failure("OFFLINE_ENGINE_CONFIGURATION_OWNER_INVALID")
	}
	remaining := c.config.ResponseBytes - archiveReadbackReserveBytes
	if remaining < 1536 {
		return nil, failure("OFFLINE_ENGINE_CONFIGURATION_ARCHIVE_BUDGET_REQUIRED")
	}
	names := map[string]bool{}
	needed := int64(1536)
	for _, file := range files {
		if !wire.SafePath(file.Name) || strings.Contains(file.Name, "/") || names[file.Name] || len(file.Contents) < 1 {
			return nil, failure("OFFLINE_ENGINE_CONFIGURATION_FILE_INVALID")
		}
		names[file.Name] = true
		size := int64(len(file.Contents))
		if size > remaining || needed > remaining-size-1024 {
			return nil, failure("OFFLINE_ENGINE_CONFIGURATION_ARCHIVE_LIMIT")
		}
		needed += size + 1024
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	if writer.WriteHeader(&tar.Header{Name: UtilityPrivateDirectory, Mode: 0700, Typeflag: tar.TypeDir, Uid: uid, Gid: gid, Format: tar.FormatUSTAR}) != nil {
		return nil, failure("OFFLINE_ENGINE_CONFIGURATION_ARCHIVE_INVALID")
	}
	for _, file := range files {
		if e := writer.WriteHeader(&tar.Header{Name: UtilityPrivateDirectory + "/" + file.Name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(file.Contents)), Uid: uid, Gid: gid, Format: tar.FormatUSTAR}); e != nil {
			return nil, failure("OFFLINE_ENGINE_CONFIGURATION_ARCHIVE_INVALID")
		}
		if _, e := writer.Write(file.Contents); e != nil {
			return nil, failure("OFFLINE_ENGINE_CONFIGURATION_ARCHIVE_INVALID")
		}
	}
	if writer.Close() != nil || int64(output.Len()) > remaining {
		return nil, failure("OFFLINE_ENGINE_CONFIGURATION_ARCHIVE_LIMIT")
	}
	return output.Bytes(), nil
}
func (c *Client) ValidateConfigurationFiles(files []ConfigurationFile, uid, gid int) error {
	_, e := c.configurationTar(files, uid, gid)
	return e
}
func (c *Client) configurationUtility(ctx context.Context, o Ownership, image string, uid, gid int) (Utility, error) {
	if _, found, e := c.InspectVolume(ctx, o); e != nil {
		return Utility{}, e
	} else if !found {
		return Utility{}, failure("OFFLINE_ENGINE_OWNED_VOLUME_REQUIRED")
	}
	utility, found, e := c.InspectUtility(ctx, o, image, uid, gid)
	if e != nil {
		return Utility{}, e
	}
	if !found {
		return Utility{}, failure("OFFLINE_ENGINE_OWNED_UTILITY_REQUIRED")
	}
	if e = c.noForeignConsumers(ctx, o, utility.ID); e != nil {
		return Utility{}, e
	}
	return utility, nil
}
func (c *Client) InspectConfiguration(ctx context.Context, o Ownership, image string, uid, gid int, files []ConfigurationFile) (ConfigurationObservation, error) {
	result := ConfigurationObservation{}
	if e := c.ValidateConfigurationFiles(files, uid, gid); e != nil {
		return result, e
	}
	utility, e := c.configurationUtility(ctx, o, image, uid, gid)
	if e != nil {
		return result, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination}}, nil, c.config.RequestSeconds)
	if e != nil {
		return result, e
	}
	if status != http.StatusOK {
		return result, failure("OFFLINE_ENGINE_CONFIGURATION_READBACK_FAILED")
	}
	result, e = inspectConfigurationArchive(raw, files, uid, gid)
	if e != nil {
		return result, e
	}
	observed, found, e := c.InspectUtility(ctx, o, image, uid, gid)
	if e != nil || !found || observed.ID != utility.ID {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_CONFIGURATION_UTILITY_CHANGED")
	}
	return result, nil
}
func inspectConfigurationArchive(raw []byte, files []ConfigurationFile, uid, gid int) (ConfigurationObservation, error) {
	return inspectPrivateArchive(raw, files, uid, gid, path.Base(UtilityDestination))
}
func inspectPrivateArchive(raw []byte, files []ConfigurationFile, uid, gid int, rootName string) (ConfigurationObservation, error) {
	bad := func() (ConfigurationObservation, error) {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_CONFIGURATION_INVENTORY_MISMATCH")
	}
	if len(raw) < 3*tarBlockBytes || len(raw)%tarBlockBytes != 0 || len(bytes.Trim(raw[len(raw)-tarEndBytes:], "\x00")) != 0 {
		return bad()
	}
	expected := map[string]ConfigurationFile{}
	for _, file := range files {
		expected[file.Name] = file
	}
	seen := map[string]bool{}
	private := false
	total := int64(0)
	source := bytes.NewReader(raw)
	archive := tar.NewReader(source)
	for {
		header, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return bad()
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] || header.Linkname != "" {
			return bad()
		}
		seen[name] = true
		if name == rootName {
			// The daemon owns this nonsecret wrapper. Consumers use the private child.
			if header.Typeflag != tar.TypeDir || header.Mode != 0755 || header.Uid != 0 || header.Gid != 0 {
				return bad()
			}
			continue
		}
		if name == rootName+"/"+UtilityPrivateDirectory {
			if header.Typeflag != tar.TypeDir || header.Mode != 0700 || header.Uid != uid || header.Gid != gid {
				return bad()
			}
			private = true
			continue
		}
		prefix := rootName + "/" + UtilityPrivateDirectory + "/"
		if !strings.HasPrefix(name, prefix) || header.Typeflag != tar.TypeReg || header.Mode != 0600 || header.Uid != uid || header.Gid != gid {
			return bad()
		}
		fileName := strings.TrimPrefix(name, prefix)
		file, ok := expected[fileName]
		if !ok || header.Size != int64(len(file.Contents)) {
			return bad()
		}
		data, e := io.ReadAll(io.LimitReader(archive, header.Size+1))
		if e != nil || !bytes.Equal(data, file.Contents) {
			return bad()
		}
		total += header.Size
	}
	if !seen[rootName] {
		return bad()
	}
	if len(bytes.Trim(raw[len(raw)-source.Len():], "\x00")) > 0 {
		return bad()
	}
	qualification := "verified_private_volume_bytes_only_not_store_namespace_application_or_target"
	if len(seen) == 1 {
		return ConfigurationObservation{Status: "observed_empty_owned_volume", Qualification: qualification}, nil
	}
	if !private || len(seen) != len(files)+2 {
		return bad()
	}
	return ConfigurationObservation{Status: ConfigurationPayloadVerified, Files: len(files), Bytes: total, Qualification: qualification}, nil
}
func (c *Client) PutConfiguration(ctx context.Context, o Ownership, image string, uid, gid int, files []ConfigurationFile) error {
	body, e := c.configurationTar(files, uid, gid)
	if e != nil {
		return e
	}
	state, e := c.InspectConfiguration(ctx, o, image, uid, gid, files)
	if e != nil {
		return e
	}
	if state.Status != "observed_empty_owned_volume" {
		return failure("OFFLINE_ENGINE_NONEMPTY_CONFIGURATION_PRESERVED")
	}
	utility, e := c.configurationUtility(ctx, o, image, uid, gid)
	if e != nil {
		return e
	}
	_, status, e := c.request(ctx, http.MethodPut, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination}, "copyUIDGID": {"true"}, "noOverwriteDirNonDir": {"true"}}, bytes.NewReader(body), c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return failure("OFFLINE_ENGINE_CONFIGURATION_WRITE_UNCERTAIN")
	}
	state, e = c.InspectConfiguration(ctx, o, image, uid, gid, files)
	if e != nil || state.Status != ConfigurationPayloadVerified {
		return failure("OFFLINE_ENGINE_CONFIGURATION_WRITE_UNCERTAIN")
	}
	return nil
}

// RemoveTLSGenerationVolume removes only an unused per-generation configuration volume.
// The live/old installation roles cannot match this fixed ownership boundary.
func (c *Client) RemoveTLSGenerationVolume(ctx context.Context, o Ownership) error {
	return c.removeGeneratedVolume(ctx, o, []string{TLSConfigurationRole})
}
