package scope

import (
	"context"
	"encoding/json"
	"path"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// SDK description verifies every signed asset and original preparation metadata.
// The full profile is persisted under the plan/descriptor hash, never a success DTO.
func (l *locked) registryReleaseDescription(ctx context.Context, root, archive, manifest string) (engine.PreparedReleaseProfile, error) {
	raw, e := protectedInput(l.config, filepath.Join(root, "manifest.json"), l.config.Verification.MaximumManifestBytes)
	var selected struct {
		Profile string `json:"profile"`
	}
	if e != nil || wire.Digest(raw) != manifest || wire.UniqueJSON(raw, &selected) != nil || selected.Profile != wire.MVPPostgreSQLCPUProfile {
		return engine.PreparedReleaseProfile{}, failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_INVALID")
	}
	return engine.DescribePreparedRelease(ctx, engine.PreparedReleaseRequest{Root: root, Verification: l.config.Verification, ArchiveSHA256: archive, ManifestSHA256: manifest})
}
func (l *locked) registryReleaseMetadata(ctx context.Context, root, archive, manifest string) (string, error) {
	p, e := l.registryReleaseDescription(ctx, root, archive, manifest)
	if e != nil {
		return "", e
	}
	return wire.Digest(encode(p)), nil
}

func validateMVPRegistryReleaseBinding(files map[string][]engine.ConfigurationFile, model compose.Model, archive, manifest string) error {
	release, e := compose.MVPRegistryReleaseTarget(model)
	if e != nil {
		return e
	}
	store, e := compose.MVPRegistryTarget(model)
	if e != nil {
		return e
	}
	for _, role := range []string{"control-config", "operator-config"} {
		names := []string{"runtime.yaml"}
		root := ""
		for _, m := range model.Services["control"].Mounts {
			if m.Source == "control-config" || len(m.Source) > len(engine.CumulativeControlRole)+1 && m.Source[:len(engine.CumulativeControlRole)+1] == engine.CumulativeControlRole+"-" {
				root = m.Target
			}
		}
		if role == "operator-config" {
			names = append(names, "control.yaml")
			root = engine.DatabaseTaskDestination
		}
		for _, name := range names {
			raw, e := restoreNamedFile(files[role], name)
			if e != nil {
				return e
			}
			var c struct {
				Reference string `json:"image_registry_config_file"`
			}
			if wire.UniqueJSON(raw, &c) != nil || c.Reference != path.Join(root, "private", path.Base(c.Reference)) {
				return failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_BINDING_INVALID")
			}
			raw, e = restoreNamedFile(files[role], path.Base(c.Reference))
			if e != nil {
				return e
			}
			var p struct {
				Store        string `json:"store_root"`
				Prepared     string `json:"prepared_release_root"`
				Archive      string `json:"release_archive_sha256"`
				Manifest     string `json:"release_manifest_sha256"`
				Verification struct {
					Mount string `json:"mount_root"`
				} `json:"verification"`
			}
			if json.Unmarshal(raw, &p) != nil || p.Store != path.Join(store, "private") || p.Prepared != path.Join(release, "private") || p.Verification.Mount != release || p.Archive != archive || p.Manifest != manifest {
				return failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_BINDING_INVALID")
			}
		}
	}
	return nil
}

// These narrow methods never run an initializer. They copy/read back only the
// independently verified signed preparation, with original expected hashes.
type registryReleaseEngine interface {
	PutPreparedRelease(context.Context, engine.Ownership, string, int, int, engine.PreparedReleaseRequest, engine.PreparedReleaseProfile) error
	InspectPreparedRelease(context.Context, engine.Ownership, string, int, int, engine.PreparedReleaseProfile) (engine.ConfigurationObservation, error)
}

var _ registryReleaseEngine = (*engine.Client)(nil)

func (l *locked) registryReleasePayload(ctx context.Context, c storageEngine, owner engine.Ownership, image string, uid, gid int, source, archive, manifest, metadata string, profile *engine.PreparedReleaseProfile, put bool) (engine.ConfigurationObservation, error) {
	var out engine.ConfigurationObservation
	if profile == nil || wire.Digest(encode(*profile)) != metadata || profile.ArchiveSHA256 != archive || profile.ManifestSHA256 != manifest {
		return out, failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_CHANGED")
	}
	sdk, ok := c.(registryReleaseEngine)
	if !ok {
		return out, failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_SDK_REQUIRED")
	}
	if put {
		req := engine.PreparedReleaseRequest{Root: source, Verification: l.config.Verification, ArchiveSHA256: archive, ManifestSHA256: manifest}
		if e := sdk.PutPreparedRelease(ctx, owner, image, uid, gid, req, *profile); e != nil {
			return out, e
		}
	}
	return sdk.InspectPreparedRelease(ctx, owner, image, uid, gid, *profile)
}
func (l *locked) coldRegistryReleaseMetadata(ctx context.Context) (string, *engine.PreparedReleaseProfile, error) {
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return "", nil, failure("OFFLINE_SCOPE_COMPLETED_INPUT_SNAPSHOT_REQUIRED")
	}
	p, e := l.readInputs(ctx, *input)
	if e != nil {
		return "", nil, e
	}
	for _, r := range p.descriptor.Roles {
		if r.Key == compose.MVPRegistryReleaseRole {
			return r.PreparedReleaseSHA256, r.PreparedReleaseProfile, nil
		}
	}
	return "", nil, failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_REQUIRED")
}
func (l *locked) putVolumePayload(ctx context.Context, c storageEngine, r VolumeRecord, files []engine.ConfigurationFile) error {
	if r.Key != compose.MVPRegistryReleaseRole {
		return c.PutConfiguration(ctx, l.volumeOwner(r), r.ImageID, r.UID, r.GID, files)
	}
	metadata, profile, e := l.coldRegistryReleaseMetadata(ctx)
	if e != nil {
		return e
	}
	o, e := l.registryReleasePayload(ctx, c, l.volumeOwner(r), r.ImageID, r.UID, r.GID, filepath.Join(l.config.ScopeRoot, "release"), l.marker.ArchiveSHA256, l.marker.ManifestSHA256, metadata, profile, true)
	if e != nil {
		return e
	}
	if o.Status != engine.ConfigurationPayloadVerified {
		return failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_UNVERIFIED")
	}
	return nil
}
func (l *locked) inspectVolumePayload(ctx context.Context, c storageEngine, r VolumeRecord, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	if r.Key != compose.MVPRegistryReleaseRole {
		return c.InspectConfiguration(ctx, l.volumeOwner(r), r.ImageID, r.UID, r.GID, files)
	}
	metadata, profile, e := l.coldRegistryReleaseMetadata(ctx)
	if e != nil {
		return engine.ConfigurationObservation{}, e
	}
	return l.registryReleasePayload(ctx, c, l.volumeOwner(r), r.ImageID, r.UID, r.GID, filepath.Join(l.config.ScopeRoot, "release"), l.marker.ArchiveSHA256, l.marker.ManifestSHA256, metadata, profile, false)
}

func validateMVPNativeRegistryDescriptor(runtime, raw []byte, model compose.Model) error {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_DESCRIPTOR_BINDING_INVALID")
	d, e := configuration.LoadNativePostgreSQLRecoveryDescriptor(raw)
	if e != nil {
		return e
	}
	var core struct {
		Models struct {
			Root string `json:"artifact_root"`
		} `json:"models"`
		Datasets struct {
			Root string `json:"artifact_root"`
		} `json:"datasets"`
		Training struct {
			Root string `json:"artifact_root"`
		} `json:"training"`
		Preparations struct {
			Root string `json:"artifact_root"`
		} `json:"model_preparation"`
	}
	if wire.UniqueJSON(runtime, &core) != nil || d.StoreRoots != [4]string{core.Models.Root, core.Datasets.Root, core.Training.Root, core.Preparations.Root} {
		return bad
	}
	store, e := compose.MVPRegistryTarget(model)
	if e != nil {
		return e
	}
	release, e := compose.MVPRegistryReleaseTarget(model)
	if e != nil {
		return e
	}
	if d.RegistryRoot != path.Join(store, "private") || d.PreparedReleaseRoot != path.Join(release, "private") || d.RegistryScaffoldRoot != release {
		return bad
	}
	return nil
}
