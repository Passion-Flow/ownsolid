package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type actualCase struct {
	Name        string           `json:"name"`
	Request     ProcessorRequest `json:"request"`
	Repository  string           `json:"repository"`
	ResultRaw   string           `json:"result_raw"`
	Checkpoints []struct {
		SHA256 string `json:"sha256"`
		Raw    string `json:"raw"`
	} `json:"checkpoint_receipts"`
	Events []string `json:"events"`
}

func actualCases(t *testing.T) []actualCase {
	t.Helper()
	raw, e := os.ReadFile("../../../tests/fixtures/training/actual-worker-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []actualCase `json:"cases"`
	}
	if e = json.Unmarshal(raw, &fixture); e != nil || len(fixture.Cases) != 3 {
		t.Fatal("actual source-bound worker fixture", e)
	}
	return fixture.Cases
}
func TestActualProtectedPythonResultCheckpointAndEventBytes(t *testing.T) {
	for _, fixture := range actualCases(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			result, e := DecodeResult([]byte(fixture.ResultRaw), fixture.Request, fixture.Repository)
			if e != nil || result.Status != fixture.Name {
				t.Fatal("actual worker result", e)
			}
			for _, checkpoint := range fixture.Checkpoints {
				digest := sha256.Sum256([]byte(checkpoint.Raw))
				if hex.EncodeToString(digest[:]) != checkpoint.SHA256 {
					t.Fatal("actual receipt bytes changed")
				}
				if _, e := DecodeCheckpoint([]byte(checkpoint.Raw), fixture.Request, fixture.Repository); e != nil {
					t.Fatal("actual checkpoint compatibility", e)
				}
			}
			var last int64
			for index, raw := range fixture.Events {
				event, e := DecodeEvent([]byte(raw), fixture.Request.JobID, fixture.Request.AttemptID, fixture.Request.Configuration.MaximumSteps)
				if e != nil || event.Sequence != int64(index+1) || event.GlobalStep < last {
					t.Fatal("actual ordered worker event", e)
				}
				last = event.GlobalStep
			}
			if fixture.Name != "failed" && result.EventCount != int64(len(fixture.Events)) {
				t.Fatal("actual event count")
			}
			foreign := fixture.Request
			foreign.WorkspaceID = "00000000-0000-4000-8000-000000000099"
			if fixture.Name != "failed" {
				if _, e = DecodeResult([]byte(fixture.ResultRaw), foreign, fixture.Repository); e == nil {
					t.Fatal("foreign Workspace output accepted")
				}
			}
		})
	}
}
func mutateJSON(t *testing.T, raw string, change func(map[string]any)) []byte {
	t.Helper()
	var value map[string]any
	if e := json.Unmarshal([]byte(raw), &value); e != nil {
		t.Fatal(e)
	}
	change(value)
	out, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestActualWorkerResultRejectsConflictingScopeInventoriesAndUnexpectedFields(t *testing.T) {
	f := actualCases(t)[0]
	for name, change := range map[string]func(map[string]any){
		"foreign_attempt":      func(v map[string]any) { v["attempt_id"] = "00000000-0000-4000-8000-000000000099" },
		"missing_dataset":      func(v map[string]any) { delete(v, "dataset_version_id") },
		"unknown":              func(v map[string]any) { v["source_text"] = "MUST_NOT_BE_ACCEPTED" },
		"null_loss":            func(v map[string]any) { v["training_loss"] = nil },
		"false_adapter_update": func(v map[string]any) { v["adapter_changed"] = false },
		"unfrozen_base":        func(v map[string]any) { v["base_parameters_frozen"] = false },
		"partial_steps":        func(v map[string]any) { v["global_step"] = 0 },
		"unexpected_parent":    func(v map[string]any) { v["parent_checkpoint_receipt_sha256"] = strings.Repeat("a", 64) },
		"empty_rows":           func(v map[string]any) { v["train_rows"] = 0 },
		"wrong_device":         func(v map[string]any) { v["device_profile"] = ProductionProfile },
		"wrong_memory":         func(v map[string]any) { v["memory_enforcement"] = "simulated" },
		"foreign_binding": func(v map[string]any) {
			v["bindings"].(map[string]any)["scope"].(map[string]any)["workspace_id"] = "00000000-0000-4000-8000-000000000099"
		},
		"package_drift": func(v map[string]any) {
			v["bindings"].(map[string]any)["packages"].(map[string]any)["torch"] = "unverified"
		},
		"input_drift":              func(v map[string]any) { v["bindings"].(map[string]any)["train_sha256"] = strings.Repeat("a", 64) },
		"unknown_checkpoint_asset": func(v map[string]any) { v["files"].([]any)[0].(map[string]any)["path"] = "arbitrary.py" },
		"path_traversal": func(v map[string]any) {
			v["files"].([]any)[0].(map[string]any)["path"] = "../adapter/adapter_config.json"
		},
		"duplicate_asset": func(v map[string]any) { files := v["files"].([]any); v["files"] = append(files, files[0]) },
		"null_metric_value": func(v map[string]any) {
			v["metrics"].([]any)[0].(map[string]any)["values"].(map[string]any)["loss"] = nil
		},
		"extra_metric_value": func(v map[string]any) {
			v["metrics"].([]any)[0].(map[string]any)["values"].(map[string]any)["raw_prompt"] = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeResult(mutateJSON(t, f.ResultRaw, change), f.Request, f.Repository); e == nil {
				t.Fatal("conflicting actual result accepted")
			}
		})
	}
	for _, bad := range []string{strings.Replace(f.ResultRaw, `"global_step":1`, `"Global_step":1`, 1), strings.Replace(f.ResultRaw, `"training_loss":`, `"training_loss":1e400,"duplicate_loss":`, 1), `{"status":"trained",` + f.ResultRaw[1:]} {
		if _, e := DecodeResult([]byte(bad), f.Request, f.Repository); e == nil {
			t.Fatal("invalid JSON/keys accepted")
		}
	}
}
func TestActualCancellationCheckpointMayResumeAtOffIntervalStep(t *testing.T) {
	f := actualCases(t)[1]
	found := false
	for _, saved := range f.Checkpoints {
		receipt, e := DecodeCheckpoint([]byte(saved.Raw), f.Request, f.Repository)
		if e != nil {
			t.Fatal(e)
		}
		if receipt.GlobalStep%f.Request.Configuration.CheckpointSteps == 0 {
			continue
		}
		found = true
		files := append([]datawire.Asset{}, receipt.Files...)
		files = append(files, datawire.Asset{Path: "projectone-checkpoint.json", SizeBytes: int64(len(saved.Raw)), SHA256: saved.SHA256})
		resume := ResumeInput{CheckpointID: "00000000-0000-4000-8000-000000000099", GlobalStep: receipt.GlobalStep, ReceiptSHA256: saved.SHA256, Files: files}
		if e = resume.Validate(f.Request.Configuration); e != nil {
			t.Fatal("real safe cancellation step was incorrectly restricted to save interval", e)
		}
	}
	if !found {
		t.Fatal("actual fixture lacks observed off-interval cancellation checkpoint")
	}
}
func TestActualEventsRejectNullUnknownCrossScopeAndInvalidNumericValues(t *testing.T) {
	f := actualCases(t)[0]
	raw := f.Events[2]
	for name, change := range map[string]func(map[string]any){"null_value": func(v map[string]any) { v["values"].(map[string]any)["loss"] = nil }, "negative_gradient": func(v map[string]any) { v["values"].(map[string]any)["grad_norm"] = -1 }, "unwanted_null": func(v map[string]any) { v["phase"] = nil }, "foreign_job": func(v map[string]any) { v["job_id"] = "00000000-0000-4000-8000-000000000099" }, "zero_sequence": func(v map[string]any) { v["sequence"] = 0 }, "future_step": func(v map[string]any) { v["global_step"] = 1001 }} {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeEvent(mutateJSON(t, raw, change), f.Request.JobID, f.Request.AttemptID, f.Request.Configuration.MaximumSteps); e == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
}
