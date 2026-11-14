//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Only adapter/model bytes are real native producer artifacts. Production identity/hardware,
// scope and expected-case envelopes are explicitly synthetic; no NVIDIA computation is asserted.
func preparationOutputFixture(t *testing.T) (preparationScenario, modelpreparations.Preparation, agentwire.Lease, []byte) {
	t.Helper()
	return preparationOutputFixtureWithAccuracy(t, wire.DefaultConfiguration().MinimumAccuracyBasisPoints)
}

// This helper changes the configured gate intent before creation, never an existing preparation.
func preparationOutputFixtureWithAccuracy(t *testing.T, minimumAccuracy int64) (preparationScenario, modelpreparations.Preparation, agentwire.Lease, []byte) {
	return preparationOutputFixtureBeforeClaim(t, minimumAccuracy, nil)
}

// Install large manufacturer-owned base bytes before a synthetic agent claims
// its ordinary live lease, matching an already-installed real node model.
func preparationOutputFixtureBeforeClaim(t *testing.T, minimumAccuracy int64, prepare func(preparationScenario, modelpreparations.Preparation)) (preparationScenario, modelpreparations.Preparation, agentwire.Lease, []byte) {
	t.Helper()
	g := newPreparationScenario(t)
	g.input.Configuration.MinimumAccuracyBasisPoints = minimumAccuracy
	p := preparationDispatched(t, g)
	if prepare != nil {
		prepare(g, p)
	}
	l := preparationClaimed(t, g)
	ctx := context.Background()
	base := os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	for _, name := range []string{"config.json", "tokenizer_config.json"} {
		raw, e := os.ReadFile(filepath.Join(base, name))
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(g.f.s.Models.RootDirectory, "objects", l.Preparation.ModelID, name)
		if existing, e := os.ReadFile(path); e == nil {
			if !bytes.Equal(existing, raw) {
				t.Fatal("fixture base source mismatch")
			}
			continue
		}
		if e = os.WriteFile(path, raw, 0444); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(path, 0444); e != nil {
			t.Fatal(e)
		}
	}
	events := preparationEvents(p.ID, *p.CurrentAttemptID, 3)
	if _, e := g.service.RecordEvents(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, events); e != nil {
		t.Fatal(e)
	}
	result, e := os.ReadFile(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var report wire.PreparedResult
	if json.Unmarshal(result, &report) != nil || report.Status != "prepared" || report.RowCount != 3 {
		t.Fatal("actual native prepared report required")
	}
	c := l.Preparation
	requestBytes, e := c.RequestBytes()
	if e != nil {
		t.Fatal(e)
	}
	report.TenantID = c.Scope.TenantID
	report.WorkspaceID = c.Scope.WorkspaceID
	report.PreparationID = c.Scope.PreparationID
	report.AttemptID = c.AttemptID
	report.TrainingJobID = c.TrainingJobID
	report.TrainingAttemptID = c.TrainingAttemptID
	report.HeldoutID = c.HeldoutID
	report.HeldoutSHA256 = c.Inputs.Heldout.SHA256
	report.Configuration = c.Configuration
	report.ConfigurationSHA256 = c.ConfigurationSHA256
	report.TrainingConfigurationSHA256 = c.TrainingConfigurationSHA256
	report.AdapterFiles = c.Inputs.AdapterFiles
	report.Identity = c.Recipe.Identity
	report.Resources = c.Recipe.Resources
	report.Device = c.Configuration.Device
	report.ComputationDtype = "bfloat16"
	report.MemoryEnforcement = "linux_cgroup_memory_cpu_fsize_wall"
	report.RequestSHA256 = digest(requestBytes)
	file, _, e := g.f.s.Datasets.OpenPublishedSource(ctx, g.version, c.Recipe.DataLimits.MaximumBytes)
	if e != nil {
		t.Fatal(e)
	}
	scanner := bufio.NewScanner(file)
	index := 0
	for scanner.Scan() {
		row, e := datawire.DecodeRecord(scanner.Bytes(), c.Recipe.DataLimits)
		if e != nil || index >= len(report.Cases) {
			file.Close()
			t.Fatal("actual heldout case", e)
		}
		report.Cases[index].SourceID = row.SourceID
		report.Cases[index].PromptSHA256 = digest([]byte(row.Prompt))
		report.Cases[index].Expected = row.Completion
		index++
	}
	file.Close()
	if scanner.Err() != nil || index != 3 {
		t.Fatal("actual heldout count")
	}
	// Recompute the explicitly synthetic envelope for its newly bound cases; preserve actual output predictions.
	blank := func() wire.Score {
		return wire.Score{Rows: 3, Confusion: [][]int64{{0, 0, 0, 0}, {0, 0, 0, 0}, {0, 0, 0, 0}}}
	}
	report.Base, report.Candidate = blank(), blank()
	labels := wire.Labels()
	for _, row := range report.Cases {
		expected := 0
		for i, label := range labels {
			if label == row.Expected {
				expected = i
			}
		}
		for i, prediction := range []wire.Prediction{row.Base, row.Candidate} {
			actual := len(labels)
			if prediction.Label != nil {
				for j, label := range labels {
					if label == *prediction.Label {
						actual = j
					}
				}
			}
			score := &report.Base
			if i == 1 {
				score = &report.Candidate
			}
			score.Confusion[expected][actual]++
			if actual == expected {
				score.Correct++
			}
		}
	}
	report.GatePassed, e = wire.QualityGate(report.Base.Correct, report.Candidate.Correct, report.RowCount, c.Configuration.MinimumAccuracyBasisPoints, c.Configuration.MaximumRegressionBasisPoints)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(report)
	if e != nil {
		t.Fatal(e)
	}
	p, e = g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return g, p, l, raw
}

func TestActualCpuModelPreparationUploadIntentExactProvenanceReplayAndChunkAccounting(t *testing.T) {
	g, p, l, metadata := preparationOutputFixture(t)
	ctx := context.Background()
	u, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata)
	if e != nil || u.PreparationID != p.ID || u.AttemptID != *p.CurrentAttemptID || len(u.Files) != 9 || u.State != "uploading" || u.ReceivedBytes != 0 {
		t.Fatal("real immutable prepared-report intent", e)
	}
	again, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata)
	if e != nil || again.ID != u.ID {
		t.Fatal("exact metadata replay", e)
	}
	changed := append([]byte(" "), metadata...)
	_, e = g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, changed)
	expected(t, e, "MODEL_PREPARATION_UPLOAD_CONFLICT")
	if count(t, g.f, "SELECT count(*) FROM model_preparation_uploads") != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_upload_started'") != 1 {
		t.Fatal("duplicate intent/audit")
	}
	body, e := os.ReadFile(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "model", "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	request := wire.ChunkRequest{Asset: "config.json", Offset: 0, Length: int64(len(body)), SHA256: digest(body)}
	accepted, e := g.service.UploadChunk(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID, request, bytes.NewReader(body))
	if e != nil || accepted.Duplicate || accepted.ReceivedBytes != int64(len(body)) {
		t.Fatal("real current-authority chunk accounting", e)
	}
	accepted, e = g.service.UploadChunk(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID, request, bytes.NewReader(body))
	if e != nil || !accepted.Duplicate || accepted.ReceivedBytes != int64(len(body)) {
		t.Fatal("exact body replay accounting", e)
	}
	if _, e = g.service.UploadChunk(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID, request, bytes.NewReader(bytes.Repeat([]byte("x"), len(body)))); e == nil {
		t.Fatal("replay trusted only metadata")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_upload_chunks WHERE upload_id=$1", u.ID) != 1 {
		t.Fatal("chunk replay double counted")
	}
}

