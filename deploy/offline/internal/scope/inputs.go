package scope

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const inputsDirectory = "inputs"
const namedVolumeStorage = "docker_named_volume"
const localizationConfigurationRole = "localization-config"

type SourceFile struct {
	Name       string `json:"name"`
	SourceFile string `json:"source_file"`
}
type SourceRole struct {
	Key   string       `json:"key"`
	Files []SourceFile `json:"files,omitempty"`
}
type SourceCatalog struct {
	FormatVersion  int          `json:"format_version"`
	StorageProfile string       `json:"storage_profile"`
	ImageAssetID   string       `json:"image_asset_id"`
	Roles          []SourceRole `json:"roles"`
}
type InputFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type InputRole struct {
	Key                    string                         `json:"key"`
	UID                    int                            `json:"uid"`
	GID                    int                            `json:"gid"`
	Files                  []InputFile                    `json:"files"`
	PayloadSHA256          string                         `json:"payload_sha256"`
	PreparedReleaseProfile *engine.PreparedReleaseProfile `json:"prepared_release_profile,omitempty"`
	PreparedReleaseSHA256  string                         `json:"prepared_release_sha256,omitempty"`
}
type InputsDescriptor struct {
	FormatVersion       int         `json:"format_version"`
	ScopeID             string      `json:"scope_id"`
	ConfigurationSHA256 string      `json:"configuration_sha256"`
	ModelSHA256         string      `json:"model_sha256"`
	CatalogSHA256       string      `json:"catalog_sha256"`
	ImageAssetID        string      `json:"image_asset_id"`
	ImageID             string      `json:"image_id"`
	ImageConfigDigest   string      `json:"image_config_digest"`
	StorageProfile      string      `json:"storage_profile"`
	Roles               []InputRole `json:"roles"`
}
type InputsRecord struct {
	Profile           string `json:"profile,omitempty"`
	DescriptorSHA256  string `json:"descriptor_sha256"`
	ModelSHA256       string `json:"model_sha256"`
	CatalogSHA256     string `json:"catalog_sha256"`
	ImageAssetID      string `json:"image_asset_id"`
	ImageID           string `json:"image_id"`
	ImageConfigDigest string `json:"image_config_digest"`
}
type preparedInputs struct {
	descriptor InputsDescriptor
	record     InputsRecord
	model      []byte
	catalog    []byte
	files      map[string][]engine.ConfigurationFile
}

var roleServices = map[string]string{localizationConfigurationRole: "control", engine.BootstrapStateRole: "control", "control-config": "control", "operator-config": "control", "postgres-config": "postgres", "gateway-config": "gateway", "models": "control", "datasets": "control", "training": "control", "preparations": "control", "postgres": "postgres"}
var serviceRoles = map[string]map[string]bool{"control": {localizationConfigurationRole: true, "control-config": true, "models": true, "datasets": true, "training": true, "preparations": true}, "postgres": {"postgres-config": true, "postgres": true}, "gateway": {"gateway-config": true}}

func serviceVolumeMountAllowed(service string, mount compose.Mount) bool {
	return mount.Type == "volume" && serviceRoles[service][mount.Source] && (mount.Source != localizationConfigurationRole || mount.ReadOnly)
}

