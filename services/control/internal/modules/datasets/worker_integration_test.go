//go:build dataset_processor_integration

package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/url"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"syscall"
	"testing"
	"time"
)

func workerScenario(t *testing.T) (*Service, identity.Auth, identity.Auth) {
	t.Helper()
	processor, path := nativeProcessor(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	p := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	if e := InitializeStore(path, a.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := OpenStore(path, a.Me.Workspace.TenantID, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	return &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}, Store: store, Processor: processor}, a, p
}
func importFixture(t *testing.T, s *Service, a identity.Auth, raw string) (Dataset, Draft) {
	t.Helper()
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	data, e := s.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "Actual native dataset " + uuid.NewString(), Origin: "Synthetic isolated local source", License: "Synthetic CC0", RightsAsserted: true, Reason: "Actual processor integration"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256([]byte(raw))
	draft, e := s.CreateDraft(ctx, a, workspace, data.ID, DraftRequest{SourceName: "fixture.jsonl", ExpectedBytes: int64(len(raw)), SourceSHA256: hex.EncodeToString(sum[:]), Reason: "Actual source import"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Upload(ctx, a, workspace, data.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, bytes.NewBufferString(raw), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if draft.State != "validating" || draft.TotalRows != nil {
		t.Fatal("queued import presented as validated")
	}
	return data, draft
}
func process(t *testing.T, s *Service) {
	t.Helper()
	worked, e := s.ProcessNext(context.Background())
	if e != nil || !worked {
		t.Fatalf("actual worker %v %v", worked, e)
	}
}
func TestActualGoPostgresPythonImportRepairPublishImmutableFiles(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, draft := importFixture(t, s, a, row("source-a", "Hello \u4e2d\u6587", "First")+"{bad}\n"+row("source-b", "Other", "Second"))
	process(t, s)
	draft, e := s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	if draft.State != "editing" || draft.InvalidRows == nil || *draft.InvalidRows != 1 || draft.TotalRows == nil || *draft.TotalRows != 3 {
		t.Fatalf("inspection not committed %+v", draft)
	}
	var rowID string
	if e = s.DB.QueryRow(ctx, "SELECT id::text FROM dataset_rows WHERE draft_id=$1 AND line_number=2", draft.ID).Scan(&rowID); e != nil {
		t.Fatal(e)
	}
	changed, e := s.ReplaceRow(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID, rowID, RowRequest{ExpectedRevision: draft.Revision, RowRevision: 1, SourceID: "source-c", Prompt: "Repaired", Completion: "Valid", Reason: "Repair invalid JSON line"}, uuid.NewString())
	if e != nil || changed.Revision != 2 {
		t.Fatal("repair", e)
	}
	if _, e = s.ReplaceRow(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID, rowID, RowRequest{ExpectedRevision: draft.Revision, RowRevision: 1, SourceID: "source-c", Prompt: "Wrong replay", Completion: "Wrong", Reason: "Stale edit"}, uuid.NewString()); e == nil || e.Error() != "DATASET_REVISION_CONFLICT" {
		t.Fatalf("stale edit %v", e)
	}
	draft, e = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID)
	if e != nil || *draft.InvalidRows != 0 || draft.Revision != 2 {
		t.Fatal("repair counts", e)
	}
	job, e := s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID, PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Publish synthetic clean dataset"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AppendRow(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID, RowRequest{ExpectedRevision: draft.Revision, SourceID: "late", Prompt: "Must not persist", Completion: "No", Reason: "Concurrent publication edit"}, uuid.NewString()); e == nil || e.Error() != "DATASET_DRAFT_BUSY" {
		t.Fatalf("queued publication edit %v", e)
	}
	process(t, s)
	job, e = s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, job.ID)
	if e != nil || job.State != "succeeded" {
		t.Fatalf("publication %+v %v", job, e)
	}
	draft, e = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID)
	if e != nil || draft.State != "published" || draft.PublishedVersionID == nil {
		t.Fatal("published draft", e)
	}
	var manifestRaw []byte
	var mapping, effective, attemptID string
	if e = s.DB.QueryRow(ctx, "SELECT manifest,row_mapping_sha256,effective_source_sha256,attempt_id::text FROM dataset_versions WHERE id=$1", *job.VersionID).Scan(&manifestRaw, &mapping, &effective, &attemptID); e != nil {
		t.Fatal(e)
	}
	var versionManifest = *newManifest(t, manifestRaw)
	if versionManifest.TrainRows+versionManifest.ValidationRows != 3 || effective == draft.SourceSHA256 {
		t.Fatal("effective snapshot mislabeled as original source")
	}
	if e = s.Store.VerifyVersion(*job.VersionID, draft.ID, job.ID, attemptID, effective, mapping, versionManifest); e != nil {
		t.Fatal(e)
	}
	var histories, audits int
	s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_row_history WHERE draft_id=$1", draft.ID).Scan(&histories)
	s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='dataset.version_published' AND target_id=$1", *job.VersionID).Scan(&audits)
	if histories != 4 || audits != 1 {
		t.Fatal("lineage/audit", histories, audits)
	}
	worked, e := s.ProcessNext(ctx)
	if worked || e != nil {
		t.Fatal("completed job reran", e)
	}
}

