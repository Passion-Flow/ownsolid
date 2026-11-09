//go:build model_integration

package deployments_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

// This acceptance uses the installed route, real SQL and verified model bytes.
// Node hardware and execution results are the existing protocol fixtures.
func TestActualOnSiteDeploymentModelLicenseAdmission(t *testing.T) {
	ctx := t.Context()
	archivePath := os.Getenv("OWNSOLID_TEST_MODEL_LICENSE_ARCHIVE")
	if archivePath == "" {
		t.Fatal("an explicit archive of the approved model revision is required")
	}
	c := setupDeployment(t, false)
	c.s.Models.RootDirectory = t.TempDir()
	if err := os.Chmod(c.s.Models.RootDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	c.s.Nodes.Identity.Config.Models.ArtifactRoot = c.s.Models.RootDirectory
	c.model = importModelLicenseArchive(t, c, archivePath)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/deployments/openapi.json")
	newSite := func(use models.OnSiteUse) (*httptest.Server, *httpfixture.Client, *deployments.Service) {
		t.Helper()
		handler := transport.New(c.s.Nodes.Identity)
		handler.Deployments.Service.ModelUse = use
		server := httpfixture.NewTLSServer(t, handler)
		t.Cleanup(server.Close)
		client := httpfixture.New(t, server, "admin", schemas)
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
		return server, client, handler.Deployments.Service
	}

	other, err := c.s.Nodes.Workspaces.Create(ctx, c.a, "Other model license workspace", c.a.Me.User.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	token, err := c.s.Nodes.MintForProfile(ctx, c.a, other.ID, "Foreign license fixture node", wire.DockerInferenceProfile, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	handshake := c.h
	handshake.Inference = nil
	foreignNode, err := c.s.Nodes.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	path := "/api/v1/admin/deployments"
	server, client, _ := newSite(models.OnSiteCommercial)
	post := func(client *httpfixture.Client, path, key string, input any, status int, schema string) map[string]any {
		t.Helper()
		client.Headers = map[string]string{"Idempotency-Key": key}
		return client.Write("POST", path, input, status, schema)
	}
	input := c.input()
	foreign := input
	foreign.NodeID = foreignNode.NodeID
	if got := post(client, path, uuid.NewString(), foreign, 404, "Problem"); got["code"] != "RESOURCE_NOT_FOUND" {
		t.Fatal("scope refusal was replaced by a license decision", got)
	}
	noGPU := input
	noGPU.GPUUUID = "GPU-" + uuid.NewString()
	if got := post(client, path, uuid.NewString(), noGPU, 409, "Problem"); got["code"] != "INFERENCE_GPU_UNAVAILABLE" {
		t.Fatal("runtime refusal was replaced by a license decision", got)
	}
	key := uuid.NewString()
	created := post(client, path, key, input, 202, "DeploymentResponse")
	deployment := created["id"].(string)
	if created["catalog_digest"] != wire.ModelCatalogDigest() || created["model_id"] != c.model {
		t.Fatal("deployment did not bind the imported version", created)
	}
	c.complete(t, c.claim(t), "running")
	server.Close()

	// Recompose the HTTP service over the same installed database with the site
	// purpose undeclared. The existing deployment remains visible and stoppable.
	_, client, service := newSite(models.OnSiteUndeclared)
	if _, err := service.Create(ctx, c.a, input, uuid.NewString(), uuid.NewString()); err != models.ErrModelLicenseUseUndeclared {
		t.Fatal("model owner did not preserve its refusal reason", err)
	}
	if got := post(client, path, uuid.NewString(), input, 403, "Problem"); got["code"] != "ACCESS_DENIED" {
		t.Fatal("license refusal became a runtime error", got)
	}
	if got := client.Call("GET", path+"/placement?model_id="+c.model, nil, 403, "Problem"); got["code"] != "ACCESS_DENIED" {
		t.Fatal("placement recommended a refused version", got)
	}
	if got := post(client, path, key, input, 202, "DeploymentResponse"); got["id"] != deployment || got["revision"] != float64(1) {
		t.Fatal("committed replay changed across the site policy change", got)
	}
	client.Call("GET", path+"/"+deployment, nil, 200, "DeploymentResponse")
	action := deployments.ActionRequest{Action: "deploy", Revision: 1, Reason: "Apply current site license to a new load"}
	if got := post(client, path+"/"+deployment+"/actions", uuid.NewString(), action, 403, "Problem"); got["code"] != "ACCESS_DENIED" {
		t.Fatal("relaunch did not apply current policy", got)
	}
	var deploymentsCount, taskCount int
	if err := c.s.Nodes.DB.QueryRow(ctx, "SELECT count(*) FROM deployments WHERE model_id=$1", c.model).Scan(&deploymentsCount); err != nil || deploymentsCount != 1 {
		t.Fatalf("refusals or replay wrote deployments: %d, %v", deploymentsCount, err)
	}
	if err := c.s.Nodes.DB.QueryRow(ctx, "SELECT count(*) FROM node_tasks WHERE payload->>'model_id'=$1", c.model).Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatalf("refusals or replay wrote node tasks: %d, %v", taskCount, err)
	}
	action.Action = "stop"
	action.Reason = "Stop the retained deployment under the updated site policy"
	post(client, path+"/"+deployment+"/actions", uuid.NewString(), action, 202, "DeploymentResponse")
	c.complete(t, c.claim(t), "stopped")
	if got := client.Call("GET", path+"/"+deployment, nil, 200, "DeploymentResponse"); got["resource_state"] != "released" {
		t.Fatal("retained deployment could not release its GPU", got)
	}
}

func importModelLicenseArchive(t *testing.T, c scenario, path string) string {
	t.Helper()
	ctx := t.Context()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	job, err := c.s.Models.StartImport(ctx, c.a, models.ImportRequest{
		WorkspaceID: c.a.Me.Workspace.ID, Name: "Pinned model for on-site license acceptance",
		CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: info.Size(), LicenseAcknowledged: true,
		Reason: "Import the exact offline model for license admission",
	}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, models.ChunkBytes)
	for offset := int64(0); offset < info.Size(); {
		chunk := buffer[:min(int64(len(buffer)), info.Size()-offset)]
		if _, err := io.ReadFull(file, chunk); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(chunk)
		if _, err := c.s.Models.UploadChunk(ctx, c.a, job.ID, offset, int64(len(chunk)), hex.EncodeToString(sum[:]), bytes.NewReader(chunk)); err != nil {
			t.Fatal(err)
		}
		offset += int64(len(chunk))
	}
	if _, err := c.s.Models.QueueImport(ctx, c.a, job.ID, "Verify the pinned model archive", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if worked, err := c.s.Models.ProcessNext(ctx); err != nil || !worked {
		t.Fatalf("real model import worker: %t, %v", worked, err)
	}
	ready, err := c.s.Models.GetImport(ctx, c.a, job.ID)
	if err != nil || ready.State != "ready" || ready.ModelID == nil {
		t.Fatalf("verified import did not commit: %+v, %v", ready, err)
	}
	return *ready.ModelID
}
