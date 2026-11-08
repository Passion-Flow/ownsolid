package v1

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExplicitInferenceAuthorityAndLegacySeparation(t *testing.T) {
	h := Handshake{ProtocolVersion: InferenceVersion, ExecutionProfile: DockerInferenceProfile, Capabilities: Capabilities(DockerInferenceProfile), Facts: validFacts()}
	if e := h.Validate(); e != nil {
		t.Fatal(e)
	}
	if len(h.Capabilities) != 8 || OperationAllowed(DockerInferenceProfile, ComponentOperation("install")) {
		t.Fatal("inference inherited component authority")
	}
	for _, profile := range []string{ReadOnlyProfile, SystemdComponentsProfile, SystemdMaintenanceProfile} {
		if OperationAllowed(profile, InferenceOperation("deploy")) {
			t.Fatal("legacy profile gained inference authority")
		}
		changed := h
		changed.ExecutionProfile = profile
		if changed.Validate() == nil {
			t.Fatal("mismatched inference handshake accepted")
		}
	}
	h.Component = &ComponentObservation{}
	if h.Validate() == nil {
		t.Fatal("component observation accepted in inference profile")
	}
	raw, e := os.ReadFile("schema.json")
	if e != nil || !strings.Contains(string(raw), ModelCatalogDigest()) {
		t.Fatal("generated catalog digest drift", e)
	}
}
func TestInferenceResultsRejectFalseReadinessAndUnboundIdentity(t *testing.T) {
	c := InferenceCommand{Action: "deploy", DeploymentID: "12345678-1234-1234-1234-123456789abc", ModelID: "22345678-1234-1234-1234-123456789abc", CatalogDigest: ModelCatalogDigest(), GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", Revision: 1}
	o := InferenceObservation{GPUs: []NVIDIAGPU{}, ObservedAt: time.Now().UTC(), Deployment: &DeploymentObservation{DeploymentID: c.DeploymentID, ModelID: c.ModelID, CatalogDigest: c.CatalogDigest, GPUUUID: c.GPUUUID, Revision: 1, Managed: true, State: "running", Ready: true, ContainerID: strings.Repeat("a", 64), ImageID: ApprovedInferenceRecipe().ImageConfigDigest, Verification: &GenerationVerification{Model: InferenceModelName, PromptSHA256: InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: time.Now().UTC()}}}
	good := Result{Outcome: "success", Inference: &o}
	if e := good.ValidateInference(c); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Result){
		func(r *Result) { r.Inference.Deployment.Verification = nil },
		func(r *Result) { r.Inference.Deployment.Revision++ },
		func(r *Result) { r.Inference.Deployment.ModelID = "32345678-1234-1234-1234-123456789abc" },
		func(r *Result) { r.Inference.ProbeErrorCode = "INFERENCE_NVIDIA_UNAVAILABLE" },
		func(r *Result) { r.Component = &ComponentObservation{} },
		func(r *Result) { r.Inference.Deployment.ImageID = "sha256:" + strings.Repeat("0", 64) },
	} {
		raw, _ := json.Marshal(good)
		var bad Result
		json.Unmarshal(raw, &bad)
		change(&bad)
		if bad.ValidateInference(c) == nil {
			t.Fatal("unsafe inference result accepted")
		}
	}
	stop := c
	stop.Action = "stop"
	o.Deployment.State = "stopped"
	o.Deployment.Ready = false
	o.ProbeErrorCode = "INFERENCE_NVIDIA_UNAVAILABLE"
	if e := good.ValidateInference(stop); e != nil {
		t.Fatal("GPU probe failure prevented confirmed owned stop", e)
	}
}
func TestModelTransferRangeRejectsUnapprovedPathsAndBounds(t *testing.T) {
	f, _ := ModelFileForName("config.json")
	good := ModelFileRequest{File: f.Path, Length: f.SizeBytes}
	if good.Validate() != nil {
		t.Fatal("approved small file rejected")
	}
	for _, bad := range []ModelFileRequest{{File: "../config.json", Length: 1}, {File: "/config.json", Length: 1}, {File: f.Path, Length: f.SizeBytes + 1}, {File: f.Path, Offset: 1, Length: 1}, {File: "model.safetensors", Length: ModelTransferChunkBytes + 1}} {
		if bad.Validate() == nil {
			t.Fatal("unbounded or arbitrary model request accepted")
		}
	}
}
