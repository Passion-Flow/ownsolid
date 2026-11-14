package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestRealTLSModelManifestRangeDigestAndIncompleteRecovery(t *testing.T) {
	content, e := os.ReadFile("../../../../tests/fixtures/models/qwen3-config.json")
	if e != nil {
		t.Fatal(e)
	}
	approved, _ := wire.ModelFileForName("config.json")
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != approved.SHA256 || int64(len(content)) != approved.SizeBytes {
		t.Fatal("actual fixture drift")
	}
	corrupt := false
	manifestChanged := false
	requests := 0
	lease := strings.Repeat("A", 43)
	credential := strings.Repeat("B", 43)
	path := "/agent/v1/nodes/12345678-1234-1234-1234-123456789abc/tasks/22345678-1234-1234-1234-123456789abc/model-files"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("missing node credential")
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "model-manifest") {
			var input wire.LeaseRenewal
			json.NewDecoder(r.Body).Decode(&input)
			if input.LeaseToken != lease {
				t.Error("manifest not lease bound")
			}
			catalog := wire.ApprovedModel()
			if manifestChanged {
				catalog.Revision = "unapproved"
			}
			json.NewEncoder(w).Encode(map[string]any{"data": catalog})
			return
		}
		requests++
		var input wire.ModelFileRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Validate() != nil || input.LeaseToken != lease || input.File != approved.Path {
			t.Error("invalid fixed model range")
			w.WriteHeader(400)
			return
		}
		digest := approved.SHA256
		if corrupt {
			digest = strings.Repeat("0", 64)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Header().Set("X-Artifact-SHA256", digest)
		w.Write(content)
	}))
	defer server.Close()
	root := t.TempDir()
	ca := filepath.Join(root, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	c, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.Credential = credential
	destination := filepath.Join(root, "config.json")
	if e = c.ModelFile(context.Background(), path, lease, approved, destination); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(destination)
	if e != nil || info.Size() != approved.SizeBytes || info.Mode().Perm() != 0444 || requests != 1 {
		t.Fatal("file not atomically bounded and verified", e)
	}
	corrupt = true
	partial := filepath.Join(root, "partial.json")
	if e = c.ModelFile(context.Background(), path, lease, approved, partial); e == nil {
		t.Fatal("corrupt range accepted")
	}
	info, e = os.Stat(partial)
	if e != nil || info.Size() != 0 || info.Mode().Perm() != 0600 {
		t.Fatal("failed bytes exposed as immutable model", e)
	}
	manifestChanged = true
	uncreated := filepath.Join(root, "uncreated.json")
	if e = c.ModelFile(context.Background(), path, lease, approved, uncreated); e == nil {
		t.Fatal("changed manifest accepted")
	}
	if _, e = os.Stat(uncreated); !os.IsNotExist(e) {
		t.Fatal("unapproved manifest created model file")
	}
	if e = c.ModelFile(context.Background(), path+"?host=outside", lease, approved, uncreated); e == nil {
		t.Fatal("arbitrary path accepted")
	}
}
