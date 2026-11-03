package deployments_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestRealTLSInferenceAPICSRFSchemaAndLeasedModelTransfer(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	root := t.TempDir()
	os.Chmod(root, 0700)
	c.s.Models.RootDirectory = root
	c.s.Nodes.Identity.Config.Models.ArtifactRoot = root
	content, e := os.ReadFile("../../../../../tests/fixtures/models/qwen3-config.json")
	if e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(root, "objects", c.model)
	if e = os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(directory, "config.json"), content, 0444); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(filepath.Join(directory, "config.json"), 0444); e != nil {
		t.Fatal(e)
	}
	server := httpfixture.NewTLSServer(t, transport.New(c.s.Nodes.Identity))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/deployments/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	// The fixture's already authenticated session belongs to this isolated schema.
	admin.Headers = map[string]string{}
	password := c.password
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	out := admin.Write("POST", "/api/v1/admin/deployments", c.input(), 202, "DeploymentResponse")
	deployment := out["id"].(string)
	admin.Call("GET", "/api/v1/admin/deployments?workspace_id="+c.a.Me.Workspace.ID, nil, 200, "DeploymentListResponse")
	admin.Call("GET", "/api/v1/admin/nodes/"+c.node.NodeID+"/inference", nil, 200, "InferenceNodeStateResponse")
	lease := c.claim(t)
	post := func(path, credential string, input any) (int, []byte, http.Header) {
		t.Helper()
		raw, _ := json.Marshal(input)
		r, _ := http.NewRequest("POST", httpfixture.AdminOrigin+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("Content-Type", "application/json")
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, body, response.Header
	}
	base := "/agent/v1/nodes/" + c.node.NodeID + "/tasks/" + lease.TaskID
	status, body, _ := post(base+"/model-manifest", c.node.Credential, wire.LeaseRenewal{LeaseToken: lease.LeaseToken})
	if status != 200 || !bytes.Contains(body, []byte(wire.ApprovedModel().Revision)) {
		t.Fatal("manifest not authorized", status, string(body))
	}
	approved, _ := wire.ModelFileForName("config.json")
	input := wire.ModelFileRequest{LeaseToken: lease.LeaseToken, File: approved.Path, Length: approved.SizeBytes}
	status, body, headers := post(base+"/model-files", c.node.Credential, input)
	sum := sha256.Sum256(body)
	if status != 200 || !bytes.Equal(body, content) || headers.Get("X-Artifact-SHA256") != hex.EncodeToString(sum[:]) || headers.Get("Content-Type") != "application/octet-stream" {
		t.Fatal("actual TLS model range", status)
	}
	input.File = "../config.json"
	status, _, _ = post(base+"/model-files", c.node.Credential, input)
	if status != 400 {
		t.Fatal("arbitrary model path", status)
	}
	input.File = approved.Path
	input.LeaseToken = strings.Repeat("A", 43)
	status, _, _ = post(base+"/model-files", c.node.Credential, input)
	if status != 409 {
		t.Fatal("stale lease read", status)
	}
	input.LeaseToken = lease.LeaseToken
	status, _, _ = post(base+"/model-files", strings.Repeat("B", 43), input)
	if status != 401 {
		t.Fatal("wrong node credential read", status)
	}
	if _, e = c.s.Nodes.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", lease.TaskID); e != nil {
		t.Fatal(e)
	}
	status, _, _ = post(base+"/model-files", c.node.Credential, input)
	if status != 409 {
		t.Fatal("expired lease read", status)
	}
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	portal.Call("GET", "/api/v1/workspaces/"+c.a.Me.Workspace.ID+"/deployments/"+deployment, nil, 200, "DeploymentResponse")
	portal.Call("GET", "/api/v1/admin/deployments/"+deployment, nil, 404, "Problem")
	admin.Write("POST", "/api/v1/admin/deployments/"+deployment+"/actions", map[string]any{"action": "stop", "revision": 1, "reason": "Synthetic strict fields", "host": "unapproved"}, 400, "Problem")
	csrf := admin.CSRF
	admin.CSRF = ""
	admin.Call("POST", "/api/v1/admin/deployments/"+deployment+"/actions", map[string]any{"action": "stop", "revision": 1, "reason": "Reject missing CSRF"}, 403, "Problem")
	admin.CSRF = csrf
}
