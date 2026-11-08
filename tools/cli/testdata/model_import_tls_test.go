package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func TestLaneDModelImportTLS(t *testing.T) {
	bundle := os.Getenv("LANE_D_MODEL_BUNDLE")
	if bundle == "" {
		t.Skip("provide the approved model bundle via LANE_D_MODEL_BUNDLE")
	}
	info, err := os.Stat(bundle)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("approved bundle is unavailable", err)
	}
	store := t.TempDir()
	if err := os.Chmod(store, 0700); err != nil {
		t.Fatal(err)
	}
	var lose atomic.Bool
	var accepted atomic.Bool
	c := newWorkflowCLI(t, func(service *identity.Service) {
		service.Config.Models.ArtifactRoot = store
	}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chunks") && lose.CompareAndSwap(true, false) {
				response := httptest.NewRecorder()
				next.ServeHTTP(response, r)
				accepted.Store(response.Code == http.StatusOK)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	c.ok("Apache-2.0", "model", "import", "catalog")
	c.refused("--accept-license", "model", "import", "start", bundle, "--reason", "Verify approved bundle")
	c.count(0, "SELECT count(*) FROM model_imports")
	start := func(name, key string) []string {
		return []string{"model", "import", "start", bundle, "--name", name, "--reason", "Verify approved bundle", "--idempotency-key", key, "--accept-license", "-o", "json"}
	}
	interrupt := func(args []string) string {
		t.Helper()
		accepted.Store(false)
		lose.Store(true)
		c.refused("interrupted; resume with the original bundle", args...)
		if !accepted.Load() {
			t.Fatal("the real dispatcher did not accept the chunk before the response was lost")
		}
		var id string
		if err := c.service.DB.QueryRow(t.Context(), "SELECT id::text FROM model_imports WHERE state='uploading'").Scan(&id); err != nil {
			t.Fatal(err)
		}
		c.count(1, "SELECT count(*) FROM model_import_chunks WHERE import_id=$1 AND byte_offset=0", id)
		return id
	}
	withdrawn := interrupt(start("Withdrawn import", uuid.NewString()))
	c.refused("--confirm", "model", "import", "cancel", withdrawn, "--reason", "Withdraw")
	if _, err := c.service.DB.Exec(t.Context(), "UPDATE users SET is_platform_admin=false WHERE id=$1", c.auth.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	cancel := []string{"model", "import", "cancel", withdrawn, "--reason", "Withdraw", "--confirm", "-o", "json"}
	c.refused("HTTP 401", cancel...)
	c.count(1, "SELECT count(*) FROM model_import_chunks WHERE import_id=$1", withdrawn)
	if _, err := c.service.DB.Exec(t.Context(), "UPDATE users SET is_platform_admin=true WHERE id=$1", c.auth.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	c.ok(`"state": "cancelled"`, cancel...)
	if _, err := os.Stat(filepath.Join(store, "imports", withdrawn)); !os.IsNotExist(err) {
		t.Fatal("cancelled import bytes remain", err)
	}

	key := uuid.NewString()
	id := interrupt(start("Approved Qwen cache", key))
	c.ok(id, "model", "import", "list")
	c.ok(`"uploaded_bytes": 8388608`, "model", "import", "get", id, "-o", "json")
	wrong := filepath.Join(t.TempDir(), "wrong.tar")
	f, err := os.Create(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(info.Size()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	resume := func(file string) []string {
		return []string{"model", "import", "resume", id, file, "--reason", "Verify approved bundle", "-o", "json"}
	}
	c.refused("MODEL_CHUNK_CONFLICT", resume(wrong)...)
	c.refused("outside the selected workspace", append(resume(bundle), "--workspace", uuid.NewString())...)
	c.count(1, "SELECT count(*) FROM model_import_chunks WHERE import_id=$1", id)
	c.ok(`"state": "queued"`, resume(bundle)...)
	c.ok(`"state": "queued"`, "model", "import", "verify", id, "--reason", "Recheck queue", "-o", "json")
	c.count(1, "SELECT count(*) FROM audit_events WHERE action='model.import_queued' AND target_id=$1", id)
	const chunkBytes = 8 << 20
	c.count(int((info.Size()+chunkBytes-1)/chunkBytes), "SELECT count(*) FROM model_import_chunks WHERE import_id=$1", id)

	stage := filepath.Join(store, "imports", id, "stage-1")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(stage, "unexpected-file")
	if err := os.WriteFile(sentinel, []byte("fixture stage obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	processed, err := c.handler.Models.Service.ProcessNext(t.Context())
	if !processed || err == nil || err.Error() != "MODEL_STAGE_INVALID" {
		t.Fatalf("the real verifier did not report the obstructed stage: %t %v", processed, err)
	}
	c.ok("MODEL_STAGE_INVALID", "model", "import", "get", id, "-o", "json")
	c.refused("failed verification", resume(bundle)...)
	c.ok(`"state": "queued"`, "model", "import", "verify", id, "--reason", "Retry after stage failure", "-o", "json")
	c.count(2, "SELECT count(*) FROM audit_events WHERE action='model.import_queued' AND target_id=$1", id)
	processed, err = c.handler.Models.Service.ProcessNext(t.Context())
	if err != nil || !processed {
		t.Fatalf("the real model verifier did not process the queued bundle: %v", err)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "fixture stage obstruction" {
		t.Fatal("verification retry modified the rejected stage", err)
	}
	var ready struct {
		State   string `json:"state"`
		ModelID string `json:"model_id"`
	}
	out := c.ok(`"state": "ready"`, "model", "import", "get", id, "-o", "json")
	if err := json.Unmarshal([]byte(out), &ready); err != nil || ready.State != "ready" || ready.ModelID == "" {
		t.Fatalf("model verifier did not publish a ready model: %v", err)
	}
	c.ok(wire.ModelCatalogDigest(), "model", "get", ready.ModelID, "-o", "json")
	c.ok(`"state": "ready"`, start("Approved Qwen cache", key)...)
	c.count(1, "SELECT count(*) FROM model_versions WHERE id=$1 AND state='ready'", ready.ModelID)
	c.count(2, "SELECT count(*) FROM model_imports")
}
