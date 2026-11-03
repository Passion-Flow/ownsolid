package scope

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func cumulativeStoreConfiguration() []byte {
	return []byte(`{"server":{"application_profile":"releases"},"models":{"artifact_root":"/var/lib/projectone/models/private"},"datasets":{"artifact_root":"/var/lib/projectone/datasets/private"},"training":{"artifact_root":"/var/lib/projectone/training/private"},"model_preparation":{"artifact_root":"/var/lib/projectone/preparations/private"}}`)
}
func TestCumulativeCurrentStoreProfileBindsExactDeclaredRuntime(t *testing.T) {
	for _, fault := range []string{"valid", "predecessor", "cache", "legacy", "wrong-app", "one-active", "wrong-root", "missing-root", "duplicate"} {
		t.Run(fault, func(t *testing.T) {
			raw := string(cumulativeStoreConfiguration())
			profile := wire.CumulativeCPUProfile
			switch fault {
			case "predecessor":
				profile = wire.CumulativePredecessorCPUProfile
				raw = strings.Replace(raw, `"releases"`, `"localization"`, 1)
			case "cache":
				profile = wire.CumulativeCacheCPUProfile
				raw = strings.Replace(raw, `"releases"`, `"`+wire.CumulativeCacheApplicationProfile+`"`, 1)
			case "legacy":
				profile = "historical-two-store"
			case "wrong-app":
				raw = strings.Replace(raw, `"releases"`, `"platform"`, 1)
			case "one-active":
				raw = strings.Replace(raw, `"artifact_root":"/var/lib/projectone/preparations/private"`, `"artifact_root":""`, 1)
			case "wrong-root":
				raw = strings.Replace(raw, "/var/lib/projectone/training/private", "/unmatched/private", 1)
			case "missing-root":
				raw = strings.Replace(raw, `"artifact_root":"/var/lib/projectone/models/private"`, `"artifact_root":""`, 1)
			case "duplicate":
				raw = strings.Replace(raw, `"training":{`, `"training":{},"training":{`, 1)
			}
			err := currentSelectedStoreProfile([]engine.ConfigurationFile{{Name: "runtime.yaml", Contents: []byte(raw)}}, profile)
			if (err == nil) != (fault == "valid" || fault == "predecessor" || fault == "cache") {
				t.Fatal(fault, err)
			}
		})
	}
}

