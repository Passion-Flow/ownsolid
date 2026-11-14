package http_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestModelImportHTTPNondefaultAdmissionAndRetryPolicy(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	s.Config.Models.ArtifactRoot = root
	p := config.DefaultRuntimePolicy()
	p.Models.MaximumAttempts = 1
	p.Models.MaximumBundleBytes = 1600000000
	s.Config.Runtime = &p
	server := httpfixture.NewTLSServer(t, identityhttp.New(s))
	defer server.Close()
	client := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, "../../../../../../api/openapi/v1/models/openapi.json"))
	client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	workspace := client.Call("GET", "/api/v1/me", nil, 200, "")["workspace"].(map[string]any)["id"].(string)
	catalog := client.Call("GET", "/api/v1/admin/models/catalog", nil, 200, "ModelCatalogAvailabilityResponse")
	if catalog["maximum_bundle_bytes"] != float64(1600000000) || catalog["maximum_attempts"] != float64(1) || catalog["minimum_bundle_bytes"] != float64(modelartifact.MinimumArchiveBytes()) {
		t.Fatal("actual HTTPS policy catalog")
	}
	input := models.ImportRequest{WorkspaceID: workspace, Name: "Bounded HTTP policy import", CatalogDigest: wire.ModelCatalogDigest(), LicenseAcknowledged: true, Reason: "Synthetic policy acceptance"}
	for _, size := range []int64{modelartifact.MinimumArchiveBytes() - 1, 1600000001} {
		input.ExpectedBytes = size
		client.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		client.Write("POST", "/api/v1/admin/model-imports", input, 400, "Problem")
	}
	input.ExpectedBytes = modelartifact.MinimumArchiveBytes()
	client.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	value := client.Write("POST", "/api/v1/admin/model-imports", input, 201, "ModelImportResponse")
	id := value["id"].(string)
	client.Headers = map[string]string{}
	// Synthetic exhausted metadata exercises retry policy; no archive was uploaded
	// and this test does not claim successful model verification/publication.
	if _, e := s.DB.Exec(context.Background(), `UPDATE model_imports SET state='failed',attempts=1,uploaded_bytes=expected_bytes WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	client.Write("POST", "/api/v1/admin/model-imports/"+id+"/complete", map[string]any{"reason": "Refuse exhausted retry"}, 409, "Problem")
	client.Write("POST", "/api/v1/admin/model-imports/"+id+"/cancel", map[string]any{"reason": "Discard exhausted synthetic intent"}, 200, "ModelImportResponse")
}