func newManifest(t *testing.T, raw []byte) *wire.Manifest {
	t.Helper()
	var manifest wire.Manifest
	if e := json.Unmarshal(raw, &manifest); e != nil {
		t.Fatal(e)
	}
	return &manifest
}

func TestActualPublicationCloneRetirementAndExportRetention(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	process(t, s)
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	job, e := s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, PublicationRequest{ExpectedRevision: d.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Publish original"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	original, e := s.GetVersion(ctx, a, a.Me.Workspace.ID, data.ID, *job.VersionID)
	if e != nil {
		t.Fatal(e)
	}
	cloneKey := uuid.NewString()
	clone, e := s.CloneVersion(ctx, a, a.Me.Workspace.ID, data.ID, original.ID, "Clone immutable original", cloneKey, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if clone.ParentVersionID == nil || *clone.ParentVersionID != original.ID {
		t.Fatal("clone parent missing")
	}
	process(t, s)
	replay, e := s.CloneVersion(ctx, a, a.Me.Workspace.ID, data.ID, original.ID, "Clone immutable original", cloneKey, uuid.NewString())
	if e != nil || replay.ID != clone.ID || replay.State != "editing" {
		t.Fatal("clone replay reset draft", e)
	}
	retired, e := s.RetireVersion(ctx, a, a.Me.Workspace.ID, data.ID, original.ID, "Retain existing artifacts", uuid.NewString())
	if e != nil || retired.RetiredAt == nil {
		t.Fatal(e)
	}
	if _, e = s.CloneVersion(ctx, a, a.Me.Workspace.ID, data.ID, original.ID, "Must not select retired", uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "DATASET_VERSION_RETIRED" {
		t.Fatalf("%v", e)
	}
	file, metadata, e := s.VersionFile(ctx, a, a.Me.Workspace.ID, data.ID, original.ID, "train.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	file.Close()
	if metadata.SizeBytes < 1 {
		t.Fatal("retired export lost")
	}
	data, e = s.Get(ctx, a, a.Me.Workspace.ID, data.ID)
	if e != nil {
		t.Fatal(e)
	}
	data, e = s.Retire(ctx, a, a.Me.Workspace.ID, data.ID, data.Revision, "Retain dataset history", uuid.NewString())
	if e != nil || data.RetiredAt == nil {
		t.Fatal(e)
	}
	file, _, e = s.VersionFile(ctx, a, a.Me.Workspace.ID, data.ID, original.ID, "samples.jsonl")
	if e != nil {
		t.Fatal("retirement removed files", e)
	}
	file.Close()
}
func TestActualQueuedCancellationAndRetryAfterChildFailure(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	jobID := *d.CurrentJobID
	cancelled, e := s.CancelJob(ctx, a, a.Me.Workspace.ID, data.ID, jobID, "Cancel before processing", uuid.NewString())
	if e != nil || cancelled.State != "cancelled" {
		t.Fatalf("%+v %v", cancelled, e)
	}
	worked, e := s.ProcessNext(ctx)
	if e != nil || worked {
		t.Fatal("cancelled queued job ran", e)
	}
	data, d = importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	actualScript := s.Processor.configuration.ProcessorFile
	fixtureProgram(t, s.Processor, "raise SystemExit(9)\n")
	process(t, s)
	failed, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, *d.CurrentJobID)
	if e != nil || failed.State != "failed" || !failed.Retryable {
		t.Fatalf("%+v %v", failed, e)
	}
	s.Processor.configuration.ProcessorFile = actualScript
	key := uuid.NewString()
	retry, e := s.RetryJob(ctx, a, a.Me.Workspace.ID, data.ID, failed.ID, "Retry repaired actual processor", key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.RetryJob(ctx, a, a.Me.Workspace.ID, data.ID, failed.ID, "Retry repaired actual processor", key, uuid.NewString())
	if e != nil || replay.ID != retry.ID {
		t.Fatal("retry intent", e)
	}
	// Advance only this isolated synthetic fixture's queued eligibility, never a production scheduler or clock.
	if _, e = s.DB.Exec(ctx, "UPDATE dataset_jobs SET not_before=clock_timestamp() WHERE id=$1", retry.ID); e != nil {
		t.Fatal(e)
	}
	process(t, s)
	completed, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, retry.ID)
	if e != nil || completed.State != "succeeded" {
		t.Fatalf("%+v %v", completed, e)
	}
}
func TestActualAuditRollbackThenFreshAttemptRecoversPreparedVersion(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	process(t, s)
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	job, e := s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, PublicationRequest{ExpectedRevision: d.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Exercise actual failed commit"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_dataset_publication() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='dataset.version_published' THEN RAISE EXCEPTION 'synthetic publication audit failure'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_dataset_publication BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_dataset_publication()`); e != nil {
		t.Fatal(e)
	}
	if worked, e := s.ProcessNext(ctx); !worked || e == nil {
		t.Fatal("audit failure was hidden")
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_versions WHERE dataset_id=$1", data.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("publication survived failed audit", e)
	}
	interrupted, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, job.ID)
	if e != nil || interrupted.State != "running" || interrupted.AttemptID == nil {
		t.Fatal("durable unfinished attempt", e)
	}
	if _, e = os.Stat(filepath.Join(s.Store.path, versionDirectory(*job.VersionID, *interrupted.AttemptID), "publication.json")); e != nil {
		t.Fatal("uncertain preparation was removed", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_dataset_publication ON audit_events;DROP FUNCTION reject_dataset_publication()"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE dataset_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", job.ID); e != nil {
		t.Fatal(e)
	}
	process(t, s)
	recovered, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, job.ID)
	if e != nil || recovered.State != "succeeded" || recovered.AttemptCount != 2 || *recovered.AttemptID == *interrupted.AttemptID {
		t.Fatalf("%+v %v", recovered, e)
	}
	var expired int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_job_attempts WHERE job_id=$1 AND state='expired'", job.ID).Scan(&expired); e != nil || expired != 1 {
		t.Fatal("attempt history", e)
	}
}

func blockingRealProcessor(t *testing.T, s *Service) {
	t.Helper()
	actual := s.Processor.configuration.ProcessorFile
	encoded, _ := json.Marshal(actual)
	fixtureProgram(t, s.Processor, "import json,os,sys,time,runpy\nr=json.load(open(sys.argv[2]));p=r['scope_directory'];f=os.open(p+'/started.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600);os.write(f,json.dumps({'pid':os.getpid()}).encode());os.close(f)\nwhile not os.path.exists(p+'/release'):time.sleep(0.01)\nrunpy.run_path("+string(encoded)+",run_name='__main__')\n")
}
func waitActualChild(t *testing.T, s *Service, job string) (string, int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var attemptID *string
		if s.DB.QueryRow(context.Background(), "SELECT attempt_id::text FROM dataset_jobs WHERE id=$1", job).Scan(&attemptID) == nil && attemptID != nil {
			scope := filepath.Join(s.Store.path, "attempts", job, *attemptID)
			raw, e := os.ReadFile(filepath.Join(scope, "started.json"))
			if e == nil {
				var value struct {
					PID int `json:"pid"`
				}
				if json.Unmarshal(raw, &value) == nil && value.PID > 0 {
					return scope, value.PID
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actual blocked child was not observed")
	return "", 0
}
func TestActualRunningCancellationStopsOwnedChildBeforeTerminalState(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	blockingRealProcessor(t, s)
	done := make(chan error, 1)
	go func() { _, e := s.ProcessNext(ctx); done <- e }()
	_, pid := waitActualChild(t, s, *d.CurrentJobID)
	if _, e := s.CancelJob(ctx, a, a.Me.Workspace.ID, data.ID, *d.CurrentJobID, "Stop owned actual child", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("running cancellation unbounded")
	}
	j, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, *d.CurrentJobID)
	if e != nil || j.State != "cancelled" {
		t.Fatalf("%+v %v", j, e)
	}
	if e = syscall.Kill(pid, 0); !errors.Is(e, syscall.ESRCH) {
		t.Fatal("child remained", e)
	}
	var count int
	s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_rows WHERE draft_id=$1", d.ID).Scan(&count)
	if count != 0 {
		t.Fatal("cancelled import committed rows")
	}
}
func TestActualPreparedPublicationRechecksAccountBeforeCommit(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	process(t, s)
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	j, e := s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, PublicationRequest{ExpectedRevision: d.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Recheck revoked publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	blockingRealProcessor(t, s)
	done := make(chan error, 1)
	go func() { _, e := s.ProcessNext(ctx); done <- e }()
	scope, _ := waitActualChild(t, s, j.ID)
	if _, e = s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(scope, "release"), []byte("continue actual processor"), 0600); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("current authority gate unbounded")
	}
	var state string
	var code *string
	if e = s.DB.QueryRow(ctx, "SELECT state,error_code FROM dataset_jobs WHERE id=$1", j.ID).Scan(&state, &code); e != nil || state != "failed" || code == nil || *code != "DATASET_ACTOR_UNAVAILABLE" {
		t.Fatal("revoked completion accepted", e, state, code)
	}
	var count int
	s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_versions WHERE dataset_id=$1", data.ID).Scan(&count)
	if count != 0 {
		t.Fatal("revoked actor published a version")
	}
}
func TestActualKeysetRowPagesAndConcurrentRevisionConflict(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second")+row("c", "three", "third"))
	process(t, s)
	first, e := s.RowPage(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, url.Values{"limit": {"1"}})
	if e != nil || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatal("first page", e)
	}
	next, e := s.RowPage(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, url.Values{"limit": {"1"}, "cursor": {*first.NextCursor}})
	if e != nil || len(next.Items) != 1 || next.Items[0].LineNumber != 2 {
		t.Fatal("next page", e)
	}
	if _, e = s.RowPage(ctx, a, a.Me.Workspace.ID, data.ID, uuid.NewString(), url.Values{"limit": {"1"}, "cursor": {*first.NextCursor}}); e == nil || e.Error() != "INVALID_CURSOR" {
		t.Fatalf("cursor scope %v", e)
	}
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, e := s.ReplaceRow(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, first.Items[0].ID, RowRequest{ExpectedRevision: d.Revision, RowRevision: first.Items[0].Revision, SourceID: "a", Prompt: "Revised", Completion: "answer", Reason: "Concurrent bounded edit"}, uuid.NewString())
			out <- e
		}()
	}
	passed, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		e := <-out
		if e == nil {
			passed++
		} else if e.Error() == "DATASET_REVISION_CONFLICT" {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatal("concurrent edit lost integrity", passed, conflicts)
	}
}

