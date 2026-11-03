package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"strconv"
	"strings"
	"testing"
)

func clientDerivedCommand(t *testing.T) wire.InferenceCommand {
	t.Helper()
	// Catalog/model identities and the128-byte tensor descriptor are explicit contract-test data.
	// This fixture registers/accepts no model and makes no file, GPU or serving claim.
	id := "12345678-1234-1234-1234-123456789abc"
	model := "22345678-1234-1234-1234-123456789abc"
	catalog := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: wire.DerivedModelKind, Scope: lifecycle.Scope{TenantID: id, WorkspaceID: id, PreparationID: id}, ModelID: model, BaseModelID: id, BaseCatalogSHA256: wire.ModelCatalogDigest(), AttemptID: id, ArtifactID: model, ReportSHA256: strings.Repeat("a", 64), RecipeSHA256: lifecycle.RecipeDigest(), BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: 2048, Files: lifecycle.ApprovedBaseFiles()}
	for index, file := range catalog.Files {
		if file.Path == "model.safetensors" {
			catalog.Files[index].SizeBytes = 128
			catalog.Files[index].SHA256 = strings.Repeat("b", 64)
		}
	}
	digest, e := catalog.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return wire.InferenceCommand{Action: "deploy", DeploymentID: id, ModelID: model, CatalogDigest: digest, GPUUUID: "GPU-" + id, Revision: 1, Derived: &wire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: catalog, ServingRecipeSHA256: wire.DerivedServingRecipeDigest()}}
}

func TestRealTLSDerivedRangesFullHashAndPrivateIncompleteFailure(t *testing.T) {
	c := clientDerivedCommand(t)
	// Explicit deterministic transport bytes, not a safetensors/model acceptance or physical serving fixture.
	content := make([]byte, wire.ModelTransferChunkBytes+19)
	for i := range content {
		content[i] = byte(i % 251)
	}
	sum := sha256.Sum256(content)
	for i := range c.Derived.Catalog.Files {
		if c.Derived.Catalog.Files[i].Path == "model.safetensors" {
			c.Derived.Catalog.Files[i].SizeBytes = int64(len(content))
			c.Derived.Catalog.Files[i].SHA256 = hex.EncodeToString(sum[:])
		}
	}
	c.CatalogDigest, _ = c.Derived.Catalog.Digest()
	asset, _ := c.ModelFile("model.safetensors")
	lease := strings.Repeat("A", 43)
	credential := strings.Repeat("B", 43)
	ranges := 0
	corrupt := false
	path := "/agent/v1/nodes/12345678-1234-1234-1234-123456789abc/tasks/22345678-1234-1234-1234-123456789abc/model-files"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("unbound transport")
			w.WriteHeader(400)
			return
		}
		var request wire.ModelFileRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.ValidateForCommand(c) != nil || request.LeaseToken != lease {
			t.Error("unbound derived range")
			w.WriteHeader(400)
			return
		}
		ranges++
		body := content[request.Offset : request.Offset+request.Length]
		hash := sha256.Sum256(body)
		if corrupt && request.Offset > 0 {
			body = append([]byte(nil), body...)
			body[0] ^= 1
			hash = sha256.Sum256(body)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("X-Artifact-SHA256", hex.EncodeToString(hash[:]))
		w.Write(body)
	}))
	defer server.Close()
	connection := &Client{Origin: server.URL, HTTP: server.Client(), Credential: credential}
	defer connection.Close()
	root := t.TempDir()
	destination := filepath.Join(root, "verified")
	if e := connection.DerivedModelFile(context.Background(), path, lease, c, asset, destination); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(destination)
	if e != nil || info.Mode().Perm() != 0444 || info.Size() != asset.SizeBytes || ranges != 2 {
		t.Fatal("not exact immutable two-range bytes", e)
	}
	corrupt = true
	partial := filepath.Join(root, "partial")
	if e = connection.DerivedModelFile(context.Background(), path, lease, c, asset, partial); e == nil {
		t.Fatal("valid per-range hashes replaced whole catalog hash")
	}
	info, e = os.Stat(partial)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unaccepted bytes exposed", e)
	}
	changed := asset
	changed.SizeBytes++
	if e = connection.DerivedModelFile(context.Background(), path, lease, c, changed, filepath.Join(root, "unexpected")); e == nil {
		t.Fatal("descriptor override")
	}
	c.Action = "stop"
	if e = connection.DerivedModelFile(context.Background(), path, lease, c, asset, filepath.Join(root, "unexpected")); e == nil {
		t.Fatal("cleanup gained download authority")
	}
}
