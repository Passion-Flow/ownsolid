//go:build dataset_processor_integration

package datasets_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func verifyTokenizerPreviewHTTPS(t *testing.T, service *datasets.Service, auth identity.Auth, portal, admin *httpfixture.Client, path, adminPath string) {
	t.Helper()
	approvedSource := os.Getenv("OWNSOLID_TEST_QWEN_TOKENIZER_DIRECTORY")
	if approvedSource == "" {
		t.Fatal("the pinned Qwen tokenizer files are required")
	}
	workspace, tenant := auth.Me.Workspace.ID, auth.Me.Workspace.TenantID
	model, foreignModel, foreignWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e := service.DB.Exec(t.Context(), "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign preview fixture','active')", foreignWorkspace, tenant); e != nil {
		t.Fatal(e)
	}
	manifest, e := json.Marshal(agentwire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	// Seed registry metadata for the previously verified snapshot. The preview
	// must prove custody of its subset without reading the weights.
	for _, binding := range [][2]string{{model, workspace}, {foreignModel, foreignWorkspace}} {
		if _, e = service.DB.Exec(t.Context(), `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
VALUES($1,$2,$3,'Preview fixture',$4,$5,$6,'ready',$7)`,
			binding[0], tenant, binding[1], agentwire.ApprovedModel().ModelKey, agentwire.ModelCatalogDigest(), manifest, auth.Me.User.ID); e != nil {
			t.Fatal(e)
		}
	}
	modelPath := filepath.Join(service.Identity.Config.Models.ArtifactRoot, "objects", model)
	if e = os.MkdirAll(modelPath, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"config.json", "merges.txt", "tokenizer.json", "tokenizer_config.json", "vocab.json"} {
		raw, e := os.ReadFile(filepath.Join(approvedSource, name))
		if e != nil {
			t.Fatal(e)
		}
		writePreviewModelFixture(t, filepath.Join(modelPath, name), raw)
	}
	record := wire.Record{SourceID: "preview", Prompt: strings.Repeat("token ", 65), Completion: "A synthetic response."}
	encoded, e := record.Canonical(service.Identity.Config.Policy().Datasets.Limits)
	if e != nil {
		t.Fatal(e)
	}
	draft := prepareHTTPSPublicationDraft(t, service, portal, path, append(encoded, '\n'))
	draftID := draft["id"].(string)
	previewPath := path + "/drafts/" + draftID + "/tokenizer-preview"
	adminPath += "/drafts/" + draftID + "/tokenizer-preview"
	input := map[string]any{"model_id": model, "sample_limit": 1, "maximum_tokens": 32}
	// Reuse the authenticated session token for the requests in this fixture.
	portal.Call("POST", previewPath, map[string]any{"model_id": model, "sample_limit": 9, "maximum_tokens": 32}, 400, "Problem")
	portal.Call("POST", previewPath, map[string]any{"model_id": model, "sample_limit": 1, "maximum_tokens": 31}, 400, "Problem")
	portal.Call("POST", previewPath, map[string]any{"model_id": foreignModel, "sample_limit": 1, "maximum_tokens": 32}, 404, "Problem")
	foreignPath := "/api/v1/workspaces/" + foreignWorkspace + "/datasets/" + draft["dataset_id"].(string) + "/drafts/" + draftID + "/tokenizer-preview"
	portal.Call("POST", foreignPath, input, 404, "Problem")
	result := portal.Call("POST", previewPath, input, 200, "")
	samples := result["samples"].([]any)
	if result["model_catalog_sha256"] != agentwire.ModelCatalogDigest() || result["maximum_tokens"] != float64(32) || len(samples) != 1 {
		t.Fatal("preview did not bind the requested model, budget and sample")
	}
	sample := samples[0].(map[string]any)
	digest, e := record.ContentDigest(service.Identity.Config.Policy().Datasets.Limits)
	if e != nil || sample["content_sha256"] != digest || sample["line_number"] != float64(1) ||
		sample["first_excess_token_index"] != float64(32) || sample["first_excess_section"] != "prompt" ||
		sample["completion"] != record.Completion || sample["total_tokens"] != sample["prompt_tokens"].(float64)+sample["completion_tokens"].(float64) {
		t.Fatal("preview lost sample identity or the correct overflow boundary", e)
	}
	input["maximum_tokens"] = sample["total_tokens"]
	fit := admin.Call("POST", adminPath, input, 200, "")["samples"].([]any)[0].(map[string]any)
	if fit["first_excess_token_index"] != nil || fit["first_excess_section"] != nil || fit["rendered_prompt"] != sample["rendered_prompt"] {
		t.Fatal("an exactly fitting sample was not reproduced")
	}
	input["maximum_tokens"] = 32
	file := filepath.Join(modelPath, "config.json")
	raw, e := os.ReadFile(file)
	if e != nil || len(raw) == 0 {
		t.Fatal("read local fixture", e)
	}
	if e = os.Remove(file); e != nil {
		t.Fatal(e)
	}
	if problem := portal.Call("POST", previewPath, input, 409, "Problem"); problem["code"] != "DATASET_FILE_INVALID" {
		t.Fatal("missing tokenizer asset was not rejected")
	}
	raw[0] ^= 1
	writePreviewModelFixture(t, file, raw)
	if problem := portal.Call("POST", previewPath, input, 409, "Problem"); problem["code"] != "DATASET_FILE_INVALID" {
		t.Fatal("same-size tokenizer tampering was not rejected")
	}

	var oversized strings.Builder
	for _, id := range []string{"large-a", "large-b", "large-c"} {
		row := wire.Record{SourceID: id, Prompt: strings.Repeat("p", 12000), Completion: strings.Repeat("c", 12000)}
		line, e := row.Canonical(service.Identity.Config.Policy().Datasets.Limits)
		if e != nil {
			t.Fatal(e)
		}
		oversized.Write(line)
		oversized.WriteByte('\n')
	}
	big := prepareHTTPSPublicationDraft(t, service, portal, path, []byte(oversized.String()))
	input["sample_limit"] = 3
	bigPath := path + "/drafts/" + big["id"].(string) + "/tokenizer-preview"
	portal.Call("POST", bigPath, input, 413, "Problem")
	t.Log("real Qwen tokenizer preview validated over scoped HTTPS")
}

func writePreviewModelFixture(t *testing.T, path string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, 0444); e != nil {
		t.Fatal(e)
	}
}
