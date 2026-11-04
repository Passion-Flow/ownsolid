//go:build training_operator_integration && training_artifact_integration && training_recovery_integration

package training_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestActualNativeTrainingPairedLogicalRestoreRestartAndReadiness(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	var e error
	f, _, _ := acceptedSnapshotCheckpoint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	// Populate the complete approved immutable model; the node and production binding reports remain synthetic.
	modelRoot := f.s.Models.RootDirectory
	for _, asset := range agentwire.ApprovedModel().Files {
		path := filepath.Join(modelRoot, "objects", f.input.ModelID, asset.Path)
		if asset.Path == "config.json" {
			continue
		}
		input, e := os.Open(filepath.Join(os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY"), asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		output, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			input.Close()
			t.Fatal(e)
		}
		n, copyError := io.Copy(output, input)
		input.Close()
		if copyError == nil {
			copyError = output.Chmod(0444)
		}
		if copyError == nil {
			copyError = output.Sync()
		}
		closeError := output.Close()
		if copyError != nil || closeError != nil || n != asset.SizeBytes {
			t.Fatal("actual model copy failed", copyError, closeError)
		}
	}
	if e = f.s.Models.VerifyStoredVersions(ctx); e != nil {
		t.Fatal("complete actual approved model", e)
	}
	// The explicitly synthetic node has no live heartbeat loop during the full model copy.
	if _, e = f.s.DB.Exec(ctx, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp() WHERE id=$1", f.input.NodeID); e != nil {
		t.Fatal("refresh synthetic fixture node after actual model copy", e)
	}
	active := dispatched(t, f)
	oldLease := claimed(t, f)
	h := newNativeTrainingOperator(t, f)
	h.SetPair("", modelRoot, f.s.Identity.Config.Datasets.ArtifactRoot)
	if e = f.s.Store.Close(); e != nil {
		t.Fatal(e)
	}
	f.s.Store = nil
	if e = f.s.Datasets.Store.Close(); e != nil {
		t.Fatal(e)
	}
	backupParent := snapshotTestDirectory(t)
	for _, kind := range []string{"model", "dataset", "training"} {
		if e = h.Run("export-"+kind+"-files", "--backup-root", filepath.Join(backupParent, kind)); e != nil {
			t.Fatal("actual paired file export", kind, e)
		}
	}
	owned := fixture.OwnedPostgres(t, f.s.DB)
	schema := owned.Schema
	archive := owned.DumpSchema(t, ctx)
	if e = os.WriteFile(filepath.Join(backupParent, "database.dump"), archive, 0600); e != nil {
		t.Fatal(e)
	}
	restored := owned.RestoreFresh(t, ctx, f.s.DB, archive, true)
	target := restored.Config().ConnConfig.Database
	rawDSN := os.Getenv("PROJECT_ONE_TEST_DSN")
	if rawDSN == "" {
		b, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"))
		if e != nil {
			t.Fatal(e)
		}
		rawDSN = string(b)
	}
	dsn, e := url.Parse(strings.TrimSpace(rawDSN))
	if e != nil {
		t.Fatal(e)
	}
	dsn.Path = "/" + target
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	modelDestination, datasetDestination, trainingDestination := snapshotTestDirectory(t), snapshotTestDirectory(t), snapshotTestDirectory(t)
	h.SetTrainingRoot(trainingDestination)
	h.SetPair(dsn.String(), modelDestination, datasetDestination)
	if e = h.Run("invalidate-restored-auth"); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"model", "dataset", "training"} {
		arguments := []string{"--backup-root", filepath.Join(backupParent, kind)}
		if kind == "training" {
			arguments = append(arguments, "--training-store-root", trainingDestination, "--confirm-empty-training-store")
		}
		if e = h.Run("import-"+kind+"-files", arguments...); e != nil {
			t.Fatal("actual paired file import", kind, e)
		}
	}
	for _, name := range []string{"verify-model-store", "verify-dataset-store", "verify-training-files"} {
		if e = h.Run(name); e != nil {
			t.Fatal("actual restored integrity", name, e)
		}
	}
	var jobState, attemptState, leaseState string
	var nodeRevoked bool
	if e = restored.QueryRow(ctx, `SELECT j.state,a.state,g.state,n.credential_hash IS NULL FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id JOIN gpu_leases g ON g.training_job_id=j.id JOIN managed_nodes n ON n.id=j.current_node_id WHERE j.id=$1`, active.ID).Scan(&jobState, &attemptState, &leaseState, &nodeRevoked); e != nil {
		t.Fatal(e)
	}
	if jobState != "recovery_required" || attemptState != "recovery_required" || leaseState != "recovery_required" || !nodeRevoked {
		t.Fatal("restored authority or uncertain GPU released", jobState, attemptState, leaseState, nodeRevoked)
	}
	var accepted int
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM training_uploads WHERE state='accepted'").Scan(&accepted); e != nil || accepted != 2 {
		t.Fatal("accepted checkpoint/adapter history lost", e, accepted)
	}
	copyService := *f.s
	copyService.DB = restored
	identityCopy := *f.s.Identity
	identityCopy.DB = restored
	copyService.Identity = &identityCopy
	wsCopy := *f.s.Workspaces
	wsCopy.DB = restored
	wsCopy.Identity = &identityCopy
	copyService.Workspaces = &wsCopy
	nodeCopy := *f.s.Nodes
	nodeCopy.DB = restored
	nodeCopy.Identity = &identityCopy
	nodeCopy.Workspaces = &wsCopy
	copyService.Nodes = &nodeCopy
	modelCopy := *f.s.Models
	modelCopy.DB = restored
	modelCopy.Identity = &identityCopy
	modelCopy.Workspaces = &wsCopy
	copyService.Models = &modelCopy
	nodeCopy.Training = &copyService
	if _, e = copyService.RecordStop(ctx, f.input.NodeID, f.credential, oldLease.TaskID, oldLease.LeaseToken, stopFixture(t, f, oldLease), nil); e == nil || strings.Contains(e.Error(), "STORAGE_MISMATCH") || e.Error() == "STORAGE_UNAVAILABLE" {
		t.Fatal("restored stale node credential completed prior attempt")
	}
	configRaw, e := os.ReadFile(h.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(configRaw, &config) != nil {
		t.Fatal("native configuration")
	}
	ca, e := os.ReadFile(h.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	startStop := func() {
		t.Helper()
		process := exec.CommandContext(ctx, binary, "serve", "--config", h.Configuration)
		log, e := os.CreateTemp(backupParent, "native-service-")
		if e != nil {
			t.Fatal(e)
		}
		defer log.Close()
		process.Stdout = log
		process.Stderr = log
		if e = process.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		defer func() {
			process.Process.Signal(syscall.SIGTERM)
			select {
			case e := <-done:
				if e != nil {
					t.Error("native graceful shutdown", e)
				}
			case <-time.After(10 * time.Second):
				process.Process.Kill()
				<-done
				t.Error("native service shutdown timed out")
			}
		}()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			response, e := client.Get(h.Origin + "/health/ready")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("actual restored native readiness timeout")
	}
	startStop()
	startStop()
	var file string
	if e = restored.QueryRow(ctx, "SELECT storage_key FROM training_artifacts LIMIT 1").Scan(&file); e != nil {
		t.Fatal(e)
	}
	protected := filepath.Join(trainingDestination, file, "adapter_model.safetensors")
	preserved := protected + ".preserved"
	if e = os.Rename(protected, preserved); e != nil {
		t.Fatal(e)
	}
	invalid := exec.CommandContext(ctx, binary, "serve", "--config", h.Configuration)
	raw, e := invalid.CombinedOutput()
	if e == nil || !bytes.Contains(raw, []byte("TRAINING_STORAGE_DATA_INVALID")) {
		t.Fatal("missing accepted adapter did not fail startup", e, string(raw))
	}
	if e = os.Rename(preserved, protected); e != nil {
		t.Fatal(e)
	}
	startStop()
	t.Log("Actual approved model, published dataset, accepted CPU checkpoint/adapter files and PostgreSQL logical restore passed native startup/restart/integrity; old authority remains fenced and uncertain GPU retained. Node/GPU/production binding reports are synthetic; no NVIDIA claim.")
}
