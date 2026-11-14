//go:build model_lifecycle_process_integration

package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func digestString(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func TestActualProtectedCpuResultIndependentGoSemantics(t *testing.T) {
	root := os.Getenv("PROJECT_ONE_MODEL_LIFECYCLE_PROCESS_ROOT")
	if root == "" {
		t.Fatal("explicit actual process evidence root required")
	}
	var catalog struct {
		Files []dataset.Asset `json:"files"`
	}
	raw, e := os.ReadFile("../../agent/v1/model-catalog.json")
	if e != nil || json.Unmarshal(raw, &catalog) != nil {
		t.Fatal("approved catalog", e)
	}
	for _, name := range []string{"approved-cli", "fresh-attempt-recovery", "explicit-lower-threshold-evaluation"} {
		t.Run(name, func(t *testing.T) {
			requests, e := filepath.Glob(filepath.Join(root, name, "*", "*", "*", "*", "request.json"))
			if e != nil || len(requests) != 1 {
				t.Fatal("actual request namespace", e)
			}
			requestBytes, e := os.ReadFile(requests[0])
			if e != nil {
				t.Fatal(e)
			}
			request, e := DecodeProcessorRequest(requestBytes)
			if e != nil {
				t.Fatal("actual request", e)
			}
			heldout, e := os.ReadFile(request.HeldoutFile)
			if e != nil || digestString(heldout) != request.HeldoutSHA256 {
				t.Fatal("actual heldout digest", e)
			}
			expected := ResultExpectation{Request: request, RequestBytes: requestBytes, BaseFiles: catalog.Files}
			for _, line := range bytes.Split(bytes.TrimSuffix(heldout, []byte("\n")), []byte("\n")) {
				row, e := dataset.DecodeRecord(line, request.DataLimits)
				if e != nil {
					t.Fatal(e)
				}
				expected.Cases = append(expected.Cases, ExpectedCase{SourceID: row.SourceID, PromptSHA256: digestString([]byte(row.Prompt)), Label: row.Completion})
			}
			templateBytes, e := os.ReadFile(filepath.Join(request.ModelDirectory, "tokenizer_config.json"))
			if e != nil {
				t.Fatal(e)
			}
			for _, asset := range catalog.Files {
				if asset.Path == "tokenizer_config.json" && digestString(templateBytes) != asset.SHA256 {
					t.Fatal("unverified template source")
				}
			}
			var tokenizer struct {
				ChatTemplate string `json:"chat_template"`
			}
			if json.Unmarshal(templateBytes, &tokenizer) != nil || tokenizer.ChatTemplate == "" {
				t.Fatal("template")
			}
			expected.ChatTemplateSHA256 = digestString([]byte(tokenizer.ChatTemplate))
			resultBytes, e := os.ReadFile(filepath.Join(request.OutputDirectory, "result.json"))
			if e != nil {
				t.Fatal(e)
			}
			result, e := DecodePreparedResult(resultBytes, expected)
			if e != nil {
				t.Fatal("actual report semantics", e)
			}
			if result.RowCount != 3 || len(result.ChangedTargets) != 56 {
				t.Fatal("actual result inventory")
			}
			if name == "approved-cli" && result.GatePassed {
				t.Fatal("default failed quality fabricated approval")
			}
			changes := map[string]func(map[string]json.RawMessage){
				"gate": func(v map[string]json.RawMessage) {
					v["gate_passed"] = json.RawMessage("true")
					if result.GatePassed {
						v["gate_passed"] = json.RawMessage("false")
					}
				},
				"foreign-attempt": func(v map[string]json.RawMessage) {
					v["attempt_id"] = json.RawMessage(`"22222222-2222-4222-8222-222222222222"`)
				},
				"request": func(v map[string]json.RawMessage) {
					v["request_sha256"] = json.RawMessage(`"0000000000000000000000000000000000000000000000000000000000000000"`)
				},
				"score": func(v map[string]json.RawMessage) {
					var score map[string]json.RawMessage
					json.Unmarshal(v["base"], &score)
					score["correct"] = json.RawMessage("3")
					v["base"], _ = json.Marshal(score)
				},
				"missing": func(v map[string]json.RawMessage) { delete(v, "instruction_sha256") },
				"unknown": func(v map[string]json.RawMessage) { v["unknown"] = json.RawMessage("true") },
				"null":    func(v map[string]json.RawMessage) { v["gate_passed"] = json.RawMessage("null") },
			}
			for key, change := range changes {
				var values map[string]json.RawMessage
				json.Unmarshal(resultBytes, &values)
				change(values)
				corrupt, _ := json.Marshal(values)
				if _, e := DecodePreparedResult(corrupt, expected); e == nil {
					t.Fatal("tampered report accepted", key)
				}
			}
		})
	}
}
