package scope

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestRestoredCurrentStoresInspectionBindsCurrentSQLAndOriginalOutputsWithoutEffects(t *testing.T) {
	c, _, d, w := liveDatabaseFixture(t)
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32)); e == nil {
		t.Fatal("missing live SQL proof admitted")
	}
	l.close()
	if _, e = runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w); e != nil {
		t.Fatal(e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	before := encode(l.events)
	counts := []int{d.effects.creates, d.effects.puts, d.effects.removes, w.databaseStarts, w.creates}
	p, files, e := l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32))
	if e != nil {
		t.Fatal(e)
	}
	proof, _ := liveDatabaseProof(l.events)
	original, e := l.readRestoreStoresPlan()
	if e != nil {
		t.Fatal(e)
	}
	if p.DatabaseProofSHA256 != proof || p.DatabasePlanSHA256 != liveDatabaseState(l.events).record.PlanSHA256 || p.SelectedStoresPlanSHA256 != wire.Digest(encode(original)) || p.PayloadSHA256 != restoreConfigurationDigest(files) || len(p.Tasks) != 2 || p.PostgresPID != 123 {
		t.Fatal("lineage", p)
	}
	for i, kind := range []string{engine.ModelFilesVerify, engine.DatasetFilesVerify} {
		task := p.Tasks[i]
		if task.Kind != kind || task.ValidateRestoredStoreTask() != nil || task.Owner.OperationID != p.GenerationID || task.VolumeOwner.OperationID != p.GenerationID || task.Peers["control"] != d.newID || !reflect.DeepEqual(task.OutputOwner, original.Jobs[i].Verify.OutputOwner) || task.StoreRestore != nil || task.SourceOwner != nil || task.Reason != "" {
			t.Fatal("fixed current verifier", task)
		}
	}
	if !bytes.Equal(before, encode(l.events)) || !reflect.DeepEqual(counts, []int{d.effects.creates, d.effects.puts, d.effects.removes, w.databaseStarts, w.creates}) {
		t.Fatal("inspection changed state")
	}
	verified, e := l.verifyRuntimeContext(context.Background(), d, w, false)
	if e != nil {
		t.Fatal(e)
	}
	current, _, e := l.makeRestoredLiveDatabasePlan(context.Background(), d, w, strings.Repeat("9", 32))
	if e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"missing-store", "wrong-kind", "wrong-output", "wrong-path", "missing-mount", "read-only-mount"} {
		t.Run(fault, func(t *testing.T) {
			var stores RestoreStorePlan
			var model compose.Model
			if wire.StrictJSON(encode(original), &stores) != nil || wire.StrictJSON(encode(verified.model), &model) != nil {
				t.Fatal("clone")
			}
			switch fault {
			case "missing-store":
				stores.Jobs = stores.Jobs[:1]
			case "wrong-kind":
				stores.Jobs[0].Verify.Kind = engine.ModelFilesImport
			case "wrong-output":
				stores.Jobs[0].Verify.OutputOwner.Role = "datasets"
			default:
				service := model.Services["control"]
				for i, mount := range service.Mounts {
					if mount.Source == "models" {
						switch fault {
						case "wrong-path":
							service.Mounts[i].Target = "/wrong"
						case "missing-mount":
							service.Mounts[i].Source = "other"
						case "read-only-mount":
							service.Mounts[i].ReadOnly = true
						}
					}
				}
				model.Services["control"] = service
			}
			if _, e := assembleCurrentStoreTasks(l, stores, current, model); e == nil {
				t.Fatal("invalid profile admitted")
			}
		})
	}
}

func TestRestoredCurrentStoresInspectionRejectsRestartStoppedAndMissingCurrentPKI(t *testing.T) {
	c, request, d, w := liveDatabaseFixture(t)
	if _, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	before := encode(l.events)
	d.process = func(r *engine.RuntimeContainer) { r.State.PID = 456 }
	if _, _, e = l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32)); e == nil {
		t.Fatal("historical SQL accepted after restart")
	}
	d.process = nil
	saved := w.state.Services["postgres"]
	stopped := saved
	stopped.State = "exited"
	w.state.Services["postgres"] = stopped
	if _, _, e = l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32)); e == nil {
		t.Fatal("stopped dependency admitted")
	}
	w.state.Services["postgres"] = saved
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	if _, _, e = l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32)); e == nil {
		t.Fatal("absent current key admitted")
	}
	if !bytes.Equal(before, encode(l.events)) {
		t.Fatal("failed inspection changed history")
	}
}

func TestRestoredCurrentStoresProfileDoesNotHideEnabledUnselectedRuntimes(t *testing.T) {
	for _, raw := range []string{`{"training":{"artifact_root":"/var/lib/training"}}`, `{"model_preparation":{"artifact_root":"/var/lib/preparations"}}`, `{"training":{"artifact_root":false}}`, `{"training":{},"training":{}}`} {
		if currentSelectedStoreProfile([]engine.ConfigurationFile{{Name: "runtime.yaml", Contents: []byte(raw)}}) == nil {
			t.Fatal("unsupported/invalid runtime admitted")
		}
	}
	if e := currentSelectedStoreProfile([]engine.ConfigurationFile{{Name: "runtime.yaml", Contents: []byte(`{"training":{},"model_preparation":{}}`)}}); e != nil {
		t.Fatal("empty runtime profile", e)
	}
}
