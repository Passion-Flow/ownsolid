//go:build model_integration

package modelartifact

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestActualApprovedModelStructuralIntegrity(t *testing.T) {
	directory := os.Getenv("PROJECT_ONE_MODEL_DIRECTORY")
	if directory == "" {
		t.Fatal("actual frozen model required for model_integration profile")
	}
	if e := VerifyDirectory(context.Background(), directory); e != nil {
		t.Fatal(e)
	}
	// Evidence contains only the approved public recipe identity, never tensor values.
	b, _ := json.Marshal(map[string]string{"status": "actual_model_files_verified", "qualification": "not_gpu_loaded"})
	t.Log(string(b))
}
