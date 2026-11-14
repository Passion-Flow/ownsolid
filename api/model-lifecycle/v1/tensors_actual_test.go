//go:build model_lifecycle_artifact_integration

package v1

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func TestActualApprovedCpuMergedWeightsIndependentGoVerification(t *testing.T) {
	basePath, candidatePath, reportPath := os.Getenv("PROJECT_ONE_MODEL_LIFECYCLE_BASE"), os.Getenv("PROJECT_ONE_MODEL_LIFECYCLE_CANDIDATE"), os.Getenv("PROJECT_ONE_MODEL_LIFECYCLE_REPORT")
	if basePath == "" || candidatePath == "" || reportPath == "" {
		t.Fatal("explicit actual approved CPU artifact paths required")
	}
	raw, e := os.ReadFile("../../agent/v1/model-catalog.json")
	if e != nil {
		t.Fatal(e)
	}
	var catalog struct {
		Files []dataset.Asset `json:"files"`
	}
	if json.Unmarshal(raw, &catalog) != nil {
		t.Fatal("canonical approved catalog")
	}
	var expected dataset.Asset
	for _, asset := range catalog.Files {
		if asset.Path == "model.safetensors" {
			expected = asset
		}
	}
	base, e := os.Open(basePath)
	if e != nil {
		t.Fatal(e)
	}
	defer base.Close()
	candidate, e := os.Open(candidatePath)
	if e != nil {
		t.Fatal(e)
	}
	defer candidate.Close()
	baseInfo, e := base.Stat()
	if e != nil {
		t.Fatal(e)
	}
	candidateInfo, e := candidate.Stat()
	if e != nil {
		t.Fatal(e)
	}
	reportBytes, e := os.ReadFile(reportPath)
	if e != nil {
		t.Fatal(e)
	}
	var report struct {
		Files   []dataset.Asset `json:"model_files"`
		Changed []string        `json:"changed_target_tensors"`
		Device  string          `json:"device_profile"`
	}
	if json.Unmarshal(reportBytes, &report) != nil || report.Device != "cpu_verification" {
		t.Fatal("actual CPU provenance required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, e := VerifyMergedWeights(ctx, base, baseInfo.Size(), expected, candidate, candidateInfo.Size(), true)
	if e != nil || !reflect.DeepEqual(result.ChangedTargets, report.Changed) || len(result.ChangedTargets) != 56 {
		t.Fatal("actual independent verification", e, len(result.ChangedTargets))
	}
	var accepted dataset.Asset
	for _, asset := range report.Files {
		if asset.Path == "model.safetensors" {
			accepted = asset
		}
	}
	if result.SHA256 != accepted.SHA256 || result.SizeBytes != accepted.SizeBytes {
		t.Fatal("actual bytes disagree with original recorded Python report")
	}
}
