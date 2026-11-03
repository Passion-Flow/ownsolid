package scope

import (
	"context"
	"path"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func (l *locked) verifyMVPNativeRestoreTargetConfiguration(ctx context.Context, request configuration.RestoreSourceRequest, source RestoreSourceResult, v verifiedRuntimeContext) error {
	if l.marker.Profile != wire.MVPPostgreSQLCPUProfile || !validMVPNativeRestoreSource(source) {
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_SOURCE_REQUIRED")
	}
	original, files, e := readMVPNativeRestoreSourceConfiguration(ctx, l.config, request, source)
	if e != nil {
		return e
	}
	names, policy, e := mvpNativeSourceReleasePolicy(original, files)
	if e != nil {
		return e
	}
	exempt, e := l.mvpNativeRestoreTargetReleasePolicy(v.inputs, v.model, original, policy, names)
	if e != nil {
		return e
	}
	d, e := l.mvpNativeRestoreDescriptor(v.inputs, v.model, original)
	if e != nil {
		return e
	}
	// This is the one validated deployment descriptor. Its source copy was
	// independently bound by the native source reader; target scope/DB/images
	// below are actual target facts. No unrelated secret gains an exemption.
	exempt["operator-config/"+configuration.NativePostgreSQLRecoveryDescriptorFile] = true
	if e = restoreRetainedKeyInventory(v.inputs, v.model, original.ConfigurationFiles, files, exempt); e != nil {
		return e
	}
	_, _, e = l.mvpNativeRestoreDomainMounts(v.model, d)
	return e
}

func (l *locked) mvpNativeRestoreDescriptor(p preparedInputs, model compose.Model, source mvpNativeBackupPlan) (configuration.NativePostgreSQLRecoveryDescriptor, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_DESCRIPTOR_CHANGED")
	raw, e := restoreNamedFile(p.files["operator-config"], configuration.NativePostgreSQLRecoveryDescriptorFile)
	if e != nil {
		return configuration.NativePostgreSQLRecoveryDescriptor{}, e
	}
	d, e := configuration.LoadNativePostgreSQLRecoveryDescriptor(raw)
	if e != nil {
		return d, e
	}
	connection, e := restoreConnections(p, model)
	if e != nil || d.RestoreTemplate == nil || d.InitializationPolicy == nil || d.BackupTemplate.SourceScopeID != l.marker.ScopeID || d.BackupTemplate.SourceManifestSHA256 != l.marker.ManifestSHA256 || source.ReleaseManifestSHA256 != l.marker.ManifestSHA256 || d.BackupTemplate.TenantID != source.NativeRequest.TenantID || d.BackupTemplate.Database != connection.database || d.BackupTemplate.Operator != connection.operator || source.NativeRequest.Database == connection.database || d.BackupTemplate.ControlImageSHA256 != strings.TrimPrefix(model.Services["control"].Image, "sha256:") || d.BackupTemplate.PostgresImageSHA256 != strings.TrimPrefix(model.Services["postgres"].Image, "sha256:") || d.BackupTemplate.ControlImageSHA256 != source.NativeRequest.ControlImageSHA256 || d.BackupTemplate.PostgresImageSHA256 != source.NativeRequest.PostgresImageSHA256 {
		return d, bad
	}
	if _, e = projectMVPNativeConfiguration(p, model, d); e != nil {
		return d, e
	}
	return d, nil
}

// Actual named-volume owners come from the original preparation journal. Their
// configured private children must also be the runtime's exact writable mounts.
func (l *locked) mvpNativeRestoreDomainMounts(model compose.Model, d configuration.NativePostgreSQLRecoveryDescriptor) ([4]engine.NativePostgreSQLStoreMount, engine.NativePostgreSQLRegistryMount, error) {
	var stores [4]engine.NativePostgreSQLStoreMount
	var registry engine.NativePostgreSQLRegistryMount
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_DOMAIN_OWNER_CHANGED")
	uid, gid, e := roleUser(model, "control-config")
	if e != nil {
		return stores, registry, e
	}
	state := volumeState(l.events)
	roles := []string{"models", "datasets", "training", "preparations", engine.NativePostgreSQLRegistryRole}
	for i, role := range roles {
		volume, ok := state.roles[role]
		if !ok || volume.Phase != phasePrepared || volume.UID != uid || volume.GID != gid {
			return stores, registry, bad
		}
		target := d.RegistryRoot
		if i < 4 {
			target = d.StoreRoots[i]
		}
		matches := 0
		for _, m := range model.Services["control"].Mounts {
			if m.Source == role {
				if m.Type != "volume" || m.ReadOnly || path.Join(m.Target, "private") != target {
					return stores, registry, bad
				}
				matches++
			}
		}
		if matches != 1 {
			return stores, registry, bad
		}
		if i < 4 {
			stores[i] = engine.NativePostgreSQLStoreMount{Owner: l.volumeOwner(volume), Target: target}
		} else {
			registry = engine.NativePostgreSQLRegistryMount{Owner: l.volumeOwner(volume), Target: target}
		}
	}
	return stores, registry, nil
}
