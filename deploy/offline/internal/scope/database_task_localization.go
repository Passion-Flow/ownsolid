package scope

import (
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Carry the existing prepared, immutable role into a fixed operator task.
// No new locale payload is generated and no runtime validation is bypassed.
func (l *locked) bindTaskLocalization(t *engine.DatabaseTask, p preparedInputs, model compose.Model) error {
	role, found := volumeState(l.events).roles[engine.LocalizationConfigurationRole]
	if !found {
		return nil // Historical profiles use their original embedded locale registry.
	}
	bad := func() error { return failure("OFFLINE_SCOPE_DATABASE_LOCALIZATION_CONTEXT_INVALID") }
	if role.Phase != phasePrepared || role.UID != t.UID || role.GID != t.GID || t.Owner.ScopeID != l.marker.ScopeID || len(p.files[engine.LocalizationConfigurationRole]) == 0 {
		return bad()
	}
	mounts := 0
	for _, m := range model.Services["control"].Mounts {
		if m.Source == engine.LocalizationConfigurationRole || m.Target == engine.LocalizationTaskDestination {
			if m.Source != engine.LocalizationConfigurationRole || m.Target != engine.LocalizationTaskDestination || m.Type != "volume" || !m.ReadOnly {
				return bad()
			}
			mounts++
		}
	}
	if mounts != 1 {
		return bad()
	}
	for _, item := range []struct{ role, name string }{{"control-config", "runtime.yaml"}, {"operator-config", "control.yaml"}} {
		raw, err := restoreNamedFile(p.files[item.role], item.name)
		var value struct {
			Localization struct {
				Directory string `json:"package_directory"`
			} `json:"localization"`
		}
		if err != nil || wire.UniqueJSON(raw, &value) != nil || value.Localization.Directory != engine.LocalizationTaskDestination+"/private" {
			return bad()
		}
	}
	owner := l.volumeOwner(role)
	t.LocalizationOwner = &owner
	return nil
}
