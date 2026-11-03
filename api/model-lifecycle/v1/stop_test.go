package v1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validPreparationStop(t *testing.T) (Command, StopReceipt) {
	t.Helper()
	c := validCommand(t)
	sha, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	id := "22222222-2222-4222-8222-222222222222"
	return c, StopReceipt{SchemaVersion: 1, ReceiptID: id, JournalID: id, NodeID: id, TaskID: id, Scope: c.Scope, AttemptID: c.AttemptID, AttemptNumber: c.AttemptNumber, CommandSHA256: sha, ImageID: *c.Recipe.Identity.ImageID, GPUUUID: c.GPUUUID, ContainerName: ContainerName(c.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, AgentErrorCode: "MODEL_PREPARATION_AGENT_INTERRUPTED", CompletedAt: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)}
}

func TestPreparationPhysicalStopBindsEveryExactOwnedContainerField(t *testing.T) {
	c, r := validPreparationStop(t)
	if r.ValidateFor(c, r.NodeID, r.TaskID) != nil {
		t.Fatal("never created exact owner")
	}
	cid := strings.Repeat("a", 64)
	r.ContainerID = &cid
	r.Proof = "owned_container_removed"
	r.AgentErrorCode = ""
	if r.ValidateFor(c, r.NodeID, r.TaskID) != nil {
		t.Fatal("positive exact owned-container removal")
	}
	mutations := map[string]func(*StopReceipt){
		"scope":               func(r *StopReceipt) { r.Scope.PreparationID = r.NodeID },
		"attempt":             func(r *StopReceipt) { r.AttemptNumber++ },
		"command":             func(r *StopReceipt) { r.CommandSHA256 = strings.Repeat("b", 64) },
		"node":                func(r *StopReceipt) { r.NodeID = c.AttemptID },
		"task":                func(r *StopReceipt) { r.TaskID = c.AttemptID },
		"image":               func(r *StopReceipt) { r.ImageID = "sha256:" + strings.Repeat("a", 64) },
		"gpu":                 func(r *StopReceipt) { r.GPUUUID = "GPU-" + r.NodeID },
		"training_owner":      func(r *StopReceipt) { r.ContainerName = "projectone-training-" + c.AttemptID },
		"not_absent":          func(r *StopReceipt) { r.OwnedContainerAbsent = false },
		"missing_id":          func(r *StopReceipt) { r.ContainerID = nil },
		"false_never_created": func(r *StopReceipt) { r.Proof = "never_created" },
		"unknown_proof":       func(r *StopReceipt) { r.Proof = "timeout_elapsed" },
		"untrusted_error":     func(r *StopReceipt) { r.AgentErrorCode = "customer/private/path" },
		"invalid_outcome":     func(r *StopReceipt) { bad := "invalid"; r.OutcomeSHA256 = &bad },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			bad := r
			mutate(&bad)
			if bad.ValidateFor(c, r.NodeID, r.TaskID) == nil {
				t.Fatal("foreign/unproven stop accepted")
			}
		})
	}
}

func TestPreparationStopEnvelopePreservesOutcomeBytesAndRejectsUntrustedShapes(t *testing.T) {
	_, r := validPreparationStop(t)
	raw, _ := json.Marshal(r)
	outcome := `{"schema_version":1,"worker_version":"0.1.0","status":"failed","preparation_id":"` + r.Scope.PreparationID + `","attempt_id":"` + r.AttemptID + `","error_code":"MODEL_PREPARATION_CANCELLED"}`
	valid := `{"lease_token":"` + strings.Repeat("a", 43) + `","receipt":` + string(raw) + `,"outcome":` + outcome + `}`
	out, e := DecodeStopSubmission([]byte(valid))
	if e != nil || string(out.Outcome) != outcome {
		t.Fatal("original result bytes", e)
	}
	for _, bad := range []string{strings.Replace(valid, `"lease_token":`, `"Lease_token":`, 1), strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1), strings.Replace(valid, `"outcome":`+outcome, `"outcome":"private-path"`, 1), strings.Replace(valid, `"lease_token":"`+strings.Repeat("a", 43)+`",`, "", 1), valid + "{}"} {
		if _, e := DecodeStopSubmission([]byte(bad)); e == nil {
			t.Fatal("invalid cleanup envelope accepted")
		}
	}
	valid = strings.Replace(valid, outcome, "null", 1)
	out, e = DecodeStopSubmission([]byte(valid))
	if e != nil || out.Outcome != nil {
		t.Fatal("explicit absent outcome", e)
	}
}
