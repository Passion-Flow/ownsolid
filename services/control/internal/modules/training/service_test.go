package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/teststorage"
	"github.com/google/uuid"
)

type scenario struct {
	s             *training.Service
	admin, portal identity.Auth
	input         training.CreateRequest
	credential    string
	password      string
}

func setup(t *testing.T) scenario {
	t.Helper()
	return setupOn(t, fixture.New(t))
}

// setupOn builds the scenario on an account fixture the caller configured.
func setupOn(t *testing.T, account *identity.Service) scenario {
	t.Helper()
	ctx := context.Background()
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	p := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	w := &workspaces.Service{DB: account.DB, Identity: account}
	n := &nodes.Service{DB: account.DB, Identity: account, Workspaces: w}
	root := filepath.Join(t.TempDir(), "datasets")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := datasets.InitializeStore(root, a.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(root, a.Me.Workspace.TenantID, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../../../runtimes/data-processing/process.py"))
	binary := os.Getenv("PROJECT_ONE_DATA_PYTHON")
	if binary == "" {
		t.Fatal("actual pinned PROJECT_ONE_DATA_PYTHON required")
	}
	processor, e := datasets.NewProcessor(config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}, config.DefaultDatasetPolicy())
	if e != nil {
		t.Fatal(e)
	}
	ds := &datasets.Service{DB: account.DB, Identity: account, Workspaces: w, Store: store, Processor: processor}
	account.Config.Datasets = config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}
	dataset, version := publishFixtureDataset(t, ds, p, a.Me.Workspace.ID)
	// Only node observations and model catalog metadata are synthetic in this business fixture.
	// Actual approved full-model compute/weights are verified by separate native training profiles.
	model := uuid.NewString()
	facts := agentwire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic", LogicalCPUs: 4, MemoryTotalBytes: 17179869184, DiskPath: "/", DiskTotalBytes: 107374182400, DiskAvailableBytes: 53687091200, ObservedAt: time.Now().UTC(), AgentVersion: agentwire.AgentVersion}
	join, e := n.MintForProfile(ctx, a, a.Me.Workspace.ID, "Synthetic training inventory", agentwire.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrolled, e := n.Enroll(ctx, agentwire.Enrollment{JoinToken: join.Token, Handshake: agentwire.Handshake{ProtocolVersion: agentwire.AIVersion, ExecutionProfile: agentwire.DockerAIProfile, Capabilities: agentwire.Capabilities(agentwire.DockerAIProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	node := enrolled.NodeID
	catalog, _ := json.Marshal(agentwire.ApprovedModel())
	if _, e = account.DB.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic model metadata','qwen3-0.6b',$4,$5,'ready',$6)`, model, a.Me.Workspace.TenantID, a.Me.Workspace.ID, agentwire.ModelCatalogDigest(), catalog, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	account.Config.Training = config.TrainingConfiguration{ArtifactRoot: filepath.Join(t.TempDir(), "training"), ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	teststorage.Observe(t, "dataset", account.Config.Datasets.ArtifactRoot)
	teststorage.Observe(t, "training", account.Config.Training.ArtifactRoot)

	s := &training.Service{DB: account.DB, Identity: account, Workspaces: w, Nodes: n, Models: &models.Service{DB: account.DB, Identity: account, Workspaces: w}, Datasets: ds}
	s.Inference = &deployments.Service{Nodes: n, Models: s.Models}
	n.Inference = s.Inference
	n.Training = s
	return scenario{s: s, admin: a, portal: p, credential: enrolled.Credential, password: password, input: training.CreateRequest{WorkspaceID: a.Me.Workspace.ID, NodeID: node, ModelID: model, DatasetID: dataset, DatasetVersionID: version, GPUUUID: "GPU-" + uuid.NewString(), Configuration: wire.DefaultConfiguration(), Reason: "Synthetic scoped training intent"}}
}

func publishFixtureDataset(t *testing.T, ds *datasets.Service, a identity.Auth, workspace string) (string, string) {
	t.Helper()
	ctx := t.Context()
	data, e := ds.Create(ctx, a, datasets.CreateRequest{WorkspaceID: workspace, Name: "Real native training dataset", Origin: "Synthetic local test records", License: "CC0 synthetic fixture", RightsAsserted: true, Reason: "Training input acceptance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var raw bytes.Buffer
	for i := 0; i < 10; i++ {
		b, _ := json.Marshal(datawire.Record{SourceID: uuid.NewString(), Prompt: "Synthetic prompt " + uuid.NewString(), Completion: "Synthetic completion"})
		raw.Write(b)
		raw.WriteByte('\n')
	}
	sum := sha256.Sum256(raw.Bytes())
	draft, e := ds.CreateDraft(ctx, a, workspace, data.ID, datasets.DraftRequest{SourceName: "synthetic.jsonl", ExpectedBytes: int64(raw.Len()), SourceSHA256: hex.EncodeToString(sum[:]), Reason: "Import actual local processor input"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ds.Upload(ctx, a, workspace, data.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, &raw, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if worked, e := ds.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual source inspection", e)
	}
	draft, e = ds.GetDraft(ctx, a, workspace, data.ID, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	publication, e := ds.Publish(ctx, a, workspace, data.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Publish actual immutable input"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := ds.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual publication", e)
	}
	publication, e = ds.GetJob(ctx, a, workspace, data.ID, publication.ID)
	if e != nil || publication.State != "succeeded" || publication.VersionID == nil {
		t.Fatal("actual published source", e)
	}
	return data.ID, *publication.VersionID
}

func expected(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s observed %v", code, e)
	}
}

func TestActualTrainingCreationFrozenIntentScopeAndQueuedCancellation(t *testing.T) {
	f := setup(t)
	s, a, input := f.s, f.portal, f.input
	ctx := context.Background()
	key := uuid.NewString()
	j, e := s.Create(ctx, a, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if j.State != "queued" || j.AttemptNumber != 0 || j.CurrentAttemptID != nil || j.AcceptedArtifactID != nil || j.ConfigurationSHA256 == "" {
		t.Fatal("false queued outcome")
	}
	replay, e := s.Create(ctx, a, input, key, uuid.NewString())
	if e != nil || replay.ID != j.ID {
		t.Fatal("idempotency", e)
	}
	changed := input
	changed.Configuration.MaximumSteps = 200
	_, e = s.Create(ctx, a, changed, key, uuid.NewString())
	expected(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = s.Create(ctx, f.admin, input, key, uuid.NewString())
	expected(t, e, "IDEMPOTENCY_CONFLICT")
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM gpu_leases").Scan(&count); e != nil || count != 0 {
		t.Fatal("queued job took physical GPU", e)
	}
	other, e := s.Workspaces.Create(ctx, f.admin, "Foreign training scope", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Get(ctx, f.admin, other.ID, j.ID)
	expected(t, e, "TRAINING_NOT_FOUND")
	if _, e = s.DB.Exec(ctx, "UPDATE training_jobs SET configuration_sha256=repeat('d',64) WHERE id=$1", j.ID); e == nil {
		t.Fatal("frozen training inputs changed")
	}
	_, e = s.Cancel(ctx, a, input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision + 1, Reason: "Stale cancel"}, uuid.NewString())
	expected(t, e, "TRAINING_REVISION_CONFLICT")
	cancelled, e := s.Cancel(ctx, a, input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Cancel queued owned job"}, uuid.NewString())
	if e != nil || cancelled.State != "cancelled" || cancelled.FinishedAt == nil {
		t.Fatal("queued cancellation", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action IN ('training.created','training.cancel_requested')", j.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal("audit/replay atomicity", count, e)
	}
}

func TestActualTrainingCreationConcurrencyAndAuditRollback(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	key := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan training.Job, 2)
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := f.s.Create(ctx, f.portal, f.input, key, uuid.NewString())
			results <- j
			errors <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for j := range results {
		if id != "" && id != j.ID {
			t.Fatal("concurrent replay created two jobs")
		}
		id = j.ID
	}
	if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.created' THEN RAISE EXCEPTION 'SYNTHETIC_TRAINING_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_training_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_training_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("audit failure reported success")
	}
	var count int
	if e := f.s.DB.QueryRow(ctx, "SELECT count(*) FROM training_jobs").Scan(&count); e != nil || count != 1 {
		t.Fatal("failed audit committed business state", count, e)
	}
}

func TestActualTrainingCurrentRolesEpochMembershipAndIsolation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	s := f.s
	j, e := s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString())
	expected(t, e, "ACCESS_DENIED")
	if _, e = s.Get(ctx, f.portal, f.input.WorkspaceID, j.ID); e != nil {
		t.Fatal("current viewer read", e)
	}
	if _, e = s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Creator remains current member"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, f.portal, f.input.WorkspaceID, j.ID); e == nil {
		t.Fatal("removed member retained read")
	}
	if _, e = s.Get(ctx, f.admin, f.input.WorkspaceID, j.ID); e != nil {
		t.Fatal("administrative scope", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, f.admin, f.input.WorkspaceID, j.ID); e == nil {
		t.Fatal("stale account epoch read")
	}
}

func TestActualTrainingRetiredInputsDisabledProfileLimitsAndScopedPagination(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	s := f.s
	for i := 0; i < 3; i++ {
		if _, e := s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	first, e := s.List(ctx, f.portal, f.input.WorkspaceID, url.Values{"limit": {"2"}})
	if e != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatal("first page", e)
	}
	next, e := s.List(ctx, f.portal, f.input.WorkspaceID, url.Values{"limit": {"2"}, "cursor": {*first.NextCursor}})
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == first.Items[1].ID {
		t.Fatal("keyset page", e)
	}
	_, e = s.List(ctx, f.admin, f.input.WorkspaceID, url.Values{"cursor": {*first.NextCursor}})
	expected(t, e, "INVALID_CURSOR")
	runtimePolicy := config.DefaultRuntimePolicy()
	runtimePolicy.Training.MaxActivePerWorkspace = 3
	s.Identity.Config.Runtime = &runtimePolicy
	_, e = s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString())
	expected(t, e, "TRAINING_JOB_LIMIT_REACHED")
	s.Identity.Config.Runtime = nil
	if _, e = s.Datasets.RetireVersion(ctx, f.portal, f.input.WorkspaceID, f.input.DatasetID, f.input.DatasetVersionID, "Retain old training provenance", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString())
	expected(t, e, "DATASET_RETIRED")
	if _, e = s.Get(ctx, f.portal, f.input.WorkspaceID, first.Items[0].ID); e != nil {
		t.Fatal("retirement erased existing history", e)
	}
	s.Identity.Config.Training = config.TrainingConfiguration{}
	_, e = s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString())
	expected(t, e, "TRAINING_UNAVAILABLE")
	input := f.input
	input.Configuration.Device = wire.VerificationProfile
	_, e = s.Create(ctx, f.portal, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "VALIDATION_FAILED")
}
