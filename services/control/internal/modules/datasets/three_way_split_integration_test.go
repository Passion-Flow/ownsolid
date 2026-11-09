//go:build dataset_processor_integration

package datasets

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

const threeWaySeed = "00112233445566778899aabbccddeeff"

func threeWayDraft(t *testing.T, s *Service, actor identity.Auth, version Version) Draft {
	t.Helper()
	ctx := t.Context()
	clone, err := s.CloneVersion(ctx, actor, version.WorkspaceID, version.DatasetID, version.ID, "Reuse exact effective source", uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	process(t, s)
	clone, err = s.GetDraft(ctx, actor, version.WorkspaceID, version.DatasetID, clone.ID)
	if err != nil || clone.TotalRows == nil || *clone.TotalRows != version.Manifest.TotalRows {
		t.Fatalf("clone changed pre-deduplication rows: %+v, %v", clone, err)
	}
	return clone
}

func threeWayAsset(t *testing.T, s *Service, actor identity.Auth, version Version, asset string) []byte {
	t.Helper()
	file, metadata, err := s.VersionFile(t.Context(), actor, version.WorkspaceID, version.DatasetID, version.ID, asset)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil || metadata.SizeBytes != int64(len(raw)) || len(raw) == 0 {
		t.Fatalf("missing or incomplete %s: %v", asset, err)
	}
	return raw
}

func TestActualThreeWayPublicationRetryAndSnapshot(t *testing.T) {
	s, _, actor := workerScenario(t)
	ctx := t.Context()
	workspace := actor.Me.Workspace.ID
	var raw strings.Builder
	for _, record := range nearFixtureRows() {
		raw.WriteString(row(record.SourceID, record.Prompt, record.Completion))
	}
	raw.WriteString(row("a", "A short unrelated prompt from the same source", "separate response"))
	data, draft := importFixture(t, s, actor, raw.String())
	process(t, s)
	draft, err := s.GetDraft(ctx, actor, workspace, data.ID, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := PublicationRequest{ExpectedRevision: draft.Revision, Seed: threeWaySeed, ValidationBasisPoints: 5000, Reason: "Keep three connected partitions"}
	opts := PublicationOptions{TestBasisPoints: 4000}
	key := uuid.NewString()
	job, err := s.PublishWithOptions(ctx, actor, workspace, data.ID, draft.ID, input, opts, key, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.PublishWithOptions(ctx, actor, workspace, data.ID, draft.ID, input, opts, key, uuid.NewString())
	if err != nil || replay.ID != job.ID {
		t.Fatal("three-way request replay changed job identity", err)
	}
	if _, err = s.PublishWithOptions(ctx, actor, workspace, data.ID, draft.ID, input, PublicationOptions{TestBasisPoints: 3999}, key, uuid.NewString()); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("a changed test ratio replaced the queued request", err)
	}
	if _, err = s.DB.Exec(ctx, `CREATE FUNCTION reject_three_way_publication() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='dataset.version_published' THEN RAISE EXCEPTION 'synthetic publication audit failure'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_three_way_publication BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_three_way_publication()`); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.ProcessNext(ctx); !worked || err == nil {
		current, readErr := s.GetJob(ctx, actor, workspace, data.ID, job.ID)
		if current.ErrorCode != nil {
			t.Fatalf("publication failed before the audit fault: %s", *current.ErrorCode)
		}
		t.Fatal("the audit fault did not interrupt commit", err, readErr)
	}
	interrupted, err := s.GetJob(ctx, actor, workspace, data.ID, job.ID)
	if err != nil || interrupted.AttemptID == nil || interrupted.State != "running" {
		t.Fatal("first prepared attempt was not retained", err)
	}
	if _, err = s.GetVersion(ctx, actor, workspace, data.ID, *job.VersionID); err == nil || err.Error() != "DATASET_NOT_FOUND" {
		t.Fatal("interrupted attempt exposed a version", err)
	}
	first := filepath.Join(s.Store.path, versionDirectory(*job.VersionID, *interrupted.AttemptID))
	if _, err = s.DB.Exec(ctx, "DROP TRIGGER reject_three_way_publication ON audit_events; DROP FUNCTION reject_three_way_publication()"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE dataset_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", job.ID); err != nil {
		t.Fatal(err)
	}
	process(t, s)
	v2, err := s.GetVersion(ctx, actor, workspace, data.ID, *job.VersionID)
	if err != nil || v2.AttemptID == *interrupted.AttemptID || v2.Manifest.SchemaVersion != 2 ||
		v2.Manifest.TestBasisPoints != opts.TestBasisPoints || v2.Manifest.TotalRows != 8 ||
		v2.Manifest.DuplicateRows != 1 || v2.Manifest.SourceGroups != 3 ||
		v2.Manifest.ValidationGroups != 1 || v2.Manifest.TestGroups != 1 {
		t.Fatalf("invalid recovered three-way manifest: %+v, %v", v2, err)
	}
	for _, asset := range []string{"manifest.json", "train.jsonl", "validation.jsonl", "test.jsonl", "assignments.jsonl"} {
		before, err := os.ReadFile(filepath.Join(first, asset))
		if err != nil || !bytes.Equal(before, threeWayAsset(t, s, actor, v2, asset)) {
			t.Fatalf("retry changed prepared %s bytes: %v", asset, err)
		}
	}
	assigned := make(map[string]string)
	counts := make(map[string]int64)
	decoder := json.NewDecoder(bytes.NewReader(threeWayAsset(t, s, actor, v2, "assignments.jsonl")))
	for {
		var value assignment
		err := decoder.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if prior := assigned[value.SourceID]; prior != "" && prior != value.Split {
			t.Fatal("same source crossed partitions")
		}
		assigned[value.SourceID] = value.Split
		counts[value.Split]++
	}
	for _, source := range []string{"a", "b", "d", "e"} {
		if assigned[source] == "" || assigned[source] != assigned["a"] {
			t.Fatal("a retained near-prompt bridge crossed partitions")
		}
	}
	if assigned["c"] != "" || len(counts) != 3 || counts["train"] != v2.Manifest.TrainRows ||
		counts["validation"] != v2.Manifest.ValidationRows || counts["test"] != v2.Manifest.TestRows {
		t.Fatalf("partition assignments disagree with the manifest: %v", counts)
	}

	nearDraft := threeWayDraft(t, s, actor, v2)
	input.ExpectedRevision = nearDraft.Revision
	opts.Deduplication = NearPromptV1
	nearJob, err := s.PublishWithOptions(ctx, actor, workspace, data.ID, nearDraft.ID, input, opts, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	actualProcessor := s.Processor.configuration.ProcessorFile
	fixtureProgram(t, s.Processor, "raise SystemExit(9)\n")
	process(t, s)
	failed, err := s.GetJob(ctx, actor, workspace, data.ID, nearJob.ID)
	if err != nil || failed.State != "failed" || !failed.Retryable {
		t.Fatal("failed child did not leave a retryable publication", err)
	}
	s.Processor.configuration.ProcessorFile = actualProcessor
	retry, err := s.RetryJob(ctx, actor, workspace, data.ID, failed.ID, "Retry the three-way policy", uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE dataset_jobs SET not_before=clock_timestamp() WHERE id=$1", retry.ID); err != nil {
		t.Fatal(err)
	}
	process(t, s)
	nearVersion, err := s.GetVersion(ctx, actor, workspace, data.ID, *retry.VersionID)
	if err != nil || nearVersion.Manifest.SchemaVersion != 2 || nearVersion.Manifest.TestBasisPoints != opts.TestBasisPoints ||
		nearVersion.Manifest.DuplicateRows != 2 || nearVersion.Manifest.SourceGroups != 3 || nearVersion.EffectiveSourceSHA256 != v2.EffectiveSourceSHA256 {
		t.Fatalf("explicit retry lost the selected split or removal policy: %+v, %v", nearVersion, err)
	}

	legacyDraft := threeWayDraft(t, s, actor, v2)
	input.ExpectedRevision = legacyDraft.Revision
	legacyJob, err := s.Publish(ctx, actor, workspace, data.ID, legacyDraft.ID, input, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	process(t, s)
	legacy, err := s.GetVersion(ctx, actor, workspace, data.ID, *legacyJob.VersionID)
	if err != nil || legacy.Manifest.SchemaVersion != 1 || legacy.Manifest.TestRows != 0 || len(legacy.Manifest.Files) != 5 ||
		legacy.EffectiveSourceSHA256 != v2.EffectiveSourceSHA256 {
		t.Fatalf("default publication changed its original contract: %+v, %v", legacy, err)
	}
	for _, version := range []Version{legacy, v2} {
		var inputs TrainingInputs
		if err := s.transaction(ctx, actor, workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			var err error
			inputs, err = s.TrainingInputSnapshotWithin(ctx, q, w.TenantID, w.ID, data.ID, version.ID, false)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.VerifyTrainingInputSnapshot(ctx, inputs); err != nil ||
			inputs.Train.Path != "train.jsonl" || inputs.Validation.Path != "validation.jsonl" ||
			inputs.Version.ManifestSHA256 != version.ManifestSHA256 {
			t.Fatalf("training consumer did not receive its original asset projection: %+v, %v", inputs, err)
		}
		if file, _, err := s.OpenTrainingAssetContext(ctx, inputs, "test.jsonl"); err == nil {
			file.Close()
			t.Fatal("training transfer exposed the held-out test asset")
		}
	}

	snapshot := filepath.Join(privateSnapshotDirectory(t), "snapshot")
	if err := s.ExportFiles(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	fresh := &Service{DB: s.DB, Identity: s.Identity, Workspaces: s.Workspaces}
	target := privateSnapshotDirectory(t)
	if err := fresh.ImportFiles(ctx, snapshot, target); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(target, actor.Me.Workspace.TenantID, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	fresh.Store = restored
	if err := fresh.VerifyStored(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(threeWayAsset(t, fresh, actor, v2, "test.jsonl"), threeWayAsset(t, s, actor, v2, "test.jsonl")) {
		t.Fatal("snapshot restore changed the test asset")
	}
	fresh.Store = nil
	testPath := filepath.Join(snapshot, versionDirectory(v2.ID, v2.AttemptID), "test.jsonl")
	if err := os.WriteFile(testPath, []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fresh.ImportFiles(ctx, snapshot, privateSnapshotDirectory(t)); err == nil {
		t.Fatal("a corrupted test asset survived restore verification")
	}
}

func TestActualThreeWayPublicationRefusesTooFewConnectedGroups(t *testing.T) {
	s, _, actor := workerScenario(t)
	ctx := t.Context()
	workspace := actor.Me.Workspace.ID
	var raw strings.Builder
	for _, record := range nearFixtureRows()[:6] {
		raw.WriteString(row(record.SourceID, record.Prompt, record.Completion))
	}
	data, draft := importFixture(t, s, actor, raw.String())
	process(t, s)
	draft, err := s.GetDraft(ctx, actor, workspace, data.ID, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := PublicationRequest{ExpectedRevision: draft.Revision, Seed: threeWaySeed, ValidationBasisPoints: 5000, Reason: "Reject unsafe three-way assignment"}
	if _, err := s.PublishWithOptions(ctx, actor, workspace, data.ID, draft.ID, input, PublicationOptions{TestBasisPoints: 4001}, uuid.NewString(), uuid.NewString()); err == nil || err.Error() != "VALIDATION_FAILED" {
		t.Fatal("combined held-out ratio exceeded its ceiling", err)
	}
	job, err := s.PublishWithOptions(ctx, actor, workspace, data.ID, draft.ID, input, PublicationOptions{TestBasisPoints: 1000}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	process(t, s)
	failed, err := s.GetJob(ctx, actor, workspace, data.ID, job.ID)
	if err != nil || failed.State != "failed" || failed.Retryable || failed.ErrorCode == nil || *failed.ErrorCode != "DATASET_SPLIT_INSUFFICIENT_GROUPS" {
		if failed.ErrorCode != nil {
			t.Fatalf("expected insufficient groups, got %s (retryable %v): %v", *failed.ErrorCode, failed.Retryable, err)
		}
		t.Fatalf("two connected groups were published as three: %+v, %v", failed, err)
	}
	if _, err := s.GetVersion(ctx, actor, workspace, data.ID, *job.VersionID); err == nil || err.Error() != "DATASET_NOT_FOUND" {
		t.Fatal("insufficient groups exposed a version", err)
	}
}
