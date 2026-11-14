package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The proof of REL-001 (docs/research/control-crash-recovery.md).
const (
	crashSeconds = 1200
	// crashLongTasksAt starts the long tasks, late enough for the first crash and the
	// upload to be over. crashLongTaskLength outlasts the second crash and control's return.
	crashLongTasksAt    = 420
	crashLongTaskWindow = 30
	crashLongTaskLength = 90
	// crashReturn is how long Docker and control get to answer again after a kill.
	crashReturn = 2 * time.Minute
	// crashImport is how long the second attempt gets to extract and publish the model.
	crashImport = 5 * time.Minute
	// crashUploadFraction of the bundle goes up before the first kill.
	crashUploadFraction = 3
	importsPath         = "/api/v1/sites/default/cells/default/admin/model-imports"
	catalogPath         = "/api/v1/sites/default/cells/default/admin/models/catalog"
	controlContainer    = "ownsolid-control-1"
	// crashLimits leaves room for the model store, which this test keeps in memory:
	// memory-backed files count against the container that writes them.
	crashLimits = `resources: {control_memory: 6g}
`
)

// modelImport is the part of an import this test reads.
type modelImport struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	ErrorCode     string `json:"error_code"`
	UploadedBytes int64  `json:"uploaded_bytes"`
	Attempts      int    `json:"attempts"`
}

// controlRestarts is how many times Docker has restarted control.
func controlRestarts(t *testing.T) int {
	t.Helper()
	output, err := run(t, "docker", "inspect", "--format", "{{.RestartCount}}", controlContainer)
	if err != nil {
		t.Fatalf("inspect control: %v %s", err, output)
	}
	n, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		t.Fatal(output, err)
	}
	return n
}

// killControl sends SIGKILL to control's process from outside its container, as the
// kernel's out-of-memory killer or a failing host would: nothing runs on the way out.
// `docker kill` is avoided because it may count as a stop the restart policy honors.
func killControl(t *testing.T) {
	t.Helper()
	output, err := run(t, "docker", "inspect", "--format", "{{.State.Pid}}", controlContainer)
	if err != nil {
		t.Fatalf("inspect control: %v %s", err, output)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil || pid < 2 {
		t.Fatalf("control's process %q: %v", output, err)
	}
	if err = syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill control: %v", err)
	}
}

// controlReturns waits until the restart policy alone has brought control back ready.
func (e environment) controlReturns(t *testing.T, restartsBefore int) time.Duration {
	t.Helper()
	took := waitFor(t, "control to return", crashReturn, func() bool {
		if controlRestarts(t) <= restartsBefore {
			return false
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, adminOrigin+"/health/ready", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := e.newBrowser(t, adminOrigin).client.Do(request)
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
	t.Logf("control answered ready %s after the kill", took.Round(100*time.Millisecond))
	return took
}

// getImport reads an import the way the console does.
func getImport(t *testing.T, admin *browser, id string) modelImport {
	t.Helper()
	var out modelImport
	status, raw := admin.request(t, http.MethodGet, importsPath+"/"+id, nil, "")
	decode(t, status, http.StatusOK, raw, &out)
	return out
}

// uploadChunk sends one chunk. Unlike browser.request it returns a broken connection,
// which is what a chunk in flight gets when control dies.
func (b *browser) uploadChunk(ctx context.Context, id, csrf string, offset int64, chunk []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.origin+importsPath+"/"+id+"/chunks", bytes.NewReader(chunk))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(chunk)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Origin", b.origin)
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("X-Chunk-Offset", strconv.FormatInt(offset, 10))
	request.Header.Set("X-Chunk-SHA256", hex.EncodeToString(digest[:]))
	response, err := b.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("chunk at %d: %d %s", offset, response.StatusCode, raw)
	}
	return nil
}

// upload sends the bundle from offset until the end, or until a call fails, and says
// where it stopped. progress sees each offset the server accepted.
func (b *browser) upload(ctx context.Context, bundle *os.File, id, csrf string, offset, chunkBytes, total int64, progress func(int64)) (int64, error) {
	buffer := make([]byte, chunkBytes)
	for offset < total {
		size := min(chunkBytes, total-offset)
		if _, err := bundle.ReadAt(buffer[:size], offset); err != nil {
			return offset, err
		}
		if err := b.uploadChunk(ctx, id, csrf, offset, buffer[:size]); err != nil {
			return offset, err
		}
		offset += size
		progress(offset)
	}
	return offset, nil
}

// partlyWritten reports a file of the stage still open for writing: extraction makes
// each file read-only only when all of it is on disk.
func partlyWritten(stage string) bool {
	entries, err := os.ReadDir(stage)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && info.Mode().Perm() == 0o600 && info.Size() > 0 {
			return true
		}
	}
	return false
}

