package browser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
)

// This test-only machine supplies synthetic GPU/results over real TLS. It executes no model or container.
func startSyntheticInferenceMachine(t *testing.T, server *httptest.Server, s *nodes.Service, a identity.Auth) (string, string, func()) {
	return startSyntheticInferenceMachineProfile(t, server, s, a, true)
}

func startSyntheticInferenceMachineProfile(t *testing.T, server *httptest.Server, s *nodes.Service, a identity.Auth, firstDeploymentFails bool) (string, string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-inference-browser", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	join, e := s.MintForProfile(ctx, a, a.Me.Workspace.ID, "Synthetic NVIDIA fixture", wire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrolled, e := s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	model := uuid.NewString()
	catalog, _ := json.Marshal(wire.ApprovedModel())
	if _, e = s.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic browser metadata <img>',$4,$5,$6,'ready',$7)", model, a.Me.Workspace.TenantID, a.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), catalog, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	client := server.Client()
	client.Timeout = 5 * time.Second
	origin, _ := url.Parse(s.Identity.Config.Server.AdminOrigin)
	post := func(path string, input, out any) bool {
		body, _ := json.Marshal(input)
		r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+path, bytes.NewReader(body))
		r.Host = origin.Host
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+enrolled.Credential)
		response, e := client.Do(r)
		if e != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return false
		}
		var envelope struct{ Data json.RawMessage }
		if json.NewDecoder(response.Body).Decode(&envelope) != nil {
			return false
		}
		return out == nil || json.Unmarshal(envelope.Data, out) == nil
	}
	observation := wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: "GPU-12345678-1234-1234-1234-123456789abc", Name: "Synthetic NVIDIA GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	base := "/agent/v1/nodes/" + enrolled.NodeID
	h.Inference = &observation
	if !post(base+"/heartbeat", h, nil) {
		t.Fatal("synthetic inference heartbeat failed")
	}
	done := make(chan struct{})
	var mu sync.Mutex
	failed := false
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		lastHeartbeat := time.Now()
		firstDeploy := firstDeploymentFails
		for {
			good := true
			if time.Since(lastHeartbeat) >= 10*time.Second {
				observation.ObservedAt = time.Now().UTC()
				h.Facts.ObservedAt = observation.ObservedAt
				good = post(base+"/heartbeat", h, nil)
				lastHeartbeat = time.Now()
			}
			var claim wire.Claim
			if good {
				good = post(base+"/tasks/claims", struct{}{}, &claim)
			}
			if good && claim.Lease != nil {
				lease := claim.Lease
				if lease.Inference == nil || lease.Inference.Validate(lease.Operation) != nil {
					good = false
				} else {
					command := lease.Inference
					observation.ErrorCode = ""
					observation.ObservedAt = time.Now().UTC()
					if command.DeploymentID != "" {
						d := &wire.DeploymentObservation{DeploymentID: command.DeploymentID, ModelID: command.ModelID, CatalogDigest: command.CatalogDigest, GPUUUID: command.GPUUUID, Revision: command.Revision, State: "unknown"}
						if observation.Deployment != nil && observation.Deployment.DeploymentID == command.DeploymentID {
							copy := *observation.Deployment
							d = &copy
							d.Revision = command.Revision
							d.ErrorCode = ""
						}
						switch command.Action {
						case "deploy", "verify":
							if command.Action == "deploy" && firstDeploy {
								firstDeploy = false
								d.State = "failed"
								d.Ready = false
								d.ErrorCode = "INFERENCE_GPU_OOM"
								observation.ErrorCode = d.ErrorCode
							} else {
								d.Managed = true
								d.State = "running"
								d.Ready = true
								d.ContainerID = strings.Repeat("a", 64)
								d.ImageID = wire.ApprovedInferenceRecipe().ImageManifestDigest
								d.Verification = &wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: time.Now().UTC()}
							}
						case "stop":
							d.State = "stopped"
							d.Ready = false
						case "remove":
							d.State = "not_installed"
							d.Ready = false
							d.Managed = false
							d.ContainerID = ""
							d.ImageID = ""
							d.Verification = nil
						}
						observation.Deployment = d
					}
					result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: &observation}
					if observation.ErrorCode != "" {
						result.Outcome = "failure"
						result.ErrorCode = observation.ErrorCode
					}
					good = result.ValidateInference(*command) == nil && post(base+"/tasks/"+lease.TaskID+"/results", result, nil)
				}
			}
			if !good {
				if ctx.Err() == nil {
					mu.Lock()
					failed = true
					mu.Unlock()
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return enrolled.NodeID, model, func() {
		cancel()
		<-done
		mu.Lock()
		defer mu.Unlock()
		if failed {
			t.Error("synthetic inference protocol fixture failed")
		}
	}
}
