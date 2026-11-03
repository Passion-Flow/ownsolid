package scope

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

type mvpNativeCurrentRelease struct{ ReleaseID, Profile, ManifestSHA256, ArchiveSHA256, PreparedRoot string }

// The original marker is immutable. A forward source is selected only from a
// completed, validated installed release, never by relabelling its old schema.
func (l *locked) mvpNativeCurrentSource(ctx context.Context) (mvpNativeCurrentRelease, error) {
	if l.marker.Profile == wire.MVPPostgreSQLCPUProfile && cumulativeUpgradeState(l.events).record == nil {
		return mvpNativeCurrentRelease{l.marker.ReleaseID, l.marker.Profile, l.marker.ManifestSHA256, l.marker.ArchiveSHA256, filepath.Join(l.config.ScopeRoot, "release")}, nil
	}
	current, e := l.cumulativeMVPCurrentSource(ctx)
	if e != nil {
		return mvpNativeCurrentRelease{}, e
	}
	return mvpNativeCurrentRelease{current.Installed.ReleaseID, current.Installed.Profile, current.ManifestSHA256, current.ArchiveSHA256, current.PreparedRoot}, nil
}

func (l *locked) usesMVPNativeBackup() bool {
	if l.marker.Profile == wire.MVPPostgreSQLCPUProfile {
		return true
	}
	s := cumulativeUpgradeState(l.events)
	return s.record != nil && s.record.Installed != nil && s.record.Installed.Profile == wire.MVPPostgreSQLCPUProfile
}

func (l *locked) mvpNativeBackupRegistryOwner(ctx context.Context, uid, gid int, registryRoot string) (engine.Ownership, error) {
	if cumulativeUpgradeState(l.events).record != nil {
		current, e := l.cumulativeMVPCurrentSource(ctx)
		if e != nil {
			return engine.Ownership{}, e
		}
		if current.UID != uid || current.GID != gid || current.RegistryTarget+"/private" != registryRoot || current.RegistryOwner.ScopeID != l.marker.ScopeID || current.RegistryOwner.Role != engine.NativePostgreSQLRegistryRole {
			return engine.Ownership{}, failure("OFFLINE_SCOPE_NATIVE_BACKUP_OWNED_REGISTRY_REQUIRED")
		}
		return current.RegistryOwner, nil
	}
	if restoreState(l.events).reserved || dependencyAdmission(l.events) != nil {
		original, e := l.readMVPNativeRestoredBackupOriginal(ctx)
		if e != nil {
			return engine.Ownership{}, e
		}
		if !validMVPNativeRestoredOriginal(original, l.marker.ScopeID) || original.Writer.UID != uid || original.Writer.GID != gid {
			return engine.Ownership{}, failure("OFFLINE_SCOPE_NATIVE_BACKUP_OWNED_REGISTRY_REQUIRED")
		}
		return original.Writer.NativePostgreSQLRecovery.Registry.Owner, nil
	}
	volume, ok := volumeState(l.events).roles[engine.NativePostgreSQLRegistryRole]
	if !ok || volume.Phase != phasePrepared || volume.UID != uid || volume.GID != gid {
		return engine.Ownership{}, failure("OFFLINE_SCOPE_NATIVE_BACKUP_OWNED_REGISTRY_REQUIRED")
	}
	return l.volumeOwner(volume), nil
}

func (l *locked) mvpNativeSourceRelease(ctx context.Context) ([]byte, []byte, error) {
	current, e := l.mvpNativeCurrentSource(ctx)
	if e != nil {
		return nil, nil, e
	}
	if _, e = bundle.VerifyPrepared(ctx, l.config.Verification, current.PreparedRoot, current.ArchiveSHA256, current.ManifestSHA256); e != nil {
		return nil, nil, e
	}
	root, e := os.OpenRoot(current.PreparedRoot)
	if e != nil {
		return nil, nil, e
	}
	defer root.Close()
	raw, e := regular(root, "manifest.json", l.config.Verification.MaximumManifestBytes)
	if e != nil {
		return nil, nil, e
	}
	signature, e := regular(root, "signature.json", l.config.Verification.MaximumMetadataBytes)
	return raw, signature, e
}

func (l *locked) validateMVPNativeCurrentPayload(ctx context.Context, v verifiedRuntimeContext) error {
	if cumulativeUpgradeState(l.events).record == nil {
		return nil
	}
	current, e := l.cumulativeMVPCurrentSource(ctx)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(current.Model, v.model) || !reflect.DeepEqual(current.Files["control-config"], v.inputs.files["control-config"]) || !reflect.DeepEqual(current.Files["operator-config"], v.inputs.files["operator-config"]) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_CURRENT_INSTALLED_CHANGED")
	}
	return nil
}

// Original view publisher identity remains bound to the original installation;
// Installed names the completed new release independently.
type mvpNativeForwardBackupBinding struct {
	Original                   Marker                `json:"original"`
	Installed                  view.InstalledRelease `json:"installed"`
	PayloadIndexSHA256         string                `json:"payload_index_sha256"`
	RuntimeConfigurationSHA256 string                `json:"runtime_configuration_sha256"`
	RegistryOwner              engine.Ownership      `json:"registry_owner"`
}

var mvpNativeSourceCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func validMVPNativeForwardBinding(p mvpNativeBackupPlan) bool {
	if p.Forward == nil {
		return true
	}
	b := p.Forward
	o, i := b.Original, b.Installed
	return o.ScopeID == p.ScopeID && o.Profile == wire.CumulativeCPUProfile && o.ConfigurationSHA256 == p.ConfigurationSHA256 && wire.ValidSHA256(o.ManifestSHA256) && wire.ValidSHA256(o.ArchiveSHA256) && o.ManifestSHA256 != p.ReleaseManifestSHA256 &&
		i.Profile == wire.MVPPostgreSQLCPUProfile && i.SchemaVersion == wire.MVPPostgreSQLCPUV1SchemaVersion && i.ReleaseID == p.ReleaseID && i.ManifestSHA256 == p.ReleaseManifestSHA256 && i.ArchiveSHA256 == p.ReleaseArchiveSHA256 && i.ActivationSequence > 0 && mvpNativeSourceCommitPattern.MatchString(i.SourceCommit) &&
		wire.ValidSHA256(b.PayloadIndexSHA256) && wire.ValidSHA256(b.RuntimeConfigurationSHA256) && b.RegistryOwner.ScopeID == p.ScopeID && b.RegistryOwner.Role == engine.NativePostgreSQLRegistryRole && identityPattern.MatchString(b.RegistryOwner.OperationID)
}

func mvpNativePolicySourceManifest(p mvpNativeBackupPlan) string {
	if p.Forward != nil {
		return p.Forward.Original.ManifestSHA256
	}
	return p.ReleaseManifestSHA256
}

func (l *locked) bindMVPNativeForwardBackup(ctx context.Context, p *mvpNativeBackupPlan) error {
	if cumulativeUpgradeState(l.events).record == nil {
		return nil
	}
	current, e := l.cumulativeMVPCurrentSource(ctx)
	if e != nil {
		return e
	}
	p.Forward = &mvpNativeForwardBackupBinding{current.Original, current.Installed, current.PayloadIndexSHA256, current.RuntimeConfigurationSHA256, current.RegistryOwner}
	if !validMVPNativeForwardBinding(*p) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_CURRENT_INSTALLED_CHANGED")
	}
	return nil
}