func protectedInput(c configuration.Config, name string, maximum int64) ([]byte, error) {
	return readPrivateInput(c, name, maximum, "OFFLINE_SCOPE_PRIVATE_INPUT_REFERENCE_REQUIRED")
}
func roleUser(model compose.Model, key string) (int, int, error) {
	service, ok := roleServices[key]
	if !ok {
		return 0, 0, failure("OFFLINE_SCOPE_INPUT_ROLE_INVALID")
	}
	parts := strings.Split(model.Services[service].User, ":")
	if len(parts) != 2 {
		return 0, 0, failure("OFFLINE_SCOPE_INPUT_ROLE_SERVICE_REQUIRED")
	}
	uid, e := strconv.ParseInt(parts[0], 10, 32)
	gid, ge := strconv.ParseInt(parts[1], 10, 32)
	if e != nil || ge != nil || uid < 1 || gid < 1 || strconv.FormatInt(uid, 10) != parts[0] || strconv.FormatInt(gid, 10) != parts[1] {
		return 0, 0, failure("OFFLINE_SCOPE_INPUT_ROLE_USER_INVALID")
	}
	return int(uid), int(gid), nil
}
func (l *locked) collectInputs(ctx context.Context, client *engine.Client, renderer modelRenderer) (preparedInputs, error) {
	result := preparedInputs{files: map[string][]engine.ConfigurationFile{}}
	c := l.config
	if c.Compose == nil || c.Engine == nil || c.Engine.Utility == nil || c.Compose.InputsFile == "" || c.Compose.ModelFile == "" {
		return result, failure("OFFLINE_SCOPE_STORAGE_INPUT_CONFIGURATION_REQUIRED")
	}
	state := imageState(l.events)
	if state.engine == nil || len(state.pending) > 0 {
		return result, failure("OFFLINE_SCOPE_COMPLETED_IMAGE_ADMISSION_REQUIRED")
	}
	if _, e := l.checkEngine(ctx, client); e != nil {
		return result, e
	}
	raw, e := protectedInput(c, c.Compose.InputsFile, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return result, e
	}
	var catalog SourceCatalog
	if wire.StrictJSON(raw, &catalog) != nil || catalog.FormatVersion != 1 || catalog.StorageProfile != namedVolumeStorage || !wire.SafePath(catalog.ImageAssetID) || strings.Contains(catalog.ImageAssetID, "/") || len(catalog.Roles) < 1 || len(catalog.Roles) > inputRoleCount(l.marker.Profile) {
		return result, failure("OFFLINE_SCOPE_STORAGE_INPUT_CATALOG_INVALID")
	}
	image, ok := state.completed[catalog.ImageAssetID]
	if !ok {
		return result, failure("OFFLINE_SCOPE_INITIALIZER_IMAGE_NOT_ADMITTED")
	}
	modelRaw, e := readModel(c)
	if e != nil {
		return result, e
	}
	model, e := compose.DecodeModel(modelRaw, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return result, e
	}
	allowed := map[string]bool{}
	for _, record := range state.completed {
		allowed[record.ManifestDigest] = true
	}
	for _, service := range model.Services {
		if !allowed[service.Image] {
			return result, failure("OFFLINE_SCOPE_MODEL_IMAGE_NOT_ADMITTED")
		}
		for _, record := range state.completed {
			if record.ManifestDigest == service.Image {
				observed, found, e := client.Inspect(ctx, service.Image)
				if e != nil {
					return result, e
				}
				if !found || !matchesImage(observed, record) {
					return result, failure("OFFLINE_SCOPE_RECORDED_IMAGE_MISSING_OR_CHANGED")
				}
				break
			}
		}
	}
	if observed, found, e := client.Inspect(ctx, image.ManifestDigest); e != nil {
		return result, e
	} else if !found || !matchesImage(observed, image) {
		return result, failure("OFFLINE_SCOPE_RECORDED_IMAGE_MISSING_OR_CHANGED")
	}
	if _, e = renderer.Render(ctx, compose.RenderContext{ScopeID: l.marker.ScopeID, ScopeRoot: c.ScopeRoot, AllowedImages: allowed}, model); e != nil {
		return result, e
	}
	keys := map[string]bool{}
	for _, key := range model.Volumes {
		if keys[key] {
			return result, failure("OFFLINE_SCOPE_DUPLICATE_VOLUME_ROLE")
		}
		keys[key] = true
	}
	if len(keys) != len(catalog.Roles) {
		return result, failure("OFFLINE_SCOPE_MODEL_STORAGE_ROLES_MISMATCH")
	}
	for name, service := range model.Services {
		for _, mount := range service.Mounts {
			if mount.Type == "bind" && name == "control" && mount.ReadOnly && mount.Source == filepath.Join(c.ScopeRoot, view.RelativeDirectory) && mount.Target == view.ContainerDirectory {
				uid, _, ue := roleUser(model, "control-config")
				if ue != nil || uid == l.marker.OwnerUID {
					return result, failure("OFFLINE_SCOPE_RELEASE_VIEW_UID_ISOLATION_REQUIRED")
				}
				projection, pe := l.releaseViewDirectory(false)
				if pe != nil {
					return result, pe
				}
				projection.Close()
				continue
			}
			if !inputServiceMountAllowed(l.marker.Profile, name, mount) {
				return result, failure("OFFLINE_SCOPE_RUNTIME_PRIVILEGED_OR_FOREIGN_STORAGE_REFUSED")
			}
		}
	}
	d := InputsDescriptor{FormatVersion: 1, ScopeID: l.marker.ScopeID, ConfigurationSHA256: c.Fingerprint(), ModelSHA256: wire.Digest(modelRaw), CatalogSHA256: wire.Digest(raw), ImageAssetID: catalog.ImageAssetID, ImageID: image.ManifestDigest, ImageConfigDigest: image.ConfigDigest, StorageProfile: namedVolumeStorage, Roles: []InputRole{}}
	count := 0
	total := int64(0)
	for _, role := range catalog.Roles {
		if !keys[role.Key] || result.files[role.Key] != nil {
			return result, failure("OFFLINE_SCOPE_INPUT_ROLE_INVALID")
		}
		uid, gid, e := inputRoleUser(l.marker.Profile, model, role.Key)
		if e != nil {
			return result, e
		}
		entry := InputRole{Key: role.Key, UID: uid, GID: gid, Files: []InputFile{}}
		payload := []engine.ConfigurationFile{}
		for _, reference := range role.Files {
			count++
			if count > c.Verification.MaximumAssets || !wire.SafePath(reference.Name) || strings.Contains(reference.Name, "/") {
				return result, failure("OFFLINE_SCOPE_INPUT_FILE_INVALID")
			}
			data, e := protectedInput(c, reference.SourceFile, c.Verification.MaximumMetadataBytes)
			if e != nil {
				return result, e
			}
			payload = append(payload, engine.ConfigurationFile{Name: reference.Name, Contents: data})
			entry.Files = append(entry.Files, InputFile{Name: reference.Name, Bytes: int64(len(data)), SHA256: wire.Digest(data)})
			total += int64(len(data))
		}
		if strings.HasSuffix(role.Key, "-config") && len(payload) == 0 || !strings.HasSuffix(role.Key, "-config") && len(payload) != 0 {
			return result, failure("OFFLINE_SCOPE_CONFIGURATION_OR_STORE_PAYLOAD_INVALID")
		}
		if e = client.ValidateConfigurationFiles(payload, uid, gid); e != nil {
			return result, e
		}
		sort.Slice(payload, func(i, j int) bool { return payload[i].Name < payload[j].Name })
		sort.Slice(entry.Files, func(i, j int) bool { return entry.Files[i].Name < entry.Files[j].Name })
		entry.PayloadSHA256 = wire.Digest(encode(entry.Files))
		if role.Key == compose.MVPRegistryReleaseRole {
			profile, e := l.registryReleaseDescription(ctx, filepath.Join(l.config.ScopeRoot, "release"), l.marker.ArchiveSHA256, l.marker.ManifestSHA256)
			if e != nil {
				return result, e
			}
			entry.PreparedReleaseProfile = &profile
			entry.PreparedReleaseSHA256 = wire.Digest(encode(profile))
		}
		result.files[role.Key] = payload
		d.Roles = append(d.Roles, entry)
	}
	if l.marker.Profile == wire.MVPPostgreSQLCPUProfile {
		if _, ok := result.files[engine.NativePostgreSQLRegistryRole]; !ok {
			return result, failure("OFFLINE_SCOPE_MVP_REGISTRY_ROLE_REQUIRED")
		}
		if _, ok := result.files[compose.MVPRegistryReleaseRole]; !ok {
			return result, failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_REQUIRED")
		}
		if _, e := compose.MVPRegistryReleaseTarget(model); e != nil {
			return result, e
		}
		if e := validateMVPRegistryReleaseBinding(result.files, model, l.marker.ArchiveSHA256, l.marker.ManifestSHA256); e != nil {
			return result, e
		}
		if e := validateMVPInitialCapabilities(result.files, model); e != nil {
			return result, e
		}
	}
	if wire.IsCumulativeCPUProfile(l.marker.Profile) {
		for _, role := range []string{"operator-config", "control-config", localizationConfigurationRole, "postgres-config", "postgres", "models", "datasets", "training", "preparations", "bootstrap-state"} {
			if _, ok := result.files[role]; !ok {
				return result, failure("OFFLINE_SCOPE_CUMULATIVE_STORAGE_ROLE_REQUIRED")
			}
		}
	}
	if l.marker.Profile == wire.PlatformProfile {
		expected := len(roleServices)
		if _, present := result.files[localizationConfigurationRole]; !present {
			expected--
		}
		if len(result.files) != expected {
			return result, failure("OFFLINE_SCOPE_COMPLETE_PLATFORM_STORAGE_ROLES_REQUIRED")
		}
	}
	sort.Slice(d.Roles, func(i, j int) bool { return d.Roles[i].Key < d.Roles[j].Key })
	var filesystem syscall.Statfs_t
	if syscall.Statfs(c.ScopeRoot, &filesystem) != nil || float64(filesystem.Bavail)*float64(filesystem.Bsize)-float64(total*2+c.Verification.MaximumMetadataBytes) < float64(filesystem.Blocks)*float64(filesystem.Bsize)*0.30 {
		return result, failure("OFFLINE_SCOPE_STORAGE_HEADROOM_REQUIRED")
	}
	result.descriptor = d
	result.model = modelRaw
	result.catalog = raw
	result.record = InputsRecord{Profile: inputProfileTag(l.marker.Profile), DescriptorSHA256: wire.Digest(encode(d)), ModelSHA256: d.ModelSHA256, CatalogSHA256: d.CatalogSHA256, ImageAssetID: d.ImageAssetID, ImageID: d.ImageID, ImageConfigDigest: d.ImageConfigDigest}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	return result, nil
}
func (p preparedInputs) descriptorBytes() []byte {
	raw, _ := json.Marshal(p.descriptor)
	return append(raw, '\n')
}

