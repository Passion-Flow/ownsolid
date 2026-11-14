//go:build dataset_processor_integration

package datasets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestActualNearPromptPublishRetryCloneAndExactCompatibility(t *testing.T) {
	s, _, actor := workerScenario(t)
	ctx := t.Context()
	workspace := actor.Me.Workspace.ID
	var raw strings.Builder
	for _, record := range nearFixtureRows() {
		raw.WriteString(row(record.SourceID, record.Prompt, record.Completion))
	}
	data, draft := importFixture(t, s, actor, raw.String())
	process(t, s)
	draft, err := s.GetDraft(ctx, actor, workspace, data.ID, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Exercise frozen near prompt policy"}
	key := uuid.NewString()
	if _, err = s.PublishWithDeduplication(ctx, actor, workspace, data.ID, draft.ID, input, "arbitrary", key, uuid.NewString()); err == nil || err.Error() != "VALIDATION_FAILED" {
		t.Fatal("unknown opt-in policy was accepted", err)
	}
	job, err := s.PublishWithDeduplication(ctx, actor, workspace, data.ID, draft.ID, input, NearPromptV1, key, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.PublishWithDeduplication(ctx, actor, workspace, data.ID, draft.ID, input, NearPromptV1, key, uuid.NewString())
	if err != nil || replay.ID != job.ID {
		t.Fatal("opt-in creation replay changed job identity", err)
	}
	if _, err = s.Publish(ctx, actor, workspace, data.ID, draft.ID, input, key, uuid.NewString()); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("legacy replay replaced the frozen opt-in policy", err)
	}
	actual := s.Processor.configuration.ProcessorFile
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	fixtureProgram(t, s.Processor, "import json, pathlib, runpy, sys\nr=json.load(open(sys.argv[2]))\n(pathlib.Path(r['scope_directory'])/'near-duplicates.json').write_text('[]')\nrunpy.run_path("+string(encoded)+",run_name='__main__')\n")
	process(t, s)
	failed, err := s.GetJob(ctx, actor, workspace, data.ID, job.ID)
	if err != nil || failed.State != "failed" || !failed.Retryable || failed.ErrorCode == nil || *failed.ErrorCode != "DATASET_PROCESSOR_REQUEST_INVALID" {
		t.Fatalf("a changed protected plan did not fail: %+v, %v", failed, err)
	}
	if _, err = s.GetVersion(ctx, actor, workspace, data.ID, *job.VersionID); err == nil || err.Error() != "DATASET_NOT_FOUND" {
		t.Fatal("the failed attempt exposed a version", err)
	}
	s.Processor.configuration.ProcessorFile = actual
	retryKey := uuid.NewString()
	retry, err := s.RetryJob(ctx, actor, workspace, data.ID, job.ID, "Retry with the sealed plan", retryKey, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.RetryJob(ctx, actor, workspace, data.ID, job.ID, "Retry with the sealed plan", retryKey, uuid.NewString())
	if err != nil || repeated.ID != retry.ID || *retry.VersionID == *job.VersionID {
		t.Fatal("explicit retry did not preserve its identity and isolate the new version", err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE dataset_jobs SET not_before=clock_timestamp() WHERE id=$1", retry.ID); err != nil {
		t.Fatal(err)
	}
	process(t, s)
	version, err := s.GetVersion(ctx, actor, workspace, data.ID, *retry.VersionID)
	if err != nil || version.Manifest.TotalRows != 7 || version.Manifest.UniqueRows != 5 ||
		version.Manifest.DuplicateRows != 2 || version.Manifest.SourceGroups != 3 {
		t.Fatalf("retry did not apply the original near prompt policy: %+v, %v", version.Manifest, err)
	}
	assertReadback := func(version Version, asset string, read func(int64, []byte) error) {
		t.Helper()
		file, _, err := s.VersionFile(ctx, actor, workspace, data.ID, version.ID, asset)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err = scanLines(file, 4096, 20, read); err != nil {
			t.Fatal(err)
		}
	}
	var duplicates []duplicate
	assertReadback(version, "duplicates.jsonl", func(_ int64, raw []byte) error {
		var entry duplicate
		err := json.Unmarshal(raw, &entry)
		duplicates = append(duplicates, entry)
		return err
	})
	if len(duplicates) != 2 || duplicates[0].LineNumber != 2 || duplicates[1].LineNumber != 3 ||
		duplicates[0].KeptLineNumber != 1 || duplicates[1].KeptLineNumber != 1 ||
		duplicates[0].MatchRule != NearPromptV1 || duplicates[1].MatchRule != NearPromptV1 ||
		duplicates[0].ContentSHA256 != duplicates[1].ContentSHA256 {
		t.Fatalf("wrong original-line duplicate mapping: %+v", duplicates)
	}
	var assignments []assignment
	assertReadback(version, "assignments.jsonl", func(_ int64, raw []byte) error {
		var entry assignment
		err := json.Unmarshal(raw, &entry)
		assignments = append(assignments, entry)
		return err
	})
	if len(assignments) != 5 || assignments[0].LineNumber != 1 || assignments[1].LineNumber != 4 ||
		assignments[2].LineNumber != 5 || assignments[0].Split != assignments[1].Split ||
		assignments[0].Split != assignments[2].Split {
		t.Fatalf("different answers or a retained bridge endpoint leaked across the split: %+v", assignments)
	}
	versionPath := filepath.Join(s.Store.path, versionDirectory(version.ID, version.AttemptID))
	effective, err := os.ReadFile(filepath.Join(versionPath, "effective.jsonl"))
	if err != nil || string(effective) != raw.String() {
		t.Fatal("near-deduplication changed pre-deduplication source or positions", err)
	}

	clone, err := s.CloneVersion(ctx, actor, workspace, data.ID, version.ID, "Clone the full effective source", uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	process(t, s)
	clone, err = s.GetDraft(ctx, actor, workspace, data.ID, clone.ID)
	if err != nil || clone.TotalRows == nil || *clone.TotalRows != 7 {
		t.Fatal("clone lost pre-deduplication source rows", err)
	}
	input.ExpectedRevision = clone.Revision
	exact, err := s.Publish(ctx, actor, workspace, data.ID, clone.ID, input, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err = s.DB.QueryRow(ctx, "SELECT creation_fingerprint FROM dataset_jobs WHERE id=$1", exact.ID).Scan(&stored); err != nil ||
		!sameFingerprint(stored, fingerprint(input, actor.Me.User.ID)) {
		t.Fatal("default publication changed its existing request identity", err)
	}
	process(t, s)
	successor, err := s.GetVersion(ctx, actor, workspace, data.ID, *exact.VersionID)
	if err != nil || successor.ParentVersionID == nil || *successor.ParentVersionID != version.ID ||
		successor.Manifest.TotalRows != 7 || successor.Manifest.UniqueRows != 6 ||
		successor.Manifest.DuplicateRows != 1 || successor.EffectiveSourceSHA256 != version.EffectiveSourceSHA256 {
		t.Fatalf("default exact publication changed version or deduplication semantics: %+v, %v", successor, err)
	}
	assertReadback(successor, "duplicates.jsonl", func(_ int64, raw []byte) error {
		var entry duplicate
		if err := json.Unmarshal(raw, &entry); err != nil {
			return err
		}
		if entry.LineNumber != 3 || entry.KeptLineNumber != 2 || strings.Contains(string(raw), "match_rule") {
			t.Fatalf("legacy duplicate record changed shape: %s", raw)
		}
		return nil
	})
}
