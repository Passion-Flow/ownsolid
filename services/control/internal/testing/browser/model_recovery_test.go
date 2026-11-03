package browser_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
)

func TestM20ModelImportRecoveryBrowser(t *testing.T) {
	testProductionBrowser(t, "models/recovery-flow.mjs", "model-recovery-browser", false, func(ctx context.Context, service *models.Service, output string) {
		verifyModelRecoveryBrowser(t, ctx, service, output, false)
	})
}

func verifyModelRecoveryBrowser(t *testing.T, ctx context.Context, service *models.Service, output string, verifiedObjectFixture bool) {
	t.Helper()
	var result struct {
		Status      string `json:"status"`
		ModelID     string `json:"model_id"`
		CancelledID string `json:"cancelled_id"`
		WorkspaceID string `json:"workspace_id"`
		ActorID     string `json:"actor_id"`
		FixtureMode string `json:"fixture_mode"`
	}
	data, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil || json.Unmarshal(data, &result) != nil || result.Status != "passed_actual_model_import_recovery_browser" {
		t.Fatal("complete browser result required", e)
	}
	for _, id := range []string{result.ModelID, result.CancelledID, result.WorkspaceID, result.ActorID} {
		if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil || parsed.String() != id {
			t.Fatal("canonical actual browser identity required")
		}
	}
	var imports, versions, chunks, attempts, cancelledChunks int
	var expected, uploaded, chunkBytes int64
	var state, model, versionState string
	if e = service.DB.QueryRow(ctx, `SELECT COUNT(*) FROM model_imports`).Scan(&imports); e != nil || imports != 2 {
		t.Fatal("exactly two real intent rows required", imports, e)
	}
	if e = service.DB.QueryRow(ctx, `SELECT expected_bytes,uploaded_bytes,state,model_id::text,attempts FROM model_imports WHERE id=$1 AND workspace_id=$2 AND requested_by=$3`, result.ModelID, result.WorkspaceID, result.ActorID).Scan(&expected, &uploaded, &state, &model, &attempts); e != nil || expected != uploaded || state != "ready" || model != result.ModelID || attempts != 1 {
		t.Fatal("actual recovered publication state/actor/attempt mismatch", e)
	}
	expectedChunks, expectedChunkBytes := int((expected+models.ChunkBytes-1)/models.ChunkBytes), expected
	if verifiedObjectFixture {
		if result.FixtureMode != "verified_immutable_object_recovery_no_chunk_rows" {
			t.Fatal("explicit verified-object fixture mode required")
		}
		expectedChunks, expectedChunkBytes = 0, 0
	} else if result.FixtureMode != "" {
		t.Fatal("normal browser cannot adopt a fixture mode")
	}
	if e = service.DB.QueryRow(ctx, `SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM model_import_chunks WHERE import_id=$1`, result.ModelID).Scan(&chunks, &chunkBytes); e != nil || chunkBytes != expectedChunkBytes || chunks != expectedChunks {
		t.Fatal("real bounded chunk metadata mismatch", e)
	}
	if e = service.DB.QueryRow(ctx, `SELECT COUNT(*) FROM model_versions`).Scan(&versions); e != nil || versions != 1 {
		t.Fatal("one immutable model identity required", e)
	}
	if e = service.DB.QueryRow(ctx, `SELECT state FROM model_versions WHERE id=$1 AND workspace_id=$2 AND created_by=$3`, result.ModelID, result.WorkspaceID, result.ActorID).Scan(&versionState); e != nil || versionState != "deleted" {
		t.Fatal("actual browser retirement required", e)
	}
	if e = service.DB.QueryRow(ctx, `SELECT state,uploaded_bytes,attempts FROM model_imports WHERE id=$1 AND workspace_id=$2 AND requested_by=$3`, result.CancelledID, result.WorkspaceID, result.ActorID).Scan(&state, &uploaded, &attempts); e != nil || state != "cancelled" || uploaded != 0 || attempts != 0 {
		t.Fatal("actual paused/cancelled import must retain zero uploaded bytes/attempts", e)
	}
	if e = service.DB.QueryRow(ctx, `SELECT COUNT(*) FROM model_import_chunks WHERE import_id=$1`, result.CancelledID).Scan(&cancelledChunks); e != nil || cancelledChunks != 0 {
		t.Fatal("paused chunk was never forwarded", e)
	}
	audits := map[string]int{}
	for _, target := range []struct {
		id      string
		actions []string
	}{
		{result.ModelID, []string{"model.import_started", "model.import_queued", "model.import_verification_started", "model.import_ready", "model.version_retired"}},
		{result.CancelledID, []string{"model.import_started", "model.import_cancellation_requested", "model.import_cancelled"}},
	} {
		for _, action := range target.actions {
			var count, scoped int
			if e = service.DB.QueryRow(ctx, `SELECT COUNT(*),COUNT(CASE WHEN workspace_id=$3 AND actor_user_id=$4 THEN 1 END) FROM audit_events WHERE target_id=$1 AND action=$2 AND outcome='success'`, target.id, action, result.WorkspaceID, result.ActorID).Scan(&count, &scoped); e != nil || count != 1 || scoped != 1 {
				t.Fatal("exactly one scoped real audit required", action, count, scoped, e)
			}
			audits[target.id+":"+action] = count
		}
	}
	files := map[string]string{}
	for _, expectedFile := range wire.ApprovedModel().Files {
		name := filepath.Join(service.RootDirectory, "objects", result.ModelID, expectedFile.Path)
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() != expectedFile.SizeBytes {
			t.Fatal("actual immutable retained file required", expectedFile.Path, err)
		}
		file, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.New()
		size, readErr := io.Copy(digest, file)
		closeErr := file.Close()
		actual := hex.EncodeToString(digest.Sum(nil))
		if readErr != nil || closeErr != nil || size != expectedFile.SizeBytes || actual != expectedFile.SHA256 {
			t.Fatal("actual publication file digest mismatch", expectedFile.Path)
		}
		files[expectedFile.Path] = actual
	}
	proof, err := json.MarshalIndent(map[string]any{"status": "passed_actual_post_browser_SQL_audit_and_all_retained_file_digests", "fixture_mode": result.FixtureMode, "import_rows": imports, "model_rows": versions, "expected_bytes": expected, "actual_chunks": chunks, "chunk_bytes": chunkBytes, "cancelled_chunks": cancelledChunks, "exact_audits": audits, "approved_file_sha256": files}, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(output, "durable-readback.json"), append(proof, '\n'), 0600) != nil {
		t.Fatal("durable readback receipt write failed", err)
	}
}