func (l *locked) snapshotInputs(ctx context.Context, p preparedInputs) error {
	if e := l.root.Mkdir(inputsDirectory, 0700); e != nil {
		return failure("OFFLINE_SCOPE_EXISTING_INPUT_SNAPSHOT_PRESERVED")
	}
	if e := writeNew(l.root, "inputs/descriptor.json", p.descriptorBytes()); e != nil {
		return e
	}
	if e := writeNew(l.root, "inputs/model.json", p.model); e != nil {
		return e
	}
	if e := writeNew(l.root, "inputs/catalog.json", p.catalog); e != nil {
		return e
	}
	if e := l.root.Mkdir("inputs/payloads", 0700); e != nil {
		return failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_DIRECTORY_FAILED")
	}
	for _, role := range p.descriptor.Roles {
		if ctx.Err() != nil {
			return failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_INTERRUPTED")
		}
		directory := "inputs/payloads/" + role.Key
		if e := l.root.Mkdir(directory, 0700); e != nil {
			return failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_DIRECTORY_FAILED")
		}
		for _, file := range p.files[role.Key] {
			if e := writeNew(l.root, directory+"/"+file.Name, file.Contents); e != nil {
				return e
			}
		}
		if e := l.syncInputDirectory(directory); e != nil {
			return e
		}
	}
	for _, directory := range []string{"inputs/payloads", inputsDirectory, "."} {
		if e := l.syncInputDirectory(directory); e != nil {
			return e
		}
	}
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_COMMIT_UNCERTAIN")
	}
	return nil
}
func (l *locked) syncInputDirectory(name string) error {
	directory, e := l.root.Open(name)
	if e != nil {
		return failure("OFFLINE_SCOPE_INPUT_DIRECTORY_DURABILITY_UNCERTAIN")
	}
	e = directory.Sync()
	ce := directory.Close()
	if e != nil || ce != nil {
		return failure("OFFLINE_SCOPE_INPUT_DIRECTORY_DURABILITY_UNCERTAIN")
	}
	return nil
}
func (l *locked) inputDirectoryEntries(name string) ([]os.DirEntry, error) {
	info, e := l.root.Lstat(name)
	if e != nil || info.Mode() != os.ModeDir|0700 || !owned(info) {
		return nil, failure("OFFLINE_SCOPE_INPUT_DIRECTORY_UNSAFE")
	}
	directory, e := l.root.Open(name)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_INPUT_DIRECTORY_UNAVAILABLE")
	}
	defer directory.Close()
	entries := []os.DirEntry{}
	for {
		batch, e := directory.ReadDir(16)
		if e != nil && e != io.EOF {
			return nil, failure("OFFLINE_SCOPE_INPUT_DIRECTORY_UNAVAILABLE")
		}
		entries = append(entries, batch...)
		if len(entries) > l.config.Verification.MaximumAssets+16 {
			return nil, failure("OFFLINE_SCOPE_INPUT_INVENTORY_LIMIT")
		}
		if e == io.EOF {
			return entries, nil
		}
	}
}
func validInputsRecord(r *InputsRecord) bool {
	return r != nil && (r.Profile == "" || r.Profile == wire.MVPPostgreSQLCPUProfile) && wire.ValidSHA256(r.DescriptorSHA256) && wire.ValidSHA256(r.ModelSHA256) && wire.ValidSHA256(r.CatalogSHA256) && wire.SafePath(r.ImageAssetID) && !strings.Contains(r.ImageAssetID, "/") && immutableDigest(r.ImageID) && immutableDigest(r.ImageConfigDigest)
}
func (l *locked) readInputs(ctx context.Context, record InputsRecord) (preparedInputs, error) {
	result := preparedInputs{files: map[string][]engine.ConfigurationFile{}}
	if !validInputsRecord(&record) || record.Profile != inputProfileTag(l.marker.Profile) {
		return result, failure("OFFLINE_SCOPE_INPUT_RECORD_INVALID")
	}
	entries, e := l.inputDirectoryEntries(inputsDirectory)
	if e != nil {
		return result, e
	}
	if len(entries) != 4 {
		return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_INVENTORY_MISMATCH")
	}
	allowed := map[string]bool{"descriptor.json": true, "model.json": true, "catalog.json": true, "payloads": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_INVENTORY_MISMATCH")
		}
	}
	raw, e := regular(l.root, "inputs/descriptor.json", l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return result, e
	}
	var descriptor InputsDescriptor
	if wire.Digest(raw) != record.DescriptorSHA256 || wire.StrictJSON(raw, &descriptor) != nil || descriptor.FormatVersion != 1 || descriptor.ScopeID != l.marker.ScopeID || descriptor.ConfigurationSHA256 != l.config.Fingerprint() || descriptor.ModelSHA256 != record.ModelSHA256 || descriptor.CatalogSHA256 != record.CatalogSHA256 || descriptor.ImageAssetID != record.ImageAssetID || descriptor.ImageID != record.ImageID || descriptor.ImageConfigDigest != record.ImageConfigDigest || descriptor.StorageProfile != namedVolumeStorage || len(descriptor.Roles) < 1 || len(descriptor.Roles) > inputRoleCount(l.marker.Profile) {
		return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_CONTEXT_MISMATCH")
	}
	model, e := regular(l.root, "inputs/model.json", l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return result, e
	}
	catalog, e := regular(l.root, "inputs/catalog.json", l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return result, e
	}
	if wire.Digest(model) != record.ModelSHA256 || wire.Digest(catalog) != record.CatalogSHA256 {
		return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_HASH_MISMATCH")
	}
	roles, e := l.inputDirectoryEntries("inputs/payloads")
	if e != nil {
		return result, e
	}
	if len(roles) != len(descriptor.Roles) {
		return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_INVENTORY_MISMATCH")
	}
	seen := map[string]bool{}
	for _, role := range descriptor.Roles {
		if ctx.Err() != nil {
			return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		if !inputRoleAllowed(l.marker.Profile, role.Key) || seen[role.Key] || role.UID < 1 || role.GID < 1 || !wire.ValidSHA256(role.PayloadSHA256) || wire.Digest(encode(role.Files)) != role.PayloadSHA256 {
			return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_ROLE_INVALID")
		}
		if role.Key == compose.MVPRegistryReleaseRole {
			sha, e := l.registryReleaseMetadata(ctx, filepath.Join(l.config.ScopeRoot, "release"), l.marker.ArchiveSHA256, l.marker.ManifestSHA256)
			if e != nil || sha != role.PreparedReleaseSHA256 || role.PreparedReleaseProfile == nil || wire.Digest(encode(*role.PreparedReleaseProfile)) != sha {
				return result, failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_CHANGED")
			}
		} else if role.PreparedReleaseSHA256 != "" || role.PreparedReleaseProfile != nil {
			return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_ROLE_INVALID")
		}
		seen[role.Key] = true
		files, e := l.inputDirectoryEntries("inputs/payloads/" + role.Key)
		if e != nil {
			return result, e
		}
		if len(files) != len(role.Files) {
			return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_INVENTORY_MISMATCH")
		}
		payload := []engine.ConfigurationFile{}
		names := map[string]bool{}
		for _, file := range role.Files {
			if !wire.SafePath(file.Name) || strings.Contains(file.Name, "/") || names[file.Name] || file.Bytes < 1 || file.Bytes > l.config.Verification.MaximumMetadataBytes || !wire.ValidSHA256(file.SHA256) {
				return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_FILE_INVALID")
			}
			names[file.Name] = true
			content, e := regular(l.root, "inputs/payloads/"+role.Key+"/"+file.Name, l.config.Verification.MaximumMetadataBytes)
			if e != nil {
				return result, e
			}
			if int64(len(content)) != file.Bytes || wire.Digest(content) != file.SHA256 {
				return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_HASH_MISMATCH")
			}
			payload = append(payload, engine.ConfigurationFile{Name: file.Name, Contents: content})
		}
		result.files[role.Key] = payload
	}
	for _, entry := range roles {
		if !seen[entry.Name()] {
			return result, failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_INVENTORY_MISMATCH")
		}
	}
	result.record = record
	result.descriptor = descriptor
	result.model = model
	result.catalog = catalog
	return result, nil
}
