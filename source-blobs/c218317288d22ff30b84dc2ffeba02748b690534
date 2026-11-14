//go:build training_artifact_integration

package training_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func TestActualApprovedCpuAdapterMetadataShapeAndEveryF32Value(t *testing.T) {
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	base := os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	if output == "" || base == "" {
		t.Fatal("actual approved CPU evidence required; run tools/quality/training/core.py --profile approved-cli before this profile")
	}
	requestBytes, e := os.ReadFile(filepath.Join(filepath.Dir(output), "request.json"))
	if e != nil {
		t.Fatal(e)
	}
	request, e := wire.DecodeRequest(requestBytes)
	if e != nil {
		t.Fatal(e)
	}
	resultBytes, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	result, e := wire.DecodeResult(resultBytes, request, agentwire.ApprovedModel().Repository)
	if e != nil {
		t.Fatal("actual CPU output contract", e)
	}
	configBytes, e := os.ReadFile(filepath.Join(base, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	expected, _ := agentwire.ModelFileForName("config.json")
	if digest(configBytes) != expected.SHA256 {
		t.Fatal("frozen base config digest")
	}
	var shape training.BaseShape
	if json.Unmarshal(configBytes, &shape) != nil {
		t.Fatal("frozen base shape")
	}
	for _, name := range []string{"adapter_config.json", "adapter_model.safetensors"} {
		var expectedSHA string
		for _, asset := range result.Files {
			if asset.Path == "adapter/"+name {
				expectedSHA = asset.SHA256
				break
			}
		}
		raw, e := os.ReadFile(filepath.Join(output, "adapter", name))
		if e != nil || digest(raw) != expectedSHA {
			t.Fatal("actual worker inventory bytes", e)
		}
	}
	metadata, e := os.ReadFile(filepath.Join(output, "adapter/adapter_config.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = wire.ValidateAdapterConfiguration(metadata, request.Configuration, agentwire.ApprovedModel().Repository); e != nil {
		t.Fatal("actual CPU PEFT metadata", e)
	}
	file, e := os.Open(filepath.Join(output, "adapter/adapter_model.safetensors"))
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if e = training.VerifyAdapterTensors(context.Background(), file, shape, request.Configuration, request.Resources.MaximumOutputBytes); e != nil {
		t.Fatal("actual112 finite F32 LoRA matrices", e)
	}
}
