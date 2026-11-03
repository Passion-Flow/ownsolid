package scope

import (
	"os"
	"path"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func validMVPNativeReleasePolicyBinding(p mvpNativeBackupPlan) bool {
	b := p.ReleaseViewBinding
	return p.NativeUID > 0 && p.NativeUID <= 1<<31-1 && b != nil && b.Validate() == nil && b.Directory == view.ContainerDirectory && b.ScopeID == p.ScopeID && b.ConfigurationSHA256 == p.ConfigurationSHA256 && b.ManifestSHA256 == mvpNativePolicySourceManifest(p) && b.PublisherUID != p.NativeUID && (p.Forward == nil || b.PublisherUID == p.Forward.Original.OwnerUID)
}

// This is the original publisher's policy, never target authority or a legacy
// BackupPlan. Both source and target reuse the existing exact payload parser.
func mvpNativeSourceReleasePolicy(p mvpNativeBackupPlan, files map[string][]engine.ConfigurationFile) (map[string]bool, view.Configuration, error) {
	bad := failure("OFFLINE_SCOPE_NATIVE_SOURCE_RELEASE_POLICY_INVALID")
	if p.Profile != wire.MVPPostgreSQLCPUProfile || !validMVPNativeReleasePolicyBinding(p) {
		return nil, view.Configuration{}, bad
	}
	keys := map[string]bool{}
	operatorName := ""
	for _, item := range []struct{ role, name string }{{"control-config", "runtime.yaml"}, {"operator-config", "runtime.yaml"}, {"operator-config", "control.yaml"}} {
		if !mvpNativeRetainedPolicyFile(p, files, item.role, item.name) {
			return nil, view.Configuration{}, bad
		}
		name, policy, e := releasePolicyPayloadForProfile(files[item.role], item.name, p.Profile)
		if e != nil || !reflect.DeepEqual(policy, *p.ReleaseViewBinding) || !mvpNativeRetainedPolicyFile(p, files, item.role, name) {
			return nil, view.Configuration{}, bad
		}
		if item.role == "operator-config" {
			if operatorName != "" && operatorName != name {
				return nil, view.Configuration{}, bad
			}
			operatorName = name
		}
		keys[item.role+"/"+name] = true
	}
	return keys, *p.ReleaseViewBinding, nil
}

func mvpNativeRetainedPolicyFile(p mvpNativeBackupPlan, files map[string][]engine.ConfigurationFile, role, name string) bool {
	var original []byte
	count := 0
	for _, f := range files[role] {
		if f.Name == name {
			count++
			original = f.Contents
		}
	}
	if count != 1 {
		return false
	}
	count = 0
	for _, f := range p.ConfigurationFiles {
		if f.Role == role && f.File.Name == name {
			count++
			if f.File.Bytes != int64(len(original)) || f.File.SHA256 != wire.Digest(original) {
				return false
			}
		}
	}
	return count == 1
}

func (l *locked) bindMVPNativeBackupReleasePolicy(p *mvpNativeBackupPlan, files map[string][]engine.ConfigurationFile) error {
	if p == nil || p.Profile != wire.MVPPostgreSQLCPUProfile || !l.usesMVPNativeBackup() {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_RELEASE_POLICY_BINDING_CHANGED")
	}
	_, policy, e := releasePolicyPayloadForProfile(files["control-config"], "runtime.yaml", p.Profile)
	if e != nil || policy.ScopeID != l.marker.ScopeID || policy.ManifestSHA256 != l.marker.ManifestSHA256 || policy.ConfigurationSHA256 != l.marker.ConfigurationSHA256 || policy.PublisherUID != l.marker.OwnerUID || policy.PublisherUID == p.NativeUID {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_RELEASE_POLICY_BINDING_CHANGED")
	}
	p.ReleaseViewBinding = &policy
	_, _, e = mvpNativeSourceReleasePolicy(*p, files)
	return e
}

// Reads only the three fixed configuration envelopes and their specifically
// referenced original policy bytes. It does not read credentials or publish.
func readMVPNativeReleasePolicy(root *os.Root, c configuration.Config, p mvpNativeBackupPlan) error {
	files := map[string][]engine.ConfigurationFile{}
	for _, item := range []struct{ role, name string }{{"control-config", "runtime.yaml"}, {"operator-config", "runtime.yaml"}, {"operator-config", "control.yaml"}} {
		raw, e := regular(root, "configuration/"+item.role+"/"+item.name, c.Verification.MaximumMetadataBytes)
		if e != nil {
			return e
		}
		var envelope struct {
			Reference string `json:"release_view_config_file"`
		}
		if wire.UniqueJSON(raw, &envelope) != nil || !wire.SafePath(path.Base(envelope.Reference)) {
			return failure("OFFLINE_SCOPE_NATIVE_SOURCE_RELEASE_POLICY_INVALID")
		}
		present := false
		for _, f := range files[item.role] {
			if f.Name == item.name {
				present = true
			}
		}
		if !present {
			files[item.role] = append(files[item.role], engine.ConfigurationFile{Name: item.name, Contents: raw})
		}
		name := path.Base(envelope.Reference)
		present = false
		for _, f := range files[item.role] {
			if f.Name == name {
				present = true
			}
		}
		if !present {
			policy, e := regular(root, "configuration/"+item.role+"/"+name, c.Verification.MaximumMetadataBytes)
			if e != nil {
				return e
			}
			files[item.role] = append(files[item.role], engine.ConfigurationFile{Name: name, Contents: policy})
		}
	}
	_, _, e := mvpNativeSourceReleasePolicy(p, files)
	return e
}