func TestActualCpuModelPreparationUploadAuditRollbackLeavesNoBusinessIntent(t *testing.T) {
	g, _, l, metadata := preparationOutputFixture(t)
	ctx := context.Background()
	if _, e := g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_upload_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_upload_started' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_UPLOAD_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_upload_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_upload_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata); e == nil {
		t.Fatal("upload audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_uploads") != 0 {
		t.Fatal("partial upload intent survived audit rollback")
	}
	if _, e := g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_upload_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata); e != nil {
		t.Fatal("exact retry after audit repair", e)
	}
}

type preparationChunkReader struct {
	entered, release chan struct{}
	once             sync.Once
	body             *bytes.Reader
}

func (r *preparationChunkReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.body.Read(p)
}

func TestActualCpuModelPreparationChunkStreamRechecksRevokedRoleWithoutHoldingBusinessLock(t *testing.T) {
	g, _, l, metadata := preparationOutputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata)
	if e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "model", "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	reader := &preparationChunkReader{entered: make(chan struct{}), release: make(chan struct{}), body: bytes.NewReader(body)}
	var once sync.Once
	release := func() { once.Do(func() { close(reader.release) }) }
	t.Cleanup(release)
	done := make(chan error, 1)
	go func() {
		_, e := g.service.UploadChunk(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID, wire.ChunkRequest{Asset: "config.json", Offset: 0, Length: int64(len(body)), SHA256: digest(body)}, reader)
		done <- e
	}()
	select {
	case <-reader.entered:
	case <-ctx.Done():
		t.Fatal("actual stream did not start")
	}
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", g.input.WorkspaceID, g.f.portal.Me.User.ID); e != nil {
		release()
		t.Fatal("stream retained authority locks", e)
	}
	release()
	select {
	case e := <-done:
		expected(t, e, "ACCESS_DENIED")
	case <-ctx.Done():
		t.Fatal("stream did not join")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_upload_chunks WHERE upload_id=$1", u.ID) != 0 {
		t.Fatal("revoked actor committed a received range")
	}
}

var _ io.Reader = (*preparationChunkReader)(nil)
