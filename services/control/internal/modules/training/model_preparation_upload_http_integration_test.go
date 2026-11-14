//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func preparationHTTPHandler(g preparationScenario) *transport.Handler {
	h := transport.New(g.f.s.Identity)
	h.ModelPreparations = g.service
	h.Nodes.Service.Preparations = g.service
	h.Nodes.PreparationInputFile = g.service.InputFile
	h.Nodes.PreparationEvents = g.service.RecordEvents
	h.Nodes.BeginPreparationUpload = g.service.BeginUpload
	h.Nodes.PreparationChunk = g.service.UploadChunk
	h.Nodes.FinishPreparationUpload = g.service.FinishUpload
	h.Nodes.PreparationStop = g.service.RecordStop
	return h
}

func TestActualCpuModelPreparationTLSUploadSchemaBoundariesExactChunksAndNoIncompleteAcceptance(t *testing.T) {
	g, _, l, metadata := preparationOutputFixture(t)
	server := httpfixture.NewTLSServer(t, preparationHTTPHandler(g))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	machine := httpfixture.New(t, server, "admin", schemas)
	machine.Headers = map[string]string{"Authorization": "Bearer " + g.node.Credential}
	base := "/agent/v1/sites/default/cells/default/nodes/" + g.node.NodeID + "/tasks/" + l.TaskID + "/model-preparation-uploads"
	value := machine.Call("POST", base, map[string]any{"lease_token": l.LeaseToken, "metadata": json.RawMessage(metadata)}, 201, "ModelPreparationUploadResponse")
	upload := value["id"].(string)
	machine.Call("POST", base+"/"+upload+"/finish", map[string]any{"lease_token": l.LeaseToken}, 409, "Problem")
	body, e := os.ReadFile(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "model", "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	request, _ := http.NewRequest("POST", httpfixture.AdminOrigin+base+"/"+upload+"/chunks", bytes.NewReader(body))
	for name, value := range map[string]string{"Authorization": "Bearer " + g.node.Credential, "Content-Type": "application/octet-stream", "X-Model-Preparation-Lease-Token": l.LeaseToken, "X-Model-Preparation-Asset": "config.json", "X-Model-Preparation-Offset": "0", "X-Model-Preparation-SHA256": digest(body)} {
		request.Header.Set(name, value)
	}
	response, e := server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(response.Body)
	response.Body.Close()
	var envelope map[string]any
	if e != nil || response.StatusCode != 200 || json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("actual TLS model chunk", response.StatusCode, e)
	}
	if e = schemas["ModelPreparationChunkAcceptanceResponse"].Validate(envelope); e != nil {
		t.Fatal("chunk response schema", e)
	}
	data := envelope["data"].(map[string]any)
	if data["received_bytes"] != float64(len(body)) || data["sha256"] != digest(body) {
		t.Fatal("TLS exact byte/hash accounting")
	}
	request, _ = http.NewRequest("POST", httpfixture.AdminOrigin+base+"/"+upload+"/chunks", bytes.NewReader(body))
	for name, value := range map[string]string{"Authorization": "Bearer " + g.node.Credential, "Content-Type": "application/octet-stream", "X-Model-Preparation-Lease-Token": l.LeaseToken, "X-Model-Preparation-Asset": "config.json", "X-Model-Preparation-Offset": "00", "X-Model-Preparation-SHA256": digest(body)} {
		request.Header.Set(name, value)
	}
	response, e = server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || response.StatusCode != 422 {
		t.Fatal("noncanonical offset accepted", response.StatusCode, e)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
		t.Fatal("missing private transfer headers")
	}
	if response.ContentLength > 0 && response.Header.Get("Content-Length") != strconv.FormatInt(response.ContentLength, 10) {
		t.Fatal("inconsistent response length")
	}
}

func TestActualCpuModelPreparationTLSRestrictedStopExactSchemaAndReplay(t *testing.T) {
	g := newPreparationScenario(t)
	preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	receipt := preparationStopReceipt(t, g, l)
	server := httpfixture.NewTLSServer(t, preparationHTTPHandler(g))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	machine := httpfixture.New(t, server, "admin", schemas)
	machine.Headers = map[string]string{"Authorization": "Bearer " + g.node.Credential}
	path := "/agent/v1/sites/default/cells/default/nodes/" + g.node.NodeID + "/tasks/" + l.TaskID + "/model-preparation-stops"
	value := machine.Call("POST", path, wire.StopSubmission{LeaseToken: l.LeaseToken, Receipt: receipt}, 200, "ModelPreparationStopAcceptanceResponse")
	if value["state"] != "failed" || value["gpu_released"] != true || value["duplicate"] != false {
		t.Fatal("TLS restricted cleanup state")
	}
	value = machine.Call("POST", path, wire.StopSubmission{LeaseToken: l.LeaseToken, Receipt: receipt}, 200, "ModelPreparationStopAcceptanceResponse")
	if value["duplicate"] != true {
		t.Fatal("TLS exact cleanup replay")
	}
}
