//go:build knowledge_database_integration && knowledge_encoder_integration

package knowledge_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func sourceDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
func TestM12ActualTLSBothAudiencesSourcePUTCSRFExactContractsAndIngestion(t *testing.T) {
	ctx := context.Background()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	auth := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	scratch := t.TempDir()
	if os.Chmod(scratch, 0700) != nil {
		t.Fatal("private scratch")
	}
	account.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 20
	account.Config.Runtime = &policy
	handler := identityhttp.New(account)
	if e := handler.Knowledge.Service.OpenConfiguredProcessor(ctx); e != nil {
		t.Fatal("real readiness", e)
	}
	defer handler.Knowledge.Service.Processor.Close()
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/knowledge/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, c := range []*httpfixture.Client{admin, portal} {
		c.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	w := auth.Me.Workspace.ID
	base := "/api/v1/workspaces/" + w + "/knowledge-bases"
	adminBase := "/api/v1/admin/knowledge-bases"
	portal.Call("GET", base+"/configuration", nil, 200, "KnowledgeConfigurationResponse")
	admin.Call("GET", adminBase+"/configuration?workspace_id="+w, nil, 200, "KnowledgeConfigurationResponse")
	portal.Call("GET", adminBase+"?workspace_id="+w, nil, 404, "Problem")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	input := map[string]any{"name": "Actual TLS knowledge", "reason": "Synthetic HTTP acceptance"}
	b := portal.Write("POST", base, input, 201, "KnowledgeBaseResponse")
	path := base + "/" + b["id"].(string)
	adminPath := adminBase + "/" + b["id"].(string)
	portal.Write("POST", base, input, 201, "KnowledgeBaseResponse")
	admin.Call("GET", adminBase+"?workspace_id="+w+"&limit=1", nil, 200, "KnowledgeBasePageResponse")
	admin.Call("GET", adminPath, nil, 200, "KnowledgeBaseResponse")
	for _, raw := range []string{`{"name":"x","name":"y","reason":"Synthetic"}`, `{"Name":"x","reason":"Synthetic"}`, `{"name":"x","reason":null}`, `{"name":"x","reason":"\ud800"}`, `{"name":"x","reason":"Synthetic","workspace_id":"foreign"}`} {
		portal.Write("POST", base, []byte(raw), 400, "Problem")
	}
	portal.Write("PATCH", path, map[string]any{"name": "Missing explicit enabled", "expected_revision": b["revision"], "reason": "Must not disable"}, 400, "Problem")
	text := "  Caf\u00e9 😀 \u6c49\u5b57\r\nThe project launch city is Lisbon.\r\n" + strings.Repeat("Synthetic unchanged source context. ", 700)
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	d := portal.Write("POST", path+"/documents", map[string]any{"name": "facts.txt", "content": text, "sha256": sourceDigest(text), "reason": "Actual large exact UTF8 source"}, 202, "KnowledgeDocumentResponse")
	documentPath := path + "/documents/" + d["id"].(string)
	source := d["pending_source"].(map[string]any)["id"].(string)
	got := portal.Call("GET", documentPath+"/source?source_id="+source, nil, 200, "KnowledgeSourceResponse")
	if got["content"] != text {
		t.Fatal("exact CRLF source roundtrip")
	}
	portal.Call("GET", documentPath+"/source?source_id="+source+"&source_id="+source, nil, 400, "Problem")
	portal.Call("GET", path+"/documents?limit=1", nil, 200, "KnowledgeDocumentPageResponse")
	if worked, e := handler.Knowledge.Service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual HTTP ingestion", e)
	}
	d = portal.Call("GET", documentPath, nil, 200, "KnowledgeDocumentResponse")
	if d["state"] != "ready" {
		t.Fatal("actual persisted ready missing")
	}
	search := portal.Write("POST", path+"/searches", map[string]any{"query": "Which city does the project launch in?", "top_k": 3, "minimum_score": 0.1}, 200, "KnowledgeSearchResponse")
	if len(search["matches"].([]any)) == 0 {
		t.Fatal("actual semantic result missing")
	}
	replace := map[string]any{"content": "The updated project launch city is Porto.", "sha256": sourceDigest("The updated project launch city is Porto."), "expected_revision": d["revision"], "reason": "Actual PUT source replacement"}
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	csrf := portal.CSRF
	portal.CSRF = ""
	portal.Call("PUT", documentPath+"/source", replace, 403, "Problem")
	portal.CSRF = csrf
	portal.Headers["Origin"] = "https://foreign.example.invalid"
	portal.Call("PUT", documentPath+"/source", replace, 403, "Problem")
	delete(portal.Headers, "Origin")
	d = portal.Write("PUT", documentPath+"/source", replace, 202, "KnowledgeDocumentResponse")
	if d["visible_source"] == nil || d["pending_source"] == nil {
		t.Fatal("replacement hid last good source")
	}
	if worked, e := handler.Knowledge.Service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual PUT replacement", e)
	}
	d = portal.Call("GET", documentPath, nil, 200, "KnowledgeDocumentResponse")
	portal.Write("DELETE", documentPath, map[string]any{"expected_revision": d["revision"], "reason": "Immediate visibility deletion"}, 200, "KnowledgeDocumentResponse")
	portal.Call("GET", documentPath+"/source?source_id="+source, nil, 404, "Problem")
	admin.Call("GET", adminPath+"/documents/"+d["id"].(string), nil, 404, "Problem")
	if e := handler.Knowledge.Service.VerifyStored(ctx); e != nil {
		t.Fatal("actual vector readiness", e)
	}
	// Actual common restore authority invalidation; database export/import is a
	// separate required recovery profile, not claimed by this transaction test.
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	pending := portal.Write("POST", path+"/documents", map[string]any{"name": "interrupted.txt", "content": "Synthetic restored pending source.", "sha256": sourceDigest("Synthetic restored pending source."), "reason": "Pending authority before common recovery"}, 202, "KnowledgeDocumentResponse")
	if e := recovery.InvalidateRestored(ctx, account, "Synthetic isolated restore authority validation"); e != nil {
		t.Fatal("common recovery", e)
	}
	portal.Call("GET", path, nil, 401, "Problem")
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	recovered := portal.Call("GET", path+"/documents/"+pending["id"].(string), nil, 200, "KnowledgeDocumentResponse")
	attempt := recovered["current_attempt"].(map[string]any)
	if recovered["state"] != "failed" || attempt["error_code"] != "KNOWLEDGE_WORKER_INTERRUPTED" {
		t.Fatal("restored live source authority not fenced")
	}
	// This test never configures a fake answer gateway or claims a Rust answer.
}
