package scope

import (
	"os"
	"path"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func releasePolicyPayload(files []engine.ConfigurationFile, configuration string) (string, view.Configuration, error) {
	return releasePolicyPayloadForProfile(files, configuration, wire.CumulativeCPUProfile)
}

func hasRecordedReleasePolicy(profile string) bool {
	return profile == wire.CumulativeCPUProfile || profile == wire.CumulativeCacheCPUProfile
}

func releasePolicyPayloadForProfile(files []engine.ConfigurationFile, configuration, profile string) (string, view.Configuration, error) {
	var policy view.Configuration
	bad := failure("OFFLINE_SCOPE_RESTORE_RELEASE_POLICY_INVALID")
	application := "releases"
	if profile == wire.MVPPostgreSQLCPUProfile {
		application = ""
	} else if !hasRecordedReleasePolicy(profile) {
		return "", policy, bad
	}
	if profile == wire.CumulativeCacheCPUProfile {
		application = wire.CumulativeCacheApplicationProfile
	}
	raw, e := restoreNamedFile(files, configuration)
	if e != nil {
		return "", policy, bad
	}
	var cfg struct {
		Reference string `json:"release_view_config_file"`
		Server    struct {
			Profile string `json:"application_profile"`
		} `json:"server"`
	}
	if wire.UniqueJSON(raw, &cfg) != nil || cfg.Server.Profile != application || cfg.Reference == "" || strings.ContainsAny(cfg.Reference, "\\\x00\r\n") || path.Clean(cfg.Reference) != cfg.Reference || !strings.HasPrefix(cfg.Reference, "/run/projectone/") || !strings.HasSuffix(path.Dir(cfg.Reference), "/private") || !wire.SafePath(path.Base(cfg.Reference)) {
		return "", policy, bad
	}
	var members map[string]any
	if wire.UniqueJSON(raw, &members) != nil {
		return "", policy, bad
	}
	nameForAlias := path.Base(cfg.Reference)
	var aliases func(any) bool
	aliases = func(value any) bool {
		switch v := value.(type) {
		case string:
			return strings.HasPrefix(v, "/") && path.Base(v) == nameForAlias
		case map[string]any:
			for _, child := range v {
				if aliases(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if aliases(child) {
					return true
				}
			}
		}
		return false
	}
	delete(members, "release_view_config_file")
	if aliases(members) {
		return "", policy, bad
	}
	name := path.Base(cfg.Reference)
	raw, e = restoreNamedFile(files, name)
	if e != nil {
		return "", policy, bad
	}
	policy, e = view.ParseConfiguration(raw)
	if e != nil || policy.Directory != view.ContainerDirectory {
		return "", policy, bad
	}
	return name, policy, nil
}
func sourceReleasePolicy(p BackupPlan, files map[string][]engine.ConfigurationFile) (map[string]bool, error) {
	bad := failure("OFFLINE_SCOPE_RESTORE_RELEASE_POLICY_INVALID")
	if !hasRecordedReleasePolicy(p.Profile) {
		if p.ReleaseViewBinding != nil {
			return nil, bad
		}
		return map[string]bool{}, nil
	}
	if p.ReleaseViewBinding == nil || p.ReleaseViewBinding.Validate() != nil || p.ReleaseViewBinding.ScopeID != p.ScopeID || p.ReleaseViewBinding.ConfigurationSHA256 != p.ConfigurationSHA256 || p.ReleaseViewBinding.Directory != view.ContainerDirectory {
		return nil, bad
	}
	if job := p.Jobs["models"].Store; job == nil || job.UID == p.ReleaseViewBinding.PublisherUID {
		return nil, bad
	}
	keys := map[string]bool{}
	operatorName := ""
	for _, item := range []struct{ role, name string }{{"control-config", "runtime.yaml"}, {"operator-config", "runtime.yaml"}, {"operator-config", "control.yaml"}} {
		name, policy, e := releasePolicyPayloadForProfile(files[item.role], item.name, p.Profile)
		if e != nil || !reflect.DeepEqual(policy, *p.ReleaseViewBinding) {
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
	return keys, nil
}
func (l *locked) bindBackupReleasePolicy(p *BackupPlan, files map[string][]engine.ConfigurationFile) error {
	if !hasRecordedReleasePolicy(p.Profile) {
		return nil
	}
	if p.Profile != l.marker.Profile {
		return failure("OFFLINE_SCOPE_BACKUP_RELEASE_POLICY_BINDING_CHANGED")
	}
	_, policy, e := releasePolicyPayloadForProfile(files["control-config"], "runtime.yaml", p.Profile)
	if e != nil {
		return e
	}
	if policy.ScopeID != l.marker.ScopeID || policy.ManifestSHA256 != l.marker.ManifestSHA256 || policy.ConfigurationSHA256 != l.marker.ConfigurationSHA256 || policy.PublisherUID != l.marker.OwnerUID {
		return failure("OFFLINE_SCOPE_BACKUP_RELEASE_POLICY_BINDING_CHANGED")
	}
	p.ReleaseViewBinding = &policy
	_, e = sourceReleasePolicy(*p, files)
	return e
}
func restoredReleasePolicyFiles(p preparedInputs, model compose.Model, source BackupPlan, files map[string][]engine.ConfigurationFile, markers []Marker) (map[string]bool, error) {
	keys, e := sourceReleasePolicy(source, files)
	if e != nil {
		return nil, e
	}
	if len(keys) == 0 {
		return keys, nil
	}
	bad := failure("OFFLINE_SCOPE_RESTORE_RELEASE_POLICY_TARGET_MISMATCH")
	if len(markers) != 1 {
		return nil, bad
	}
	marker := markers[0]
	if marker.ScopeID == source.ScopeID || !hasRecordedReleasePolicy(marker.Profile) || marker.Profile != source.Profile || marker.ScopeID != p.descriptor.ScopeID || marker.ConfigurationSHA256 != p.descriptor.ConfigurationSHA256 {
		return nil, bad
	}
	uid, _, e := roleUser(model, "control-config")
	if e != nil {
		return nil, bad
	}
	for _, item := range []struct{ role, name string }{{"control-config", "runtime.yaml"}, {"operator-config", "runtime.yaml"}, {"operator-config", "control.yaml"}} {
		name, policy, e := releasePolicyPayloadForProfile(p.files[item.role], item.name, marker.Profile)
		if e != nil || !keys[item.role+"/"+name] || policy.ScopeID != marker.ScopeID || policy.ManifestSHA256 != marker.ManifestSHA256 || policy.ConfigurationSHA256 != marker.ConfigurationSHA256 || policy.PublisherUID != marker.OwnerUID || policy.PublisherUID == uid || policy.PageSize != source.ReleaseViewBinding.PageSize || policy.OperationSeconds != source.ReleaseViewBinding.OperationSeconds {
			return nil, bad
		}
	}
	return keys, nil
}

// Source read admission reads only the three fixed configuration documents and
// their specifically referenced policy files; private key/DSN bytes are not read.
func readBackupReleasePolicy(root *os.Root, p BackupPlan, maximum int64) error {
	if !hasRecordedReleasePolicy(p.Profile) {
		if p.ReleaseViewBinding != nil {
			return failure("OFFLINE_SCOPE_RESTORE_RELEASE_POLICY_INVALID")
		}
		return nil
	}
	files := map[string][]engine.ConfigurationFile{}
	for _, item := range []struct{ role, name string }{{"control-config", "runtime.yaml"}, {"operator-config", "runtime.yaml"}, {"operator-config", "control.yaml"}} {
		raw, e := regular(root, "configuration/"+item.role+"/"+item.name, maximum)
		if e != nil {
			return e
		}
		files[item.role] = append(files[item.role], engine.ConfigurationFile{Name: item.name, Contents: raw})
		var value struct {
			Reference string `json:"release_view_config_file"`
		}
		if wire.UniqueJSON(raw, &value) != nil || !wire.SafePath(path.Base(value.Reference)) {
			return failure("OFFLINE_SCOPE_RESTORE_RELEASE_POLICY_INVALID")
		}
		name := path.Base(value.Reference)
		present := false
		for _, f := range files[item.role] {
			present = present || f.Name == name
		}
		if !present {
			payload, e := regular(root, "configuration/"+item.role+"/"+name, maximum)
			if e != nil {
				return e
			}
			files[item.role] = append(files[item.role], engine.ConfigurationFile{Name: name, Contents: payload})
		}
	}
	_, e := sourceReleasePolicy(p, files)
	return e
}
