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
	"strconv"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
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
	installModelFile := func(name string, data []byte) {
		t.Helper()
		temp := filepath.Join(directory, name+".replacement")
		if e := os.WriteFile(temp, data, 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Chmod(temp, 0444); e != nil {
			t.Fatal(e)
		}
		if e := os.Rename(temp, filepath.Join(directory, name)); e != nil {
			t.Fatal(e)
		}
	}
	installModelFile("config.json", content)
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
	base := "/agent/v1/sites/default/cells/default/nodes/" + c.node.NodeID + "/tasks/" + lease.TaskID
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
	changed := bytes.Clone(content)
	changed[0] ^= 1
	installModelFile("config.json", changed)
	status, body, headers = post(base+"/model-files", c.node.Credential, input)
	var problem struct {
		Code string `json:"code"`
	}
	if e = json.Unmarshal(body, &problem); e != nil || status != http.StatusConflict ||
		problem.Code != "MODEL_FILE_CHANGED" || headers.Get("X-Artifact-SHA256") != "" {
		t.Fatalf("changed pinned config was not refused: %d %s %v", status, problem.Code, e)
	}
	installModelFile("config.json", content)
	status, body, headers = post(base+"/model-files", c.node.Credential, input)
	if status != http.StatusOK || !bytes.Equal(body, content) || headers.Get("X-Artifact-SHA256") != approved.SHA256 {
		t.Fatalf("the same lease could not retry the restored file: %d", status)
	}
	if fixtureDirectory := os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY"); fixtureDirectory != "" {
		tokenizer, _ := wire.ModelFileForName("tokenizer.json")
		original, e := os.ReadFile(filepath.Join(fixtureDirectory, tokenizer.Path))
		if e != nil || int64(len(original)) != tokenizer.SizeBytes {
			t.Fatalf("pinned tokenizer fixture: %d bytes, %v", len(original), e)
		}
		installModelFile(tokenizer.Path, original)
		request := wire.ModelFileRequest{LeaseToken: lease.LeaseToken, File: tokenizer.Path}
		readRange := func() []byte {
			t.Helper()
			status, body, headers := post(base+"/model-files", c.node.Credential, request)
			digest := sha256.Sum256(body)
			if status != http.StatusOK || !bytes.Equal(body, original[request.Offset:request.Offset+request.Length]) ||
				headers.Get("Content-Length") != strconv.FormatInt(request.Length, 10) ||
				headers.Get("X-Artifact-SHA256") != hex.EncodeToString(digest[:]) {
				t.Fatalf("invalid tokenizer range at %d: %d", request.Offset, status)
			}
			return body
		}
		assembled := sha256.New()
		for request.Offset = 0; request.Offset < tokenizer.SizeBytes; request.Offset += request.Length {
			request.Length = min(wire.ModelTransferChunkBytes, tokenizer.SizeBytes-request.Offset)
			assembled.Write(readRange())
		}
		if hex.EncodeToString(assembled.Sum(nil)) != tokenizer.SHA256 {
			t.Fatal("the range assembly did not match the pinned tokenizer digest")
		}
		request.Offset = (tokenizer.SizeBytes + wire.ModelTransferChunkBytes - 1) / wire.ModelTransferChunkBytes * wire.ModelTransferChunkBytes
		status, _, headers = post(base+"/model-files", c.node.Credential, request)
		if status != http.StatusBadRequest || headers.Get("X-Artifact-SHA256") != "" {
			t.Fatalf("a range beyond the approved tokenizer was admitted: %d", status)
		}
		request.Offset = wire.ModelTransferChunkBytes
		request.Length = tokenizer.SizeBytes - request.Offset
		refuseFile := func() {
			t.Helper()
			status, body, headers := post(base+"/model-files", c.node.Credential, request)
			var problem struct{ Code string }
			if e := json.Unmarshal(body, &problem); e != nil || status != http.StatusConflict ||
				problem.Code != "MODEL_FILE_INVALID" || headers.Get("X-Artifact-SHA256") != "" {
				t.Fatalf("non-immutable tokenizer file was served: %d %s %v", status, problem.Code, e)
			}
		}
		tokenizerPath := filepath.Join(directory, tokenizer.Path)
		saved := tokenizerPath + ".saved"
		if e = os.Rename(tokenizerPath, saved); e != nil {
			t.Fatal(e)
		}
		if e = os.Symlink(filepath.Base(saved), tokenizerPath); e != nil {
			t.Fatal(e)
		}
		refuseFile()
		if e = os.Rename(saved, tokenizerPath); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(tokenizerPath, 0644); e != nil {
			t.Fatal(e)
		}
		refuseFile()
		if e = os.Chmod(tokenizerPath, 0444); e != nil {
			t.Fatal(e)
		}
		readRange()
		t.Logf("Verified %d pinned tokenizer bytes, short terminal range and exact retry", tokenizer.SizeBytes)
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
	// A delivery timeout does not revoke the current token before the task deadline.
	status, _, _ = post(base+"/model-files", c.node.Credential, input)
	if status != 200 {
		t.Fatal("current token refused before the deadline", status)
	}
	if _, e = c.s.Nodes.DB.Exec(ctx, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", lease.TaskID); e != nil {
		t.Fatal(e)
	}
	status, _, _ = post(base+"/model-files", c.node.Credential, input)
	if status != 409 {
		t.Fatal("model read after the task deadline", status)
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
