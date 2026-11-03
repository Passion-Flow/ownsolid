package scope

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func taskLocalizationFixture() (*locked, engine.DatabaseTask, preparedInputs, compose.Model) {
	scope := strings.Repeat("a", 32)
	role := VolumeRecord{Key: engine.LocalizationConfigurationRole, Phase: phasePrepared, UID: 65532, GID: 65532, ResourceOperationID: strings.Repeat("b", 32)}
	l := &locked{marker: Marker{ScopeID: scope}, events: []Event{{Action: "volume_prepare", Outcome: "completed", Volume: &role}}}
	files := []engine.ConfigurationFile{{Name: "inventory.json", Contents: []byte("synthetic")}}
	config := []byte(`{"localization":{"package_directory":"/var/lib/projectone/locales/private"}}`)
	p := preparedInputs{files: map[string][]engine.ConfigurationFile{engine.LocalizationConfigurationRole: files, "control-config": {{Name: "runtime.yaml", Contents: config}}, "operator-config": {{Name: "control.yaml", Contents: config}}}}
	m := compose.Model{Services: map[string]compose.Service{"control": {Mounts: []compose.Mount{{Type: "volume", Source: engine.LocalizationConfigurationRole, Target: engine.LocalizationTaskDestination, ReadOnly: true}}}}}
	return l, engine.DatabaseTask{Owner: engine.Ownership{ScopeID: scope}, UID: 65532, GID: 65532}, p, m
}

func TestFixedTaskBindsOnlyPreparedMatchingReadonlyLocalization(t *testing.T) {
	for _, fault := range []string{"valid", "legacy", "pending", "uid", "empty", "rw", "target", "directory", "duplicate"} {
		t.Run(fault, func(t *testing.T) {
			l, task, p, model := taskLocalizationFixture()
			svc := model.Services["control"]
			switch fault {
			case "legacy":
				l.events = nil
			case "pending":
				l.events[0].Volume.Phase = phasePayloadVerified
			case "uid":
				task.UID++
			case "empty":
				p.files[engine.LocalizationConfigurationRole] = nil
			case "rw":
				svc.Mounts[0].ReadOnly = false
			case "target":
				svc.Mounts[0].Target = "/different"
			case "directory":
				p.files["operator-config"][0].Contents = []byte(`{"localization":{"package_directory":"/different/private"}}`)
			case "duplicate":
				svc.Mounts = append(svc.Mounts, svc.Mounts[0])
			}
			model.Services["control"] = svc
			err := l.bindTaskLocalization(&task, p, model)
			if (err == nil) != (fault == "valid" || fault == "legacy") {
				t.Fatal(fault, err)
			}
			if fault == "valid" && (task.LocalizationOwner == nil || task.LocalizationOwner.ScopeID != l.marker.ScopeID || task.LocalizationOwner.Role != engine.LocalizationConfigurationRole) {
				t.Fatal("prepared owner not bound")
			}
			if fault == "legacy" && task.LocalizationOwner != nil {
				t.Fatal("historical operation silently changed")
			}
		})
	}
}
func TestRecordedBackupLocalizationPreservesOnlyExactOriginalPlanHash(t *testing.T) {
	owner := engine.Ownership{ScopeID: strings.Repeat("a", 32), OperationID: strings.Repeat("b", 32), Role: engine.LocalizationConfigurationRole}
	p := BackupPlan{Jobs: map[string]BackupJob{"models": {Store: &engine.DatabaseTask{Kind: engine.ModelFilesExport, LocalizationOwner: &owner}}}}
	legacy := p
	legacy.Jobs = map[string]BackupJob{"models": {Store: &engine.DatabaseTask{Kind: engine.ModelFilesExport}}}
	legacySHA := wire.Digest(encode(legacy))
	got := recordedBackupLocalization(p, legacySHA)
	if wire.Digest(encode(got)) != legacySHA || p.Jobs["models"].Store.LocalizationOwner == nil {
		t.Fatal("original binding or source plan mutated")
	}
	newSHA := wire.Digest(encode(p))
	if wire.Digest(encode(recordedBackupLocalization(p, newSHA))) != newSHA {
		t.Fatal("new binding discarded")
	}
	if wire.Digest(encode(recordedBackupLocalization(p, strings.Repeat("f", 64)))) != newSHA {
		t.Fatal("unbound original plan accepted")
	}
	raw, _ := json.Marshal(got)
	var read BackupPlan
	if json.Unmarshal(raw, &read) != nil || wire.Digest(encode(read)) != legacySHA {
		t.Fatal("legacy roundtrip changed")
	}
}
