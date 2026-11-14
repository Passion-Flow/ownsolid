//go:build knowledge_database_integration && knowledge_encoder_integration && knowledge_target_integration

package knowledge_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM12ActualTargetClientLocalTLSCPUJourneyAndCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	scope := uuid.NewString()
	w := &workspaces.Service{DB: account.DB, Identity: account}
	workspace, e := w.Create(ctx, admin, "Knowledge qualification "+scope, admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	scratch := t.TempDir()
	if os.Chmod(scratch, 0700) != nil {
		t.Fatal("private scratch")
	}
	repository := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	account.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(repository, "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 20
	account.Config.Runtime = &policy
	handler := identityhttp.New(account)
	if e = handler.Knowledge.Service.OpenConfiguredProcessor(ctx); e != nil {
		t.Fatal("actual offline encoder readiness", e)
	}
	defer handler.Knowledge.Service.Processor.Close()
	server := httptest.NewUnstartedServer(handler)
	account.Config.Server.PortalOrigin = "https://" + server.Listener.Addr().String()
	account.Config.Server.AdminOrigin = "https://admin.example.invalid"
	server.StartTLS()
	defer server.Close()
	worker, stopWorker := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); handler.Knowledge.Service.Run(worker) }()
	defer func() {
		stopWorker()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("actual worker did not stop")
		}
	}()
	directory := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "target-client-local")
	if os.Getenv("PROJECT_ONE_EVIDENCE_ROOT") == "" || os.Mkdir(directory, 0700) != nil {
		t.Fatal("explicit new external target-client evidence required")
	}
	write := func(name string, value []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if os.WriteFile(path, value, 0600) != nil {
			t.Fatal("private target reference")
		}
		return path
	}
	credentials, _ := json.Marshal(map[string]string{"email": "admin@example.invalid", "password": password})
	credentialPath := write("synthetic-credentials.json", credentials)
	ca := write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	input, _ := json.Marshal(map[string]any{"schema_version": 1, "kind": "projectone-isolated-knowledge-journey-v1", "scope_id": scope, "mount_root": os.Getenv("PROJECT_ONE_EXTERNAL_ROOT"), "workspace_id": workspace.ID, "portal_origin": server.URL, "ca_file": ca, "credentials_file": credentialPath, "expected_catalog_sha256": knowledge.CatalogDigest(), "alias_id": nil})
	configuration := write("scope.json", input)
	output := filepath.Join(directory, "journey")
	command := exec.CommandContext(ctx, "python3", "-B", filepath.Join(repository, "tests/validation/knowledge/journey.py"), "--config", configuration, "--output-directory", output, "--local-protocol-check")
	raw, e := command.CombinedOutput()
	if e != nil {
		t.Fatal("actual target-client local protocol failed", e, string(raw))
	}
	receipt, e := os.ReadFile(filepath.Join(output, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var result struct {
		Status, Mode, Cleanup string
		CreatedBaseID         string `json:"created_base_id"`
		CreatedDocumentID     string `json:"created_document_id"`
		Checks                []any
	}
	if json.Unmarshal(receipt, &result) != nil || result.Status != "passed_local_protocol_only" || result.Mode != "local_protocol_only" || len(result.Checks) != 6 || result.Cleanup != "new_document_tombstoned_new_base_disabled_session_logged_out_audit_retained" {
		t.Fatal("actual target-client acceptance/cleanup receipt missing")
	}
	var disabled, deleted bool
	if e = account.DB.QueryRow(ctx, `SELECT NOT b.enabled,d.deleted_at IS NOT NULL FROM knowledge_bases b JOIN knowledge_documents d ON d.base_id=b.id WHERE b.id=$1 AND d.id=$2`, result.CreatedBaseID, result.CreatedDocumentID).Scan(&disabled, &deleted); e != nil || !disabled || !deleted {
		t.Fatal("actual target-client cleanup did not persist", e)
	}
	t.Log("real stdlib target client→Go TLS/CSRF/cookies/current Workspace→actual offline CPU/pgvector/source/search/delete/cleanup passed in explicit local mode; native target remains pending")
}
