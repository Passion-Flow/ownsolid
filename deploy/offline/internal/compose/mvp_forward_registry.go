package compose

import (
	"encoding/json"
	"path"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"strings"
)

// The only new data mount is this fixed native registry role. No host path or
// caller-controlled role is accepted; all earlier mounts stay byte-equivalent.
func MVPRegistryModel(original Model, target string) (Model, error) {
	bad := func() (Model, error) { return Model{}, failure("OFFLINE_COMPOSE_MVP_REGISTRY_MOUNT_INVALID") }
	if !containerTarget(target) {
		return bad()
	}
	raw, e := json.Marshal(original)
	if e != nil {
		return bad()
	}
	model, e := DecodeModel(raw, int64(len(raw)))
	if e != nil {
		return bad()
	}
	for _, role := range model.Volumes {
		if role == engine.NativePostgreSQLRegistryRole {
			return bad()
		}
	}
	s, ok := model.Services["control"]
	if !ok || !numericUser(s.User) {
		return bad()
	}
	for _, m := range s.Mounts {
		if m.Target == target || strings.HasPrefix(m.Target, target+"/") || strings.HasPrefix(target, m.Target+"/") {
			return bad()
		}
	}
	for _, m := range s.Tmpfs {
		if m.Target == target || strings.HasPrefix(m.Target, target+"/") || strings.HasPrefix(target, m.Target+"/") {
			return bad()
		}
	}
	model.Volumes = append(model.Volumes, engine.NativePostgreSQLRegistryRole)
	s.Mounts = append(s.Mounts, Mount{Type: "volume", Source: engine.NativePostgreSQLRegistryRole, Target: target, ReadOnly: false})
	model.Services["control"] = s
	if _, e := MVPRegistryTarget(model); e != nil {
		return bad()
	}
	return model, nil
}

func MVPRegistryTarget(model Model) (string, error) {
	bad := failure("OFFLINE_COMPOSE_MVP_REGISTRY_MOUNT_INVALID")
	declared := 0
	for _, r := range model.Volumes {
		if r == engine.NativePostgreSQLRegistryRole {
			declared++
		}
	}
	target := ""
	for role, s := range model.Services {
		for _, m := range s.Mounts {
			if m.Source != engine.NativePostgreSQLRegistryRole {
				continue
			}
			if role != "control" || m.Type != "volume" || m.ReadOnly || !containerTarget(m.Target) || target != "" {
				return "", bad
			}
			target = m.Target
		}
	}
	if declared != 1 || target == "" {
		return "", bad
	}
	for _, m := range model.Services["control"].Mounts {
		if m.Source == engine.NativePostgreSQLRegistryRole {
			continue
		}
		if m.Source == MVPRegistryReleaseRole && m.Type == "volume" && m.ReadOnly && target == path.Join(m.Target, "store") {
			continue
		}
		if m.Target == target || strings.HasPrefix(target, m.Target+"/") || strings.HasPrefix(m.Target, target+"/") {
			return "", bad
		}
	}
	for _, m := range model.Services["control"].Tmpfs {
		if m.Target == target || strings.HasPrefix(target, m.Target+"/") || strings.HasPrefix(m.Target, target+"/") {
			return "", bad
		}
	}
	return target, nil
}

// MVPRegistryReleaseRole is an independent immutable signed-artifact owner.
const MVPRegistryReleaseRole = engine.RegistryPreparedReleaseRole

func MVPRegistryReleaseModel(original Model, releaseTarget string) (Model, error) {
	bad := failure("OFFLINE_COMPOSE_MVP_REGISTRY_RELEASE_INVALID")
	store, e := MVPRegistryTarget(original)
	if !numericUser(original.Services["control"].User) || e != nil || !containerTarget(releaseTarget) || store != path.Join(releaseTarget, "store") {
		return Model{}, bad
	}
	raw, e := json.Marshal(original)
	if e != nil {
		return Model{}, bad
	}
	model, e := DecodeModel(raw, int64(len(raw)))
	if e != nil {
		return Model{}, bad
	}
	for _, v := range model.Volumes {
		if v == MVPRegistryReleaseRole {
			return Model{}, bad
		}
	}
	control := model.Services["control"]
	for _, m := range control.Mounts {
		if m.Source == engine.NativePostgreSQLRegistryRole {
			continue
		}
		if relatedRegistryPath(m.Target, releaseTarget) {
			return Model{}, bad
		}
	}
	for _, m := range control.Tmpfs {
		if relatedRegistryPath(m.Target, releaseTarget) {
			return Model{}, bad
		}
	}
	model.Volumes = append(model.Volumes, MVPRegistryReleaseRole)
	control.Mounts = append(control.Mounts, Mount{Type: "volume", Source: MVPRegistryReleaseRole, Target: releaseTarget, ReadOnly: true})
	model.Services["control"] = control
	if _, e = MVPRegistryReleaseTarget(model); e != nil {
		return Model{}, e
	}
	return model, nil
}
func relatedRegistryPath(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func MVPRegistryReleaseTarget(model Model) (string, error) {
	bad := failure("OFFLINE_COMPOSE_MVP_REGISTRY_RELEASE_INVALID")
	store, e := MVPRegistryTarget(model)
	if e != nil {
		return "", e
	}
	target := ""
	declared := 0
	for _, v := range model.Volumes {
		if v == MVPRegistryReleaseRole {
			declared++
		}
	}
	for role, s := range model.Services {
		for _, m := range s.Mounts {
			if m.Source != MVPRegistryReleaseRole {
				continue
			}
			if role != "control" || m.Type != "volume" || !m.ReadOnly || target != "" || !containerTarget(m.Target) || store != path.Join(m.Target, "store") {
				return "", bad
			}
			target = m.Target
		}
	}
	if declared != 1 || target == "" {
		return "", bad
	}
	for _, m := range model.Services["control"].Mounts {
		if m.Source == MVPRegistryReleaseRole || m.Source == engine.NativePostgreSQLRegistryRole {
			continue
		}
		if relatedRegistryPath(target, m.Target) {
			return "", bad
		}
	}
	for _, m := range model.Services["control"].Tmpfs {
		if relatedRegistryPath(target, m.Target) {
			return "", bad
		}
	}
	return target, nil
}
