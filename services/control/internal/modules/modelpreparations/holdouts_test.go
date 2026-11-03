package modelpreparations_test

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
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type scenario struct {
	s             *modelpreparations.Service
	identity      *identity.Service
	datasets      *datasets.Service
	workspaces    *workspaces.Service
	admin, portal identity.Auth
	password      string
}

func setup(t *testing.T) scenario {
	t.Helper()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	ws := &workspaces.Service{DB: account.DB, Identity: account}
	root := filepath.Join(t.TempDir(), "datasets")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := datasets.InitializeStore(root, portal.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(root, portal.Me.Workspace.TenantID, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../../../runtimes/data-processing/process.py"))
	binary := os.Getenv("PROJECT_ONE_DATA_PYTHON")
	if binary == "" {
		t.Fatal("actual pinned data Python required")
	}
	datasetConfig := config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}
	processor, e := datasets.NewProcessor(datasetConfig, config.DefaultDatasetPolicy())
	if e != nil {
		t.Fatal(e)
	}
	ds := &datasets.Service{DB: account.DB, Identity: account, Workspaces: ws, Store: store, Processor: processor}
	account.Config.Datasets = datasetConfig
	service, e := modelpreparations.New(account.DB, account, ws, ds)
	if e != nil {
		t.Fatal(e)
	}
	return scenario{s: service, identity: account, datasets: ds, workspaces: ws, admin: admin, portal: portal, password: password}
}
func rows(prefix string) []datawire.Record {
	return []datawire.Record{{SourceID: prefix + "-account", Prompt: prefix + " forgot password", Completion: "account"}, {SourceID: prefix + "-billing", Prompt: prefix + " duplicate invoice", Completion: "billing"}, {SourceID: prefix + "-technical", Prompt: prefix + " application error", Completion: "technical"}}
}
func publish(t *testing.T, f scenario, workspace string, records []datawire.Record) datasets.Version {
	t.Helper()
	ctx := context.Background()
	parent, e := f.datasets.Create(ctx, f.portal, datasets.CreateRequest{WorkspaceID: workspace, Name: "Synthetic source " + uuid.NewString(), Origin: "Local synthetic model-lifecycle verification", License: "CC0 synthetic fixture", RightsAsserted: true, Reason: "Actual immutable source verification"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var body bytes.Buffer
	for _, record := range records {
		raw, _ := json.Marshal(record)
		body.Write(raw)
		body.WriteByte('\n')
	}
	sum := sha256.Sum256(body.Bytes())
	draft, e := f.datasets.CreateDraft(ctx, f.portal, workspace, parent.ID, datasets.DraftRequest{SourceName: "synthetic.jsonl", ExpectedBytes: int64(body.Len()), SourceSHA256: hex.EncodeToString(sum[:]), Reason: "Actual local JSONL source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.datasets.Upload(ctx, f.portal, workspace, parent.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, &body, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if worked, e := f.datasets.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual inspect", worked, e)
	}
	draft, e = f.datasets.GetDraft(ctx, f.portal, workspace, parent.ID, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	job, e := f.datasets.Publish(ctx, f.portal, workspace, parent.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Actual independent immutable split"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := f.datasets.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual publish", worked, e)
	}
	job, e = f.datasets.GetJob(ctx, f.portal, workspace, parent.ID, job.ID)
	if e != nil || job.VersionID == nil || job.State != "succeeded" {
		t.Fatal("published version", e, job.State)
	}
	version, e := f.datasets.GetVersion(ctx, f.portal, workspace, parent.ID, *job.VersionID)
	if e != nil {
		t.Fatal(e)
	}
	return version
}
func input(version datasets.Version) modelpreparations.HoldoutRequest {
	return modelpreparations.HoldoutRequest{WorkspaceID: version.WorkspaceID, DatasetID: version.DatasetID, DatasetVersionID: version.ID, ExpectedManifestSHA256: version.ManifestSHA256, Name: "Synthetic routing evaluation", Reason: "Reserve independent local evaluation source"}
}
func expected(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s observed %v", code, e)
	}
}
func count(t *testing.T, f scenario, sql string, args ...any) int {
	t.Helper()
	var value int
	if e := f.identity.DB.QueryRow(context.Background(), sql, args...).Scan(&value); e != nil {
		t.Fatal(e)
	}
	return value
}

func trainingService(t *testing.T, f scenario) *training.Service {
	t.Helper()
	ctx := context.Background()
	nodeService := &nodes.Service{DB: f.identity.DB, Identity: f.identity, Workspaces: f.workspaces}
	facts := agentwire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-model-lifecycle", LogicalCPUs: 4, MemoryTotalBytes: 17179869184, DiskPath: "/", DiskTotalBytes: 107374182400, DiskAvailableBytes: 53687091200, ObservedAt: time.Now().UTC(), AgentVersion: agentwire.AgentVersion}
	token, e := nodeService.MintForProfile(ctx, f.admin, f.portal.Me.Workspace.ID, "Synthetic source admission node", agentwire.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = nodeService.Enroll(ctx, agentwire.Enrollment{JoinToken: token.Token, Handshake: agentwire.Handshake{ProtocolVersion: agentwire.AIVersion, ExecutionProfile: agentwire.DockerAIProfile, Capabilities: agentwire.Capabilities(agentwire.DockerAIProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	model := uuid.NewString()
	catalog, _ := json.Marshal(agentwire.ApprovedModel())
	if _, e = f.identity.DB.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Explicit synthetic base metadata','qwen3-0.6b',$4,$5,'ready',$6)`, model, f.portal.Me.Workspace.TenantID, f.portal.Me.Workspace.ID, agentwire.ModelCatalogDigest(), catalog, f.admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	f.identity.Config.Training = config.TrainingConfiguration{ArtifactRoot: filepath.Join(t.TempDir(), "training"), ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	service := &training.Service{DB: f.identity.DB, Identity: f.identity, Workspaces: f.workspaces, Nodes: nodeService, Models: &models.Service{DB: f.identity.DB, Identity: f.identity, Workspaces: f.workspaces}, Datasets: f.datasets, Holdouts: f.s}
	nodeService.Training = service
	// Node/model/GPU and release metadata are explicit test doubles; no dispatch or compute is requested.

	return service
}
func trainingInput(t *testing.T, f scenario, service *training.Service, version datasets.Version) training.CreateRequest {
	t.Helper()
	var node, model string
	if e := f.identity.DB.QueryRow(context.Background(), "SELECT id::text FROM managed_nodes LIMIT 1").Scan(&node); e != nil {
		t.Fatal(e)
	}
	if e := f.identity.DB.QueryRow(context.Background(), "SELECT id::text FROM model_versions LIMIT 1").Scan(&model); e != nil {
		t.Fatal(e)
	}
	if _, e := f.identity.DB.Exec(context.Background(), "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp() WHERE id=$1", node); e != nil {
		t.Fatal(e)
	}
	return training.CreateRequest{WorkspaceID: version.WorkspaceID, NodeID: node, ModelID: model, DatasetID: version.DatasetID, DatasetVersionID: version.ID, GPUUUID: "GPU-" + uuid.NewString(), Configuration: trainingwire.DefaultConfiguration(), Reason: "Explicit synthetic admission only"}
}

func TestActualHoldoutImmutablePersistenceReplayPaginationAndCurrentAuthority(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("independent"))
	request := input(version)
	key := uuid.NewString()
	holdout, e := f.s.ReserveHoldout(ctx, f.portal, request, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if holdout.RowCount != 3 || holdout.Labels != (modelpreparations.LabelCounts{Account: 1, Billing: 1, Technical: 1}) || holdout.EffectiveSourceSHA256 != version.EffectiveSourceSHA256 {
		t.Fatal("immutable source metadata")
	}
	replay, e := f.s.ReserveHoldout(ctx, f.portal, request, key, uuid.NewString())
	if e != nil || replay.ID != holdout.ID {
		t.Fatal("exact replay", e)
	}
	changed := request
	changed.Reason = "Different intent"
	_, e = f.s.ReserveHoldout(ctx, f.portal, changed, key, uuid.NewString())
	expected(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = f.s.ReserveHoldout(ctx, f.portal, request, uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_EXISTS")
	page, e := f.s.ListHoldouts(ctx, f.portal, request.WorkspaceID, url.Values{})
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != holdout.ID {
		t.Fatal("scope page", e)
	}
	restarted, e := modelpreparations.New(f.identity.DB, f.identity, f.workspaces, f.datasets)
	if e != nil {
		t.Fatal(e)
	}
	if value, e := restarted.GetHoldout(ctx, f.portal, request.WorkspaceID, holdout.ID); e != nil || value.ID != holdout.ID {
		t.Fatal("restart persistence", e)
	}
	if count(t, f, "SELECT count(*) FROM model_holdout_sources") != 3 || count(t, f, "SELECT count(*) FROM model_holdout_prompts") != 6 {
		t.Fatal("reservation fingerprints incomplete")
	}
	if _, e = f.identity.DB.Exec(ctx, "UPDATE model_holdouts SET name='changed' WHERE id=$1", holdout.ID); e == nil {
		t.Fatal("immutable row modified")
	}
	if _, e = f.identity.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", request.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.ReserveHoldout(ctx, f.portal, request, key, uuid.NewString())
	expected(t, e, "ACCESS_DENIED")
	if _, e = f.s.GetHoldout(ctx, f.portal, request.WorkspaceID, holdout.ID); e != nil {
		t.Fatal("current viewer read", e)
	}
	if _, e = f.identity.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.GetHoldout(ctx, f.portal, request.WorkspaceID, holdout.ID); e == nil {
		t.Fatal("stale epoch read")
	}
}
func TestActualHoldoutAuditRollbackLabelsDuplicatesAndPrivateSourceIntegrity(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("audit"))
	request := input(version)
	if _, e := f.identity.DB.Exec(ctx, `CREATE FUNCTION reject_holdout_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.holdout_reserved' THEN RAISE EXCEPTION 'synthetic audit rejection'; END IF; RETURN NEW; END;$$; CREATE TRIGGER reject_holdout_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_holdout_audit()`); e != nil {
		t.Fatal(e)
	}
	key := uuid.NewString()
	if _, e := f.s.ReserveHoldout(ctx, f.portal, request, key, uuid.NewString()); e == nil {
		t.Fatal("audit failure accepted")
	}
	if count(t, f, "SELECT count(*) FROM model_holdouts") != 0 || count(t, f, "SELECT count(*) FROM model_holdout_sources") != 0 || count(t, f, "SELECT count(*) FROM model_holdout_prompts") != 0 {
		t.Fatal("audit rollback retained partial reservation")
	}
	if _, e := f.identity.DB.Exec(ctx, "DROP TRIGGER reject_holdout_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.ReserveHoldout(ctx, f.portal, request, key, uuid.NewString()); e != nil {
		t.Fatal("retry after audit repair", e)
	}
	invalid := rows("labels")
	invalid[0].Completion = "other"
	bad := publish(t, f, request.WorkspaceID, invalid)
	_, e := f.s.ReserveHoldout(ctx, f.portal, input(bad), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_LABELS_INVALID")
	duplicate := rows("duplicate")
	duplicate[1].Prompt = duplicate[0].Prompt
	bad = publish(t, f, request.WorkspaceID, duplicate)
	_, e = f.s.ReserveHoldout(ctx, f.portal, input(bad), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_LABELS_INVALID")
}
func TestActualHoldoutBlocksFutureRawWrappedGroupAndNilGuardTraining(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	service := trainingService(t, f)
	reserved := rows("holdout")
	version := publish(t, f, f.portal.Me.Workspace.ID, reserved)
	if _, e := f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"raw", "wrapped", "group"} {
		t.Run(kind, func(t *testing.T) {
			candidate := rows("training-" + kind)
			switch kind {
			case "raw":
				candidate[0].Prompt = reserved[0].Prompt
				candidate[0].Completion = "Different completion"
			case "wrapped":
				candidate[0].Prompt = lifecyclewire.InstructionPrompt(reserved[0].Prompt)
			case "group":
				candidate[0].SourceID = reserved[0].SourceID
			}
			selected := publish(t, f, f.portal.Me.Workspace.ID, candidate)
			_, e := service.Create(ctx, f.portal, trainingInput(t, f, service, selected), uuid.NewString(), uuid.NewString())
			expected(t, e, "MODEL_HOLDOUT_OVERLAP")
		})
	}
	selected := publish(t, f, f.portal.Me.Workspace.ID, rows("safe"))
	service.Holdouts = nil
	_, e := service.Create(ctx, f.portal, trainingInput(t, f, service, selected), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_GUARD_REQUIRED")
	service.Holdouts = f.s
	job, e := service.Create(ctx, f.portal, trainingInput(t, f, service, selected), uuid.NewString(), uuid.NewString())
	if e != nil || job.State != "queued" {
		t.Fatal("safe unrelated source refused", e)
	}
	if count(t, f, "SELECT count(*) FROM training_jobs") != 1 || count(t, f, "SELECT count(*) FROM gpu_leases") != 0 || count(t, f, "SELECT count(*) FROM node_tasks WHERE operation='training.run.v1'") != 0 {
		t.Fatal("refused training had an effect or queued work reserved GPU")
	}
}