func TestActualFatalLineProvenanceSurvivesErrorTruncationAndRowLimit(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	policy := s.Identity.Config.Policy()
	policy.Datasets.Limits.MaximumErrors = 1
	s.Identity.Config.Runtime = &policy
	s.Processor.policy = policy.Datasets
	data, d := importFixture(t, s, a, "{bad}\n{bad}\n\xff\n")
	process(t, s)
	d, e := s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	if e != nil || !d.FatalSourceError || d.ErrorLineNumber == nil || *d.ErrorLineNumber != 3 || len(d.ValidationErrors) != 1 || !d.ErrorsTruncated {
		t.Fatalf("fatal encoding line lost %+v %v", d, e)
	}
	if _, e = s.AppendRow(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, RowRequest{ExpectedRevision: d.Revision, SourceID: "a", Prompt: "Cannot repair encoding silently", Completion: "No", Reason: "New source is required"}, uuid.NewString()); e == nil || e.Error() != "DATASET_CORRECTED_UPLOAD_REQUIRED" {
		t.Fatalf("%v", e)
	}
	policy.Datasets.Limits.MaximumRows = 2
	s.Processor.policy = policy.Datasets
	s.Identity.Config.Runtime = &policy
	data, d = importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second")+row("c", "three", "third"))
	process(t, s)
	d, e = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	if e != nil || d.ErrorLineNumber == nil || *d.ErrorLineNumber != 3 || !d.FatalSourceError || d.TotalRows != nil {
		t.Fatalf("fatal row bound %+v %v", d, e)
	}
	j, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, *d.CurrentJobID)
	if e != nil || j.ErrorLineNumber == nil || *j.ErrorLineNumber != 3 || j.State != "failed" || j.Retryable {
		t.Fatalf("fatal job %+v %v", j, e)
	}
}

