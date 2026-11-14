package v1

import (
	"encoding/json"
	"testing"
)

func TestFailedWorkerResultCannotCarryPreparedArtifactsOrForeignAttempt(t *testing.T) {
	command := validCommand(t)
	request, e := command.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	failure := FailedResult{SchemaVersion: 1, WorkerVersion: WorkerVersion, Status: "failed", PreparationID: request.PreparationID, AttemptID: request.AttemptID, ErrorCode: "MODEL_PREPARATION_PROCESSOR_TIMEOUT"}
	raw, _ := json.Marshal(failure)
	if _, e := DecodeFailedResult(raw, request); e != nil {
		t.Fatal(e)
	}
	mutations := map[string]func(map[string]json.RawMessage){
		"artifact": func(v map[string]json.RawMessage) { v["model_files"] = json.RawMessage("[]") },
		"unknown_code": func(v map[string]json.RawMessage) {
			v["error_code"] = json.RawMessage(`"MODEL_PREPARATION_INVENTED_ERROR"`)
		},
		"foreign_attempt": func(v map[string]json.RawMessage) {
			v["attempt_id"] = json.RawMessage(`"22222222-2222-4222-8222-222222222222"`)
		},
		"false_cancel": func(v map[string]json.RawMessage) { v["status"] = json.RawMessage(`"cancelled"`) },
		"null":         func(v map[string]json.RawMessage) { v["error_code"] = json.RawMessage("null") },
		"missing":      func(v map[string]json.RawMessage) { delete(v, "attempt_id") },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			var values map[string]json.RawMessage
			json.Unmarshal(raw, &values)
			change(values)
			body, _ := json.Marshal(values)
			if _, e := DecodeFailedResult(body, request); e == nil {
				t.Fatal("invalid failure accepted")
			}
		})
	}
	failure.Status = "cancelled"
	failure.ErrorCode = "MODEL_PREPARATION_CANCELLED"
	raw, _ = json.Marshal(failure)
	if _, e := DecodeFailedResult(raw, request); e != nil {
		t.Fatal("real cancellation rejected", e)
	}
}
func TestProcessorPathsRejectAsciiControls(t *testing.T) {
	command := validCommand(t)
	request, e := command.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	request.Configuration.Device = "cpu_verification"
	request.Identity.Profile = "native_cpu_verification"
	request.Identity.ImageID = nil
	for _, value := range []string{"/model\x00other", "/model\nother", "/model\x7fother"} {
		request.ModelDirectory = value
		if request.Validate() == nil {
			t.Fatal("unsafe path", value)
		}
	}
}
