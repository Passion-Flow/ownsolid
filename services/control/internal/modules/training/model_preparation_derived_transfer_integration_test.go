//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration

package training_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func actualDerivedTransfer(t *testing.T, g preparationScenario, catalog wire.DerivedCatalog) {
	t.Helper()
	ctx := context.Background()
	deploymentsBefore := count(t, g.f, "SELECT count(*) FROM deployments")
	leasesBefore := count(t, g.f, "SELECT count(*) FROM gpu_leases")
	sha, e := catalog.Digest()
	if e != nil {
		t.Fatal(e)
	}
	command := agentwire.InferenceCommand{Action: "deploy", DeploymentID: uuid.NewString(), ModelID: catalog.ModelID, CatalogDigest: sha, GPUUUID: g.input.GPUUUID, Revision: 1, Derived: &agentwire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: catalog, ServingRecipeSHA256: agentwire.DerivedServingRecipeDigest()}}
	// A synthetic node-task owning callback isolates actual TLS/file authorization.
	// No deployment/resource row, container, GPU claim or serving readiness is created.
	task, e := g.f.s.Nodes.CreateInferenceTaskBound(ctx, g.f.admin, g.node.NodeID, agentwire.DerivedInferenceOperation, uuid.NewString(), "Actual approved derived-file range verification only", uuid.NewString(), bytes.Repeat([]byte{1}, 32), func(pgx.Tx, nodes.Node) (agentwire.InferenceCommand, error) { return command, nil }, func(pgx.Tx, nodes.Node, nodes.Task, agentwire.InferenceCommand) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	claim, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	server := httpfixture.NewTLSServer(t, preparationHTTPHandler(g))
	defer server.Close()
	source, e := os.Open(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "model", "model.safetensors"))
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	asset, ok := command.ModelFile("model.safetensors")
	if !ok {
		t.Fatal("actual model asset")
	}
	last := (asset.SizeBytes - 1) / agentwire.ModelTransferChunkBytes * agentwire.ModelTransferChunkBytes
	for _, offset := range []int64{0, last} {
		length := min(agentwire.ModelTransferChunkBytes, asset.SizeBytes-offset)
		body, _ := json.Marshal(agentwire.ModelFileRequest{LeaseToken: claim.Lease.LeaseToken, File: asset.Path, Offset: offset, Length: length})
		request, _ := http.NewRequest("POST", httpfixture.AdminOrigin+"/agent/v1/nodes/"+g.node.NodeID+"/tasks/"+task.ID+"/model-files", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+g.node.Credential)
		request.Header.Set("Content-Type", "application/json")
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, length+1))
		response.Body.Close()
		expected, e2 := io.ReadAll(io.NewSectionReader(source, offset, length))
		if e != nil || e2 != nil || response.StatusCode != 200 || int64(len(raw)) != length || !bytes.Equal(raw, expected) || response.Header.Get("X-Artifact-SHA256") != digest(expected) {
			t.Fatal("actual derived range bytes/hash/authority", response.StatusCode, e, e2)
		}
	}
	// Expire only this synthetic metadata task through the actual bounded sweep.
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	if e = g.f.s.Nodes.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if count(t, g.f, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='failed' AND lease_token_hash IS NULL", task.ID) != 1 || count(t, g.f, "SELECT count(*) FROM deployments") != deploymentsBefore || count(t, g.f, "SELECT count(*) FROM gpu_leases") != leasesBefore {
		t.Fatal("metadata range fixture fabricated serving or retained execution")
	}
	t.Log("Actual registered native candidate first8MiB and exact final range traversed TLS/current delegated node/task authority/private immutable catalog/file checks with exact bytes/SHA. Explicit metadata-only task; no deployment or physical serving.")
}