func TestControlCrash(t *testing.T) {
	if os.Getenv("OWNSOLID_E2E_LOAD") == "" {
		t.Skip("needs OWNSOLID_E2E_LOAD, and OWNSOLID_E2E_RUN=TestControlCrash in run-single-node.sh")
	}
	archive := os.Getenv("OWNSOLID_E2E_MODEL_ARCHIVE")
	if archive == "" {
		t.Fatal("OWNSOLID_E2E_MODEL_ARCHIVE names no model bundle")
	}
	e := loadEnvironment(t)
	agents := loadAgents
	if value := os.Getenv("OWNSOLID_E2E_SIMULATED_AGENTS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 100 {
			t.Fatalf("OWNSOLID_E2E_SIMULATED_AGENTS=%q", value)
		}
		agents = n
	}
	bundle, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	info, err := bundle.Stat()
	if err != nil {
		t.Fatal(err)
	}
	total := info.Size()

	e.apply(t, e.configurationWith(t, t.TempDir(), "single-node", e.controlImage, fleetLimits+crashLimits))
	admin, csrf := e.adminSession(t, e.password(t))
	e.installLicense(t, admin, csrf)
	fleet := e.enrollFleet(t, admin, csrf, agents, map[string]any{
		"duration_seconds": crashSeconds, "long_task_seconds": crashLongTaskLength, "window_seconds": 60,
		"faults": []map[string]any{{"kind": "long_tasks", "at_seconds": crashLongTasksAt, "percent": 100, "duration_seconds": crashLongTaskWindow}},
	})
	targets := fleet.targets()
	fleet.start(t, 90*time.Second)

	var catalog struct {
		CatalogDigest string `json:"catalog_digest"`
		ChunkBytes    int64  `json:"chunk_bytes"`
	}
	workspace := e.sql(t, "SELECT id FROM workspaces ORDER BY created_at LIMIT 1")
	status, raw := admin.request(t, http.MethodGet, catalogPath, nil, "")
	decode(t, status, http.StatusOK, raw, &catalog)
	var started modelImport
	status, raw = admin.requestWith(t, http.MethodPost, importsPath, map[string]any{
		"workspace_id": workspace, "name": "Survives a crash", "catalog_digest": catalog.CatalogDigest,
		"expected_bytes": total, "license_acknowledged": true, "reason": "Crash recovery drill",
	}, admin.csrfToken(t), map[string]string{"Idempotency-Key": newUUID(t)})
	decode(t, status, http.StatusCreated, raw, &started)
	id := started.ID
	stages := filepath.Join(e.root, "data", "models", "imports", id)

	t.Run("an upload cut off by a crash resumes from what the server kept", func(t *testing.T) {
		restarts := controlRestarts(t)
		killAt, killed := total/crashUploadFraction, false
		stopped, err := admin.upload(t.Context(), bundle, id, admin.csrfToken(t), 0, catalog.ChunkBytes, total, func(offset int64) {
			if offset >= killAt && !killed {
				killed = true
				killControl(t)
			}
		})
		if err == nil || !killed {
			t.Fatalf("the upload ran through the kill: stopped at %d, killed %t, error %v", stopped, killed, err)
		}
		t.Logf("the upload broke at %d of %d: %v", stopped, total, err)
		e.controlReturns(t, restarts)
		current := getImport(t, admin, id)
		if current.State != "uploading" || current.UploadedBytes < stopped-catalog.ChunkBytes || current.UploadedBytes > stopped+catalog.ChunkBytes {
			t.Fatalf("after the crash: %+v, client stopped at %d", current, stopped)
		}
		end, err := admin.upload(t.Context(), bundle, id, admin.csrfToken(t), current.UploadedBytes, catalog.ChunkBytes, total, func(int64) {})
		if err != nil || end != total {
			t.Fatalf("resume from %d: stopped at %d: %v", current.UploadedBytes, end, err)
		}
		if got := getImport(t, admin, id); got.UploadedBytes != total {
			t.Fatalf("after the resume: %+v", got)
		}
	})

	fleet.at(crashLongTasksAt + 2)
	long := createTasks(t, admin, admin.csrfToken(t), targets)
	// A node runs one task at a time, so the repeated write goes to a node with none.
	key := newUUID(t)
	var accepted struct {
		ID string `json:"id"`
	}
	path := nodesPath + "/" + fleet.nodes[1].NodeID + "/tasks"
	body := map[string]string{"operation": "collect_facts.v1"}
	status, raw = admin.requestWith(t, http.MethodPost, path, body, admin.csrfToken(t), map[string]string{"Idempotency-Key": key})
	decode(t, status, http.StatusAccepted, raw, &accepted)
	long = append(long, accepted.ID)
	list := "'" + strings.Join(long, "','") + "'"
	waitFor(t, "the long tasks to run", 30*time.Second, func() bool {
		return e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+list+") AND state='running'") == strconv.Itoa(len(long))
	})

	var killedAt time.Time
	t.Run("a crash while extracting leaves running tasks and the import recoverable", func(t *testing.T) {
		restarts := controlRestarts(t)
		var queued modelImport
		status, raw := admin.request(t, http.MethodPost, importsPath+"/"+id+"/complete", map[string]string{"reason": "Crash recovery drill"}, admin.csrfToken(t))
		decode(t, status, http.StatusAccepted, raw, &queued)
		stage := filepath.Join(stages, "stage-1")
		waitFor(t, "extraction to start", time.Minute, func() bool { return partlyWritten(stage) })
		killControl(t)
		killedAt = time.Now()
		if !partlyWritten(stage) {
			t.Fatal("the stage was not part-written when control died")
		}
		e.controlReturns(t, restarts)
	})

	t.Run("the next attempt publishes the model and spends one attempt", func(t *testing.T) {
		var current modelImport
		waitFor(t, "the import to finish", crashImport, func() bool {
			current = getImport(t, admin, id)
			return current.State == "ready" || current.State == "failed"
		})
		if current.State != "ready" || current.Attempts != 2 || current.ErrorCode != "" {
			t.Fatalf("after recovery: %+v", current)
		}
		if _, err := os.Lstat(filepath.Join(stages, "stage-1")); !os.IsNotExist(err) {
			t.Fatalf("the stopped attempt's files remain: %v", err)
		}
		if n := e.sql(t, "SELECT count(*) FROM model_versions WHERE id='"+id+"' AND state='ready'"); n != "1" {
			t.Fatalf("%s ready models", n)
		}
		for action, want := range map[string]string{"model.import_verification_started": "2", "model.import_ready": "1", "model.import_failed": "0"} {
			if n := e.sql(t, "SELECT count(*) FROM audit_events WHERE action='"+action+"' AND target_id='"+id+"'"); n != want {
				t.Errorf("%s: %s audit events, want %s", action, n, want)
			}
		}
	})

	t.Run("tasks running through the crash finish on their first attempt", func(t *testing.T) {
		e.waitForTasks(t, "tasks through the crash", long, time.Duration(crashLongTaskLength)*time.Second+time.Minute)
		if n := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+list+") AND attempt=1 AND acknowledged_attempt=1"); n != strconv.Itoa(len(long)) {
			t.Fatalf("%s of %d on their first attempt", n, len(long))
		}
	})

	t.Run("a write repeated after the crash finds the same task", func(t *testing.T) {
		var again struct {
			ID string `json:"id"`
		}
		status, raw := admin.requestWith(t, http.MethodPost, path, body, admin.csrfToken(t), map[string]string{"Idempotency-Key": key})
		if status != http.StatusOK && status != http.StatusAccepted {
			t.Fatalf("repeat: %d %s", status, raw)
		}
		decode(t, status, status, raw, &again)
		if again.ID != accepted.ID {
			t.Fatalf("repeat made task %s, first was %s", again.ID, accepted.ID)
		}
	})

	t.Run("every agent comes back after the crash", func(t *testing.T) {
		waitFor(t, "the fleet", loadReconnect, func() bool { return e.heardSince(t, fleet, killedAt) == len(fleet.nodes) })
		t.Logf("every agent had been heard from when checked %s after the kill", time.Since(killedAt).Round(time.Second))
	})

	t.Run("nothing left the host", e.egress.assertIsolated)
}