func TestActualMembershipLifetimeReplacementRejectsPreparedImport(t *testing.T) {
	s, admin, _ := workerScenario(t)
	ctx := context.Background()
	password := fixture.Password()
	invitation, e := s.Identity.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "dataset-lifetime@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Identity.Register(ctx, fixture.InvitationToken(t, s.Identity, invitation.ID), "Lifetime fixture", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	member := fixture.Login(t, s.Identity, "dataset-lifetime@example.invalid", password, "portal")
	data, d := importFixture(t, s, member, row("a", "one", "first")+row("b", "two", "second"))
	blockingRealProcessor(t, s)
	done := make(chan error, 1)
	go func() { _, e := s.ProcessNext(ctx); done <- e }()
	scope, _ := waitActualChild(t, s, *d.CurrentJobID)
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", member.Me.Workspace.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", member.Me.Workspace.ID, member.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'member')", member.Me.Workspace.TenantID, member.Me.Workspace.ID, member.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(scope, "release"), []byte("continue actual processor"), 0600)
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("membership replacement completion timeout")
	}
	j, e := s.GetJob(ctx, admin, member.Me.Workspace.ID, data.ID, *d.CurrentJobID)
	if e != nil || j.State != "failed" || j.ErrorCode == nil || *j.ErrorCode != "DATASET_ACTOR_UNAVAILABLE" {
		t.Fatalf("membership lifetime reused %+v %v", j, e)
	}
	var count int
	s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_rows WHERE draft_id=$1", d.ID).Scan(&count)
	if count != 0 {
		t.Fatal("old membership committed rows")
	}
}
func TestActualExpiredAttemptCompetitionAcceptsOnlyFreshFence(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	blockingRealProcessor(t, s)
	oldDone := make(chan error, 1)
	go func() { _, e := s.ProcessNext(ctx); oldDone <- e }()
	oldScope, _ := waitActualChild(t, s, *d.CurrentJobID)
	// Advance only this isolated fixture's durable lease; each child and commit is real.
	if _, e := s.DB.Exec(ctx, "UPDATE dataset_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", *d.CurrentJobID); e != nil {
		t.Fatal(e)
	}
	freshDone := make(chan error, 1)
	go func() { _, e := s.ProcessNext(ctx); freshDone <- e }()
	var newScope string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		scope, _ := waitActualChild(t, s, *d.CurrentJobID)
		if scope != oldScope {
			newScope = scope
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if newScope == "" {
		t.Fatal("fresh fenced attempt not observed")
	}
	os.WriteFile(filepath.Join(newScope, "release"), []byte("accept current attempt"), 0600)
	os.WriteFile(filepath.Join(oldScope, "release"), []byte("old output must be stale"), 0600)
	select {
	case e := <-freshDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fresh completion timeout")
	}
	select {
	case e := <-oldDone:
		if e == nil || e.Error() != "DATASET_ATTEMPT_STALE" {
			t.Fatalf("old fence completion %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old child not bounded")
	}
	j, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, *d.CurrentJobID)
	if e != nil || j.State != "succeeded" || j.AttemptCount != 2 {
		t.Fatalf("%+v %v", j, e)
	}
	var rows, histories int
	s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_rows WHERE draft_id=$1", d.ID).Scan(&rows)
	s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_row_history WHERE draft_id=$1", d.ID).Scan(&histories)
	if rows != 2 || histories != 2 {
		t.Fatal("stale process duplicated import", rows, histories)
	}
}

func TestActualExpiredAttemptCannotCommitFailureAfterLeaseLoss(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	actualScript := s.Processor.configuration.ProcessorFile
	blockingRealProcessor(t, s)
	done := make(chan error, 1)
	go func() { _, e := s.ProcessNext(ctx); done <- e }()
	waitActualChild(t, s, *d.CurrentJobID)
	if _, e := s.DB.Exec(ctx, "UPDATE dataset_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", *d.CurrentJobID); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e == nil || e.Error() != "DATASET_ATTEMPT_STALE" {
			t.Fatalf("expired attempt committed terminal failure: %v", e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("expired child did not stop")
	}
	current, e := s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, *d.CurrentJobID)
	if e != nil || current.State != "running" {
		t.Fatalf("expired work lost recovery state %+v %v", current, e)
	}
	s.Processor.configuration.ProcessorFile = actualScript
	process(t, s)
	current, e = s.GetJob(ctx, a, a.Me.Workspace.ID, data.ID, *d.CurrentJobID)
	if e != nil || current.State != "succeeded" || current.AttemptCount != 2 {
		t.Fatalf("fresh recovery %+v %v", current, e)
	}
}

func TestCloneRetainsPreDedupSourceConnectivityAndNewVersionLineage(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("A", "duplicate", "answer")+row("B", "duplicate", "answer")+row("B", "linked", "other")+row("C", "independent", "third"))
	process(t, s)
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	input := PublicationRequest{ExpectedRevision: d.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Preserve source bridge on clone"}
	j, e := s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	original, e := s.GetVersion(ctx, a, a.Me.Workspace.ID, data.ID, *j.VersionID)
	if e != nil {
		t.Fatal(e)
	}
	clone, e := s.CloneVersion(ctx, a, a.Me.Workspace.ID, data.ID, original.ID, "Clone complete effective rows", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	clone, e = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, clone.ID)
	if e != nil || clone.TotalRows == nil || *clone.TotalRows != 4 || clone.SourceSHA256 != original.EffectiveSourceSHA256 {
		t.Fatalf("clone lost pre-dedup bridge %+v %v", clone, e)
	}
	input.ExpectedRevision = clone.Revision
	j, e = s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, clone.ID, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	next, e := s.GetVersion(ctx, a, a.Me.Workspace.ID, data.ID, *j.VersionID)
	if e != nil || next.ParentVersionID == nil || *next.ParentVersionID != original.ID || next.Ordinal != 2 || next.Manifest.SourceGroups != original.Manifest.SourceGroups || next.Manifest.DuplicateRows != original.Manifest.DuplicateRows || next.Manifest.ManifestFile.SHA256 != original.Manifest.ManifestFile.SHA256 {
		t.Fatalf("clone changed deterministic source grouping %+v %v", next, e)
	}
}

func TestActualAppendDeleteScopedRowsAndViewerPublishedExports(t *testing.T) {
	s, admin, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	process(t, s)
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	appended, e := s.AppendRow(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, RowRequest{ExpectedRevision: d.Revision, SourceID: "c", Prompt: "append", Completion: "third", Reason: "Append actual valid record"}, uuid.NewString())
	if e != nil || appended.LineNumber != 3 {
		t.Fatal("append", e)
	}
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	if e = s.DeleteRow(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, appended.ID, d.Revision, appended.Revision, "Tombstone owned draft record", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	if *d.TotalRows != 2 || *d.InvalidRows != 0 {
		t.Fatal("tombstone counts")
	}
	appended, e = s.AppendRow(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, RowRequest{ExpectedRevision: d.Revision, SourceID: "d", Prompt: "new position", Completion: "fourth", Reason: "Preserve deleted original position"}, uuid.NewString())
	if e != nil || appended.LineNumber != 4 {
		t.Fatal("deleted position reused", e)
	}
	other, foreign := importFixture(t, s, a, row("x", "other", "foreign")+row("y", "different", "foreign"))
	process(t, s)
	if _, e = s.ReplaceRow(ctx, a, a.Me.Workspace.ID, other.ID, foreign.ID, appended.ID, RowRequest{ExpectedRevision: 1, RowRevision: 1, SourceID: "wrong", Prompt: "wrong", Completion: "wrong", Reason: "Foreign row must fail"}, uuid.NewString()); e == nil || e.Error() != "DATASET_NOT_FOUND" {
		t.Fatalf("cross-dataset row %v", e)
	}
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	j, e := s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, PublicationRequest{ExpectedRevision: d.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Publish for authorized viewer"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	password := fixture.Password()
	invite, e := s.Identity.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "dataset-viewer@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Identity.Register(ctx, fixture.InvitationToken(t, s.Identity, invite.ID), "Dataset viewer", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	viewer := fixture.Login(t, s.Identity, "dataset-viewer@example.invalid", password, "portal")
	if _, e = s.Workspaces.ChangeMember(ctx, admin, a.Me.Workspace.ID, viewer.Me.User.ID, "viewer", 1, "Read published datasets only", uuid.NewString(), false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetDraft(ctx, viewer, a.Me.Workspace.ID, data.ID, d.ID); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatalf("viewer draft %v", e)
	}
	if _, e = s.Get(ctx, viewer, a.Me.Workspace.ID, other.ID); e == nil || e.Error() != "DATASET_NOT_FOUND" {
		t.Fatalf("viewer unpublished dataset %v", e)
	}
	if _, e = s.GetVersion(ctx, viewer, a.Me.Workspace.ID, data.ID, *j.VersionID); e != nil {
		t.Fatal("viewer version", e)
	}
	file, _, e := s.VersionFile(ctx, viewer, a.Me.Workspace.ID, data.ID, *j.VersionID, "train.jsonl")
	if e != nil {
		t.Fatal("viewer published export", e)
	}
	file.Close()
	page, e := s.DatasetPage(ctx, viewer, a.Me.Workspace.ID, url.Values{"limit": {"1"}})
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != data.ID {
		t.Fatal("viewer inventory", e)
	}
	if _, e = s.Workspaces.ChangeMember(ctx, admin, a.Me.Workspace.ID, viewer.Me.User.ID, "", 2, "Remove published access", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.VersionFile(ctx, viewer, a.Me.Workspace.ID, data.ID, *j.VersionID, "train.jsonl"); e == nil {
		t.Fatal("removed member exported retained data")
	}
}