// Explicit task/role metadata fixture only; no file or GPU verification is
// promoted from this assembly test. Actual independent restore stays separate.
func cumulativeStoreTaskFixture() (*locked, RestoreStorePlan, RestoredLiveDatabasePlan, compose.Model) {
	scope := strings.Repeat("a", 32)
	old := strings.Repeat("b", 32)
	next := strings.Repeat("c", 32)
	l := &locked{marker: Marker{ScopeID: scope, Profile: wire.CumulativeCPUProfile}}
	base := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: scope, OperationID: next}, VolumeOwner: engine.Ownership{ScopeID: scope, OperationID: next, Role: engine.StoreRestoreConfigRole + "-" + next}, UID: 65532, GID: 65532, Image: "sha256:" + strings.Repeat("d", 64), NetworkID: strings.Repeat("e", 64), Peers: map[string]string{"postgres": strings.Repeat("f", 64), "control": strings.Repeat("1", 64)}}
	current := RestoredLiveDatabasePlan{GenerationID: next, Tasks: []engine.DatabaseTask{base, base}}
	stores := RestoreStorePlan{}
	service := compose.Service{}
	for i, kind := range restoredStoreCatalog() {
		role := VolumeRecord{Key: kind.role, Phase: phasePrepared, UID: 65532, GID: 65532, ResourceOperationID: fmt.Sprintf("%032x", i+2)}
		l.events = append(l.events, Event{Action: "volume_prepare", Outcome: "completed", Volume: &role})
		task := base
		task.Owner.OperationID = old
		task.Owner.Role = "database-" + kind.verifyKind
		task.Kind = kind.verifyKind
		task.VolumeOwner.OperationID = old
		task.VolumeOwner.Role = engine.StoreRestoreConfigRole + "-" + old
		owner := l.volumeOwner(role)
		task.OutputOwner = &owner
		if kind.verifyReason {
			task.Reason = "Synthetic retained restore reason"
		}
		stores.Jobs = append(stores.Jobs, RestoreStoreJob{Role: kind.role, Verify: task})
		service.Mounts = append(service.Mounts, compose.Mount{Type: "volume", Source: kind.role, Target: "/var/lib/projectone/" + kind.role})
	}
	return l, stores, current, compose.Model{Services: map[string]compose.Service{"control": service}}
}
func TestCumulativeCurrentStoreTasksKeepAllOwnersReasonsAndCurrentInputs(t *testing.T) {
	for _, fault := range []string{"valid", "missing-store", "legacy-profile", "wrong-output", "wrong-uid", "missing-mount", "readonly", "missing-reason", "locale-drift"} {
		t.Run(fault, func(t *testing.T) {
			l, stores, current, model := cumulativeStoreTaskFixture()
			service := model.Services["control"]
			switch fault {
			case "missing-store":
				stores.Jobs = stores.Jobs[:3]
			case "legacy-profile":
				l.marker.Profile = "historical-two-store"
			case "wrong-output":
				stores.Jobs[2].Verify.OutputOwner.OperationID = strings.Repeat("9", 32)
			case "wrong-uid":
				stores.Jobs[2].Verify.UID++
			case "missing-mount":
				service.Mounts = service.Mounts[:3]
			case "readonly":
				service.Mounts[3].ReadOnly = true
			case "missing-reason":
				stores.Jobs[2].Verify.Reason = ""
			case "locale-drift":
				stores.Jobs[3].Verify.LocalizationOwner = &engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: strings.Repeat("9", 32), Role: engine.LocalizationConfigurationRole}
			}
			model.Services["control"] = service
			tasks, err := assembleCurrentStoreTasks(l, stores, current, model)
			if (err == nil) != (fault == "valid") {
				t.Fatal(fault, err)
			}
			if fault == "valid" {
				if len(tasks) != 4 {
					t.Fatal("missing current store verifier")
				}
				for i, task := range tasks {
					if task.ValidateRestoredStoreTask() != nil || task.Owner.OperationID != current.GenerationID || !reflect.DeepEqual(task.OutputOwner, stores.Jobs[i].Verify.OutputOwner) || task.VolumeOwner != current.Tasks[0].VolumeOwner || task.Reason != stores.Jobs[i].Verify.Reason {
						t.Fatal("changed verifier authority", task.Kind)
					}
				}
			}
		})
	}
}
func TestCumulativeCurrentStoreProofRequiresEveryFourStoreStep(t *testing.T) {
	legacy := currentStoresSteps()
	steps := currentStoresSteps(4)
	if len(legacy) != 13 || len(steps) != 19 || currentStoresSteps(2) != nil || currentStoresSteps(3) != nil {
		t.Fatal("closed sequences changed")
	}
	r := RestoredCurrentStoresRecord{StoreCount: 4, GenerationID: strings.Repeat("a", 32), PlanSHA256: strings.Repeat("b", 64)}
	var events []Event
	for index := range steps {
		r.Step = index
		copy := r
		events = append(events, Event{Action: "current_stores_effect", Outcome: "completed", RestoredCurrentStores: &copy})
		proof, ok := currentStoresProof(events)
		if ok != (index == len(steps)-1) || (ok && proof == "") {
			t.Fatal("accepted a short four-store prefix", index)
		}
	}
	if currentStoresIdentity(r, RestoredCurrentStoresRecord{GenerationID: r.GenerationID, PlanSHA256: r.PlanSHA256, Step: r.Step}) {
		t.Fatal("store count drift admitted")
	}
	legacyRecord := r
	legacyRecord.StoreCount = 0
	raw, _ := json.Marshal(legacyRecord)
	if strings.Contains(string(raw), "store_count") {
		t.Fatal("historical encoding changed")
	}
}
