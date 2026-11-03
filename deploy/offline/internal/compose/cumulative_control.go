package compose

import (
	"encoding/json"
	"path"
	"path/filepath"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// CumulativeControlModel changes only the admitted Control image, its unique
// private configuration volume and the fixed read-only public view mount.
func CumulativeControlModel(original Model, generation, image, scopeRoot string) (Model, error) {
	return cumulativeControlModel(original, generation, image, scopeRoot, false)
}

// MVPControlModel retains the exact original M17 projection mount and numeric
// runtime identity while replacing its current Control/configuration only.
func MVPControlModel(original Model, generation, image, scopeRoot string) (Model, error) {
	if !mvpForwardMountClosure(original, scopeRoot) {
		return Model{}, failure("OFFLINE_COMPOSE_MVP_MOUNT_CLOSURE_INVALID")
	}
	return cumulativeControlModel(original, generation, image, scopeRoot, true)
}

func mvpForwardMountClosure(model Model, scopeRoot string) bool {
	control, ok := model.Services["control"]
	if !ok || !numericUser(control.User) {
		return false
	}
	declared := map[string]bool{}
	for _, v := range model.Volumes {
		if declared[v] {
			return false
		}
		declared[v] = true
	}
	for role, s := range model.Services {
		for i, m := range s.Mounts {
			if !containerTarget(m.Target) {
				return false
			}
			if m.Type == "bind" {
				if role != "control" || m.Source != filepath.Join(scopeRoot, view.RelativeDirectory) || m.Target != view.ContainerDirectory || !m.ReadOnly {
					return false
				}
			} else if m.Type != "volume" || !declared[m.Source] {
				return false
			}
			for j := 0; j < i; j++ {
				other := s.Mounts[j].Target
				if other == m.Target || strings.HasPrefix(other, m.Target+"/") || strings.HasPrefix(m.Target, other+"/") {
					return false
				}
			}
			for _, tmp := range s.Tmpfs {
				if path.Clean(tmp.Target) != tmp.Target || tmp.Target == m.Target || strings.HasPrefix(tmp.Target, m.Target+"/") || strings.HasPrefix(m.Target, tmp.Target+"/") {
					return false
				}
			}
		}
	}
	return true
}

func cumulativeControlModel(original Model, generation, image, scopeRoot string, retainedView bool) (Model, error) {
	bad := func() (Model, error) { return Model{}, failure("OFFLINE_COMPOSE_CUMULATIVE_CONTROL_MODEL_INVALID") }
	if !scopeIDPattern.MatchString(generation) || !immutableImage(image) || !filepath.IsAbs(scopeRoot) || filepath.Clean(scopeRoot) != scopeRoot {
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
	role := engine.CumulativeControlRole + "-" + generation
	declared, mounted, views := 0, 0, 0
	for i, v := range model.Volumes {
		if v == role {
			return bad()
		}
		if v == "control-config" {
			declared++
			model.Volumes[i] = role
		}
	}
	for name, s := range model.Services {
		for i, m := range s.Mounts {
			if m.Target == view.ContainerDirectory {
				if !retainedView || name != "control" || m.Type != "bind" || m.Source != filepath.Join(scopeRoot, view.RelativeDirectory) || !m.ReadOnly {
					return bad()
				}
				views++
			}
			if m.Source != "control-config" {
				continue
			}
			if name != "control" || m.Type != "volume" || !m.ReadOnly || !containerTarget(m.Target) {
				return bad()
			}
			mounted++
			s.Mounts[i].Source = role
		}
		if name == "control" {
			s.Image = image
			if !retainedView {
				s.Mounts = append(s.Mounts, Mount{Type: "bind", Source: filepath.Join(scopeRoot, view.RelativeDirectory), Target: view.ContainerDirectory, ReadOnly: true})
			}
		}
		model.Services[name] = s
	}
	if declared != 1 || mounted != 1 || retainedView && views != 1 {
		return bad()
	}
	return model, nil
}
