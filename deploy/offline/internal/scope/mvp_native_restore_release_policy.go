package scope

import (
	"path/filepath"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// Called only after genuine native source/configuration admission and the
// original source-policy helper. The supplied policy/key set is that helper's
// original result, not a public shape-valid policy or a generation exemption.
// The target's already frozen private files supply its policy. This function
// never generates, rewrites or publishes a target configuration or journal.
func (l *locked) mvpNativeRestoreTargetReleasePolicy(p preparedInputs, model compose.Model, source mvpNativeBackupPlan, original view.Configuration, originalNames map[string]bool) (map[string]bool, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_TARGET_POLICY_CHANGED")
	m := l.marker
	if m.Profile != wire.MVPPostgreSQLCPUProfile || !validMVPNativeBackupPlan(source) || !validMVPNativeReleasePolicyBinding(source) || source.ReleaseViewBinding == nil || !reflect.DeepEqual(*source.ReleaseViewBinding, original) || source.Profile != m.Profile || source.ScopeID == m.ScopeID || source.ReleaseID != m.ReleaseID || source.ReleaseManifestSHA256 != m.ManifestSHA256 || original.Validate() != nil || original.Directory != view.ContainerDirectory || original.ScopeID != source.ScopeID || original.ManifestSHA256 != source.ReleaseManifestSHA256 || original.ConfigurationSHA256 != source.ConfigurationSHA256 || len(originalNames) != 2 || p.descriptor.ScopeID != m.ScopeID || p.descriptor.ConfigurationSHA256 != m.ConfigurationSHA256 {
		return nil, bad
	}
	uid, gid, e := roleUser(model, "control-config")
	if e != nil || uid == m.OwnerUID {
		return nil, bad
	}
	// These are actual frozen input-role identities, not a guessed image UID.
	roles := map[string]bool{}
	for _, role := range p.descriptor.Roles {
		if role.Key != "control-config" && role.Key != "operator-config" {
			continue
		}
		if roles[role.Key] || role.UID != uid || role.GID != gid {
			return nil, bad
		}
		roles[role.Key] = true
	}
	if len(roles) != 2 || !mvpNativeRestoreReadonlyReleaseView(model, l.config.ScopeRoot) {
		return nil, bad
	}
	wanted := original
	wanted.ScopeID, wanted.ManifestSHA256, wanted.ConfigurationSHA256 = m.ScopeID, m.ManifestSHA256, m.ConfigurationSHA256
	wanted.PublisherUID = m.OwnerUID
	if wanted.Validate() != nil {
		return nil, bad
	}
	keys := map[string]bool{}
	operatorName := ""
	for _, item := range []struct{ role, name string }{{"control-config", "runtime.yaml"}, {"operator-config", "runtime.yaml"}, {"operator-config", "control.yaml"}} {
		envelope, e := restoreNamedFile(p.files[item.role], item.name)
		if e != nil || !mvpNativeFrozenConfigurationFile(p, item.role, item.name, envelope) {
			return nil, bad
		}
		name, policy, e := releasePolicyPayloadForProfile(p.files[item.role], item.name, wire.MVPPostgreSQLCPUProfile)
		if e != nil || !originalNames[item.role+"/"+name] || !reflect.DeepEqual(policy, wanted) {
			return nil, bad
		}
		raw, e := restoreNamedFile(p.files[item.role], name)
		if e != nil || !mvpNativeFrozenConfigurationFile(p, item.role, name, raw) {
			return nil, bad
		}
		if item.role == "operator-config" {
			if operatorName != "" && operatorName != name {
				return nil, bad
			}
			operatorName = name
		}
		keys[item.role+"/"+name] = true
	}
	if !reflect.DeepEqual(keys, originalNames) {
		return nil, bad
	}
	return keys, nil
}

func mvpNativeFrozenConfigurationFile(p preparedInputs, role, name string, raw []byte) bool {
	count := 0
	for _, r := range p.descriptor.Roles {
		if r.Key != role {
			continue
		}
		for _, file := range r.Files {
			if file.Name == name {
				if file.Bytes != int64(len(raw)) || file.SHA256 != wire.Digest(raw) {
					return false
				}
				count++
			}
		}
	}
	return count == 1
}

// Preserve the existing Compose public projection boundary. Another service,
// writable mount, ancestor, alias target or duplicate can never carry authority.
func mvpNativeRestoreReadonlyReleaseView(model compose.Model, scopeRoot string) bool {
	if !filepath.IsAbs(scopeRoot) || filepath.Clean(scopeRoot) != scopeRoot {
		return false
	}
	wanted := filepath.Join(scopeRoot, view.RelativeDirectory)
	count := 0
	for name, service := range model.Services {
		for _, mount := range service.Mounts {
			if mount.Target != view.ContainerDirectory && mount.Source != wanted {
				continue
			}
			if name != "control" || mount.Type != "bind" || mount.Target != view.ContainerDirectory || mount.Source != wanted || !mount.ReadOnly {
				return false
			}
			count++
		}
	}
	return count == 1
}
