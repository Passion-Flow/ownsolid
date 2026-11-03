//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// Actual repositories and actual retained CPU adapter/heldout bytes. Only node,
// model-catalog metadata and physical-container envelopes are explicit doubles.
func runtimeReviewSetup(t *testing.T) reviewStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	ws := &workspaces.Service{DB: f.PostgreSQL, Identity: owner}
	s, e := New(f.PostgreSQL, owner, ws, nil)
	if e != nil {
		t.Fatal(e)
	}
	s.Nodes = &nodes.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: ws}
	g := reviewStory{f: f, ctx: ctx, s: s, model: uuid.NewString()}
	var issued identity.IssuedSession
	if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	g.a, e = owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	g.seed(t, "model_versions", map[string]any{"id": g.model, "name": "Explicit synthetic approved catalog envelope", "model_key": agent.ApprovedModel().ModelKey, "catalog_digest": agent.ModelCatalogDigest(), "manifest": reviewJSON(reviewRaw(t, agent.ApprovedModel())), "state": "ready", "created_by": f.User})
	return g
}

func runtimeSourceSetup(t *testing.T) creatorStory {
	t.Helper()
	g := runtimeReviewSetup(t)
	root := t.TempDir()
	trainingRoot := creatorStoreDirectory(t, root, "training")
	datasetRoot := creatorStoreDirectory(t, root, "datasets")
	modelRoot := creatorStoreDirectory(t, root, "models")
	prepRoot := creatorStoreDirectory(t, root, "preparations")
	if e := training.InitializeStore(trainingRoot, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	resources := trainwire.DefaultResources()
	trainingStore, e := training.OpenStore(trainingRoot, g.f.Tenant, 4096, resources.MaximumOutputBytes, resources.MaximumOutputFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := trainingStore.Close(); e != nil {
			t.Error(e)
		}
	})
	ts := &training.Service{DB: g.f.PostgreSQL, Identity: g.s.Identity, Workspaces: g.s.Workspaces, Store: trainingStore}
	source := creatorAdapterGraph(t, creatorAdapterStory{f: g.f, ctx: g.ctx, owner: g.s.Identity, s: ts, a: g.a, c: trainwire.Command{ModelID: g.model}})
	// Real retained CPU adapter bytes pass through the actual private chunk and
	// publication implementation. Synthetic SQL/physical envelopes grant no GPU proof.
	if e = trainingStore.PrepareUpload(source.descriptor); e != nil {
		t.Fatal(e)
	}
	for _, asset := range source.descriptor.Files {
		raw, e := os.ReadFile(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT"), "adapter", asset.Path))
		if e != nil || int64(len(raw)) != asset.SizeBytes || hash(raw) != asset.SHA256 {
			t.Fatal("exact retained CPU adapter bytes", asset.Path, e)
		}
		for offset := int64(0); offset < asset.SizeBytes; offset += trainwire.TransferChunkBytes {
			length := min(trainwire.TransferChunkBytes, asset.SizeBytes-offset)
			part := raw[offset : offset+length]
			if _, e = trainingStore.WriteChunk(g.ctx, source.descriptor, asset.Path, offset, length, hash(part), bytes.NewReader(part)); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = trainingStore.PreparePublishedFiles(g.ctx, source.descriptor); e != nil {
		t.Fatal(e)
	}
	if e = datasets.InitializeStore(datasetRoot, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	dsStore, e := datasets.OpenStore(datasetRoot, g.f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := dsStore.Close(); e != nil {
			t.Error(e)
		}
	})
	if e = models.InitializeStore(modelRoot, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	if e = InitializeStore(prepRoot, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	prepStore, e := OpenStore(prepRoot, g.f.Tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := prepStore.Close(); e != nil {
			t.Error(e)
		}
	})
	ds := &datasets.Service{DB: g.f.PostgreSQL, Identity: g.s.Identity, Workspaces: g.s.Workspaces, Store: dsStore}
	g.s.Training, g.s.Datasets, g.s.Models, g.s.Store = ts, ds, &models.Service{DB: g.f.PostgreSQL, Identity: g.s.Identity, Workspaces: g.s.Workspaces, RootDirectory: modelRoot}, prepStore
	g.s.Identity.Config.Models.ArtifactRoot = modelRoot
	g.s.Identity.Config.Datasets.ArtifactRoot = datasetRoot
	g.s.Identity.Config.Training.ArtifactRoot = trainingRoot
	digest := strings.Repeat("a", 64)
	g.s.Identity.Config.ModelPreparation = config.ModelPreparationConfiguration{ArtifactRoot: prepRoot, ImageReference: wire.ImageRepository + "@sha256:" + digest, ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: digest}
	node := uuid.NewString()
	now := time.Now().UTC()
	g.seed(t, "managed_nodes", map[string]any{"id": node, "name": "Synthetic creator target " + node, "status": "active", "credential_hash": bytes.Repeat([]byte{9}, 32), "protocol_version": wire.NodeProtocolVersion, "execution_profile": wire.NodeProfile, "capabilities": reviewJSON(reviewRaw(t, agent.Capabilities(agent.DockerModelLifecycleProfile))), "facts": reviewJSON(reviewRaw(t, agent.Facts{OSID: "synthetic", Architecture: "test", Hostname: "Creator metadata only", MemoryTotalBytes: 1048576, ObservedAt: now})), "last_heartbeat_at": now})
	holdout, heldoutFile := creatorHeldout(t, g, datasetRoot)
	return creatorStory{g: g, source: source, input: CreateRequest{WorkspaceID: g.f.Workspace, TrainingJobID: source.job, HeldoutID: holdout, NodeID: node, GPUUUID: "GPU-" + uuid.NewString(), Configuration: wire.DefaultConfiguration(), Reason: "Actual retained-file creation over explicit synthetic SQL scope \u4e16\u754c"}, trainingRoot: trainingRoot, heldoutFile: heldoutFile}
}

type runtimeStory struct {
	creatorStory
	credential  string
	preparation Preparation
	lease       agent.Lease
}

func runtimeSetup(t *testing.T) runtimeStory {
	t.Helper()
	g := runtimeSourceSetup(t)
	credential := security.Token()
	sum := security.Digest("node-credential:v1:" + credential)
	now := time.Now().UTC()
	policy := g.g.s.policy()
	facts := agent.Facts{OSID: wire.NodeOSID, OSVersion: wire.NodeOSVersion, Architecture: wire.NodeArchitecture, Hostname: "Explicit synthetic runtime node", LogicalCPUs: 4, MemoryTotalBytes: 2 * policy.MinimumHostMemory, DiskPath: "/synthetic", DiskTotalBytes: 4 * policy.MinimumFreeBytes, DiskAvailableBytes: 2 * policy.MinimumFreeBytes, ObservedAt: now, AgentVersion: agent.AgentVersion}
	g.g.exec(t, `UPDATE managed_nodes SET facts=$1,credential_hash=$2,last_heartbeat_at=clock_timestamp() WHERE id=$3`, `UPDATE managed_nodes SET facts=?,credential_hash=?,last_heartbeat_at=UTC_TIMESTAMP(6) WHERE id=?`, string(reviewRaw(t, facts)), sum, g.input.NodeID)
	observation := agent.InferenceObservation{GPUs: []agent.NVIDIAGPU{{UUID: g.input.GPUUUID, Name: "Explicit synthetic GPU envelope", DriverVersion: "580.1.0", MemoryTotalMiB: 2 * policy.MinimumGPUMemoryMiB, MemoryFreeMiB: 2 * policy.MinimumGPUMemoryMiB, ComputeCapability: 8.0, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
	g.g.seed(t, "inference_nodes", map[string]any{"node_id": g.input.NodeID, "observation": reviewJSON(reviewRaw(t, observation)), "received_at": now})
	g.g.s.Inference = &deployments.Service{Nodes: g.g.s.Nodes}
	p, e := g.create(uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return runtimeStory{creatorStory: g, credential: credential, preparation: p}
}
func (g *runtimeStory) dispatch(t *testing.T) {
	t.Helper()
	worked, e := g.g.s.DispatchNext(g.g.ctx)
	if e != nil || !worked {
		t.Fatal("actual dispatch", worked, e)
	}
	g.refresh(t)
	if g.preparation.State != "preparing" || g.preparation.CurrentAttemptID == nil {
		t.Fatal("durable attempt missing")
	}
}
func (g *runtimeStory) claim(t *testing.T) {
	t.Helper()
	c, owned, e := g.g.s.ClaimPreparation(g.g.ctx, g.input.NodeID, g.credential, uuid.NewString())
	if e != nil || !owned || c.Lease == nil || c.Lease.Preparation == nil {
		t.Fatal("actual claim", owned, e)
	}
	g.lease = *c.Lease
}
func (g *runtimeStory) refresh(t *testing.T) {
	t.Helper()
	p, e := g.g.s.Get(g.g.ctx, g.g.a, g.g.f.Workspace, g.preparation.ID)
	if e != nil {
		t.Fatal(e)
	}
	g.preparation = p
}
func (g runtimeStory) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g runtimeStory) due(t *testing.T) {
	t.Helper()
	g.g.exec(t, `UPDATE model_preparations SET next_dispatch_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparations SET next_dispatch_at=UTC_TIMESTAMP(6) WHERE id=?`, g.preparation.ID)
}
func (g runtimeStory) stopReceipt(t *testing.T, delivered bool) wire.StopReceipt {
	t.Helper()
	c := g.lease.Preparation
	sha, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	r := wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: g.input.NodeID, TaskID: g.lease.TaskID, Scope: c.Scope, AttemptID: c.AttemptID, AttemptNumber: c.AttemptNumber, CommandSHA256: sha, ImageID: *c.Recipe.Identity.ImageID, GPUUUID: c.GPUUUID, ContainerName: wire.ContainerName(c.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, AgentErrorCode: "MODEL_PREPARATION_AGENT_INTERRUPTED", CompletedAt: time.Now().UTC()}
	if delivered {
		cid := strings.Repeat("d", 64)
		r.Proof = "owned_container_removed"
		r.ContainerID = &cid
	}
	return r
}
func runtimeError(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("wanted %s, got %v", code, e)
	}
}
func (g runtimeStory) released(t *testing.T, wanted bool) {
	t.Helper()
	n := g.count(t, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'`, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=? AND state='released'`, g.preparation.ID)
	if (n == 1) != wanted {
		t.Fatal("physical owner mismatch", n, wanted)
	}
}
func runtimeEvents(p, attempt string, rows int64) [][]byte {
	out := [][]byte{}
	for sequence := int64(1); sequence <= 2*rows+3; sequence++ {
		e := wire.Event{Sequence: sequence, PreparationID: p, AttemptID: attempt, Kind: "phase"}
		switch sequence {
		case 1:
			e.Phase = "evaluating_base"
		case rows + 2:
			e.Phase = "merging"
		case rows + 3:
			e.Phase = "evaluating_candidate"
		default:
			e.Kind = "evaluation_progress"
			e.Phase = "base"
			completed := sequence - 1
			if sequence > rows+3 {
				e.Phase = "candidate"
				completed = sequence - rows - 3
			}
			e.CompletedRows = &completed
			e.TotalRows = &rows
		}
		raw, _ := json.Marshal(e)
		out = append(out, raw)
	}
	return out
}

func TestActualPairedPreparationRuntime(t *testing.T) {
	t.Run("dispatch_claim_renew_and_exact_command", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		if e := bindFrozenCommand(g.preparation, *g.lease.Preparation); e != nil {
			t.Fatal(e)
		}
		renewed, owned, e := g.g.s.RenewPreparation(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken)
		if e != nil || !owned || renewed.ValidForMilliseconds <= 0 || renewed.LeaseExpiresAt.Location() != time.UTC {
			t.Fatal("actual renewal", e)
		}
		_, _, e = g.g.s.RenewPreparation(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, security.Token())
		runtimeError(t, e, "AGENT_LEASE_STALE")
		duplicate, owned, e := g.g.s.ClaimPreparation(g.g.ctx, g.input.NodeID, g.credential, uuid.NewString())
		if e != nil || !owned || duplicate.Lease != nil {
			t.Fatal("claim must not disclose another live token", e)
		}
		g.released(t, false)
	})
	t.Run("ordered_events_replay_gap_and_readback", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		events := runtimeEvents(g.preparation.ID, *g.preparation.CurrentAttemptID, 3)
		if _, e := g.g.s.RecordEvents(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, events[1:2]); e == nil {
			t.Fatal("event gap accepted")
		}
		accepted, e := g.g.s.RecordEvents(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, events)
		if e != nil || len(accepted) != 9 {
			t.Fatal("actual ordered events", e)
		}
		duplicate, e := g.g.s.RecordEvents(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, events)
		if e != nil || len(duplicate) != 9 || !duplicate[0].Duplicate {
			t.Fatal("event replay", e)
		}
		page, e := g.g.s.Events(g.g.ctx, g.g.a, g.g.f.Workspace, g.preparation.ID, *g.preparation.CurrentAttemptID, url.Values{"limit": {"4"}})
		if e != nil || len(page.Items) != 4 || page.NextAfter == nil || *page.NextAfter != 4 {
			t.Fatal("actual event cursor", e)
		}
		attempts, e := g.g.s.Attempts(g.g.ctx, g.g.a, g.g.f.Workspace, g.preparation.ID, url.Values{})
		if e != nil || len(attempts.Items) != 1 || attempts.Items[0].LastEventSequence != 9 {
			t.Fatal("actual attempt history", e)
		}
	})
	t.Run("real_adapter_heldout_ranges_and_credential_fences", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		for _, kind := range []string{"adapter", "heldout"} {
			asset := g.lease.Preparation.Inputs.Heldout
			if kind == "adapter" {
				asset = g.lease.Preparation.Inputs.AdapterFiles[0]
			}
			input := wire.InputFileRequest{LeaseToken: g.lease.LeaseToken, Kind: kind, Asset: asset.Path, Offset: 0, Length: min(wire.TransferChunkBytes, asset.SizeBytes)}
			out, e := g.g.s.InputFile(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, input)
			if e != nil {
				t.Fatal("actual retained range", kind, e)
			}
			raw, e := io.ReadAll(io.NewSectionReader(out.File, out.Offset, out.Size))
			out.File.Close()
			if e != nil || hash(raw) != out.SHA256 || int64(len(raw)) != input.Length {
				t.Fatal("range bytes/hash mismatch", e)
			}
			_, e = g.g.s.InputFile(g.g.ctx, g.input.NodeID, security.Token(), g.lease.TaskID, input)
			runtimeError(t, e, "AGENT_UNAUTHORIZED")
		}
	})
	t.Run("physical_stop_replay_conflict_and_no_false_publication", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		receipt := g.stopReceipt(t, false)
		accepted, e := g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !accepted.GPUReleased || accepted.State != "failed" {
			t.Fatal("positive exact stop", e)
		}
		again, e := g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !again.Duplicate {
			t.Fatal("stop replay", e)
		}
		changed := receipt
		changed.ReceiptID = uuid.NewString()
		_, e = g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, changed, nil)
		runtimeError(t, e, "MODEL_PREPARATION_STOP_CONFLICT")
		g.released(t, true)
		if n := g.count(t, `SELECT count(*) FROM model_preparation_artifacts`, `SELECT count(*) FROM model_preparation_artifacts`); n != 0 {
			t.Fatal("stop manufactured artifact")
		}
	})
	t.Run("revoked_actor_cannot_execute_but_can_stop", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		receipt := g.stopReceipt(t, false)
		g.g.exec(t, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, g.g.f.User)
		_, _, e := g.g.s.RenewPreparation(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken)
		runtimeError(t, e, "ACCESS_DENIED")
		a, e := g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || a.State != "cancelled" || !a.GPUReleased {
			t.Fatal("restricted revoked cleanup", e)
		}
	})
	t.Run("undelivered_cancel_releases_only_never_delivered", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		_, e := g.g.s.Cancel(g.g.ctx, g.g.a, g.g.f.Workspace, g.preparation.ID, ActionRequest{ExpectedRevision: g.preparation.Revision, Reason: "Explicit cancellation before physical delivery"}, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		g.due(t)
		worked, e := g.g.s.ReconcileNext(g.g.ctx)
		if e != nil || !worked {
			t.Fatal("actual never-delivered reconcile", e)
		}
		g.released(t, true)
	})
	t.Run("expired_delivered_retains_gpu_until_positive_stop", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		g.g.exec(t, `UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, `UPDATE node_tasks SET lease_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?`, g.lease.TaskID)
		g.due(t)
		worked, e := g.g.s.ReconcileNext(g.g.ctx)
		if e != nil || !worked {
			t.Fatal(e)
		}
		g.released(t, false)
		receipt := g.stopReceipt(t, false)
		a, e := g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !a.GPUReleased {
			t.Fatal("expired restricted cleanup", e)
		}
	})
	t.Run("explicit_recovery_fresh_attempt_and_old_token_refusal", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		old := g.lease
		receipt := g.stopReceipt(t, false)
		if _, e := g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil); e != nil {
			t.Fatal(e)
		}
		g.refresh(t)
		input := RecoveryRequest{ExpectedRevision: g.preparation.Revision, NodeID: g.input.NodeID, Reason: "Explicit same-source recovery after exact physical stop"}
		key := uuid.NewString()
		p, e := g.g.s.Recover(g.g.ctx, g.g.a, g.g.f.Workspace, g.preparation.ID, input, key, uuid.NewString())
		if e != nil || p.State != "queued" || p.PendingRecoveryID == nil {
			t.Fatal("actual recovery", e)
		}
		again, e := g.g.s.Recover(g.g.ctx, g.g.a, g.g.f.Workspace, p.ID, input, key, uuid.NewString())
		if e != nil || again.ID != p.ID {
			t.Fatal("exact recovery replay", e)
		}
		g.preparation = p
		g.dispatch(t)
		g.claim(t)
		if g.lease.Preparation.AttemptNumber != 2 || g.lease.TaskID == old.TaskID {
			t.Fatal("fresh attempt not created")
		}
		currentExpiry := func() time.Time {
			var until time.Time
			if e := g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement(
				`SELECT lease_expires_at FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4`,
				`SELECT lease_expires_at FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`,
				g.g.f.Tenant, g.g.f.Workspace, g.input.NodeID, g.lease.TaskID)).Scan(&until); e != nil {
				t.Fatal("current actual lease", e)
			}
			return until
		}
		before := currentExpiry()
		renewed, handled, e := g.g.s.RenewPreparation(g.g.ctx, g.input.NodeID, g.credential, old.TaskID, old.LeaseToken)
		if e != nil || handled || !reflect.ValueOf(renewed).IsZero() {
			t.Fatal("historical delegated route must decline with a zero lease", handled, e)
		}
		// Exercise the actual public node owner after the preparation route declines.
		g.g.s.Nodes.Preparations = g.g.s
		renewed, e = g.g.s.Nodes.Renew(g.g.ctx, g.input.NodeID, g.credential, old.TaskID, old.LeaseToken)
		runtimeError(t, e, "AGENT_LEASE_STALE")
		if !reflect.ValueOf(renewed).IsZero() || !currentExpiry().Equal(before) {
			t.Fatal("historical node renewal projected authority or changed the current lease")
		}
		if n := g.count(t,
			`SELECT count(*) FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND state='failed' AND lease_token_hash IS NULL AND lease_expires_at IS NULL`,
			`SELECT count(*) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND state='failed' AND lease_token_hash IS NULL AND lease_expires_at IS NULL`,
			g.g.f.Tenant, g.g.f.Workspace, g.input.NodeID, old.TaskID); n != 1 {
			t.Fatal("historical task gained a live lease")
		}
	})
	t.Run("unknown_claim_zero_token_and_durable_refusal", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		wrapped := creatorUnknown(g.creatorStory, 1)
		s := wrapped.g.s
		claim, owned, e := s.ClaimPreparation(g.g.ctx, g.input.NodeID, g.credential, uuid.NewString())
		runtimeError(t, e, string(transaction.CommitUnknown))
		if !owned || !reflect.DeepEqual(claim, agent.Claim{}) {
			t.Fatal("uncertain claim disclosed token")
		}
		claim, owned, e = s.ClaimPreparation(g.g.ctx, g.input.NodeID, g.credential, uuid.NewString())
		if e != nil || !owned || claim.Lease != nil {
			t.Fatal("uncertain claim implicitly resent", e)
		}
		g.released(t, false)
	})
	t.Run("dispatch_and_stop_audit_rollback", func(t *testing.T) {
		g := runtimeSetup(t)
		clear := runtimeRejectAudit(t, g, "model.preparation_dispatched")
		worked, e := g.g.s.DispatchNext(g.g.ctx)
		if e == nil || worked {
			t.Fatal("failed dispatch audit accepted")
		}
		if n := g.count(t, `SELECT count(*) FROM model_preparation_attempts`, `SELECT count(*) FROM model_preparation_attempts`); n != 0 {
			t.Fatal("partial dispatched attempt")
		}
		if n := g.count(t, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1`, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=?`, g.preparation.ID); n != 0 {
			t.Fatal("partial GPU owner")
		}
		clear()
		g.dispatch(t)
		g.claim(t)
		clear = runtimeRejectAudit(t, g, "model.preparation_physical_stop_accepted")
		receipt := g.stopReceipt(t, false)
		a, e := g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e == nil || !reflect.DeepEqual(a, wire.StopAcceptance{}) {
			t.Fatal("failed stop audit returned acceptance")
		}
		g.released(t, false)
		clear()
		if _, e = g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil); e != nil {
			t.Fatal(e)
		}
		g.released(t, true)
	})
	t.Run("unknown_stop_zero_and_exact_recovery", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		receipt := g.stopReceipt(t, false)
		wrapped := creatorUnknown(g.creatorStory, 2)
		a, e := wrapped.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		runtimeError(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(a, wire.StopAcceptance{}) {
			t.Fatal("uncertain stop disclosed acceptance")
		}
		g.released(t, true)
		a, e = wrapped.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !a.Duplicate {
			t.Fatal("explicit exact stop recovery", e)
		}
	})

	t.Run("repeat_wait_keeps_revision_and_dispatch_retry", func(t *testing.T) {
		g := runtimeSetup(t)
		g.g.exec(t, `UPDATE managed_nodes SET status='disabled' WHERE id=$1`, `UPDATE managed_nodes SET status='disabled' WHERE id=?`, g.input.NodeID)
		if worked, e := g.g.s.DispatchNext(g.g.ctx); e != nil || worked {
			t.Fatal("node unavailable wait", worked, e)
		}
		g.refresh(t)
		if g.preparation.WaitCode == nil || *g.preparation.WaitCode != "NODE_UNAVAILABLE" {
			t.Fatal("missing real wait reason")
		}
		revision := g.preparation.Revision
		g.due(t)
		if worked, e := g.g.s.DispatchNext(g.g.ctx); e != nil || worked {
			t.Fatal("repeat wait", worked, e)
		}
		g.refresh(t)
		if g.preparation.Revision != revision {
			t.Fatal("unchanged wait bumped revision")
		}
		g.g.exec(t, `UPDATE managed_nodes SET status='active' WHERE id=$1`, `UPDATE managed_nodes SET status='active' WHERE id=?`, g.input.NodeID)
		g.due(t)
		g.dispatch(t)
	})

	t.Run("selected_restore_invalidation_retains_physical_owner", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		g.claim(t)
		if e := g.g.f.Store.Within(g.g.ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q) }); e != nil {
			t.Fatal(e)
		}
		g.released(t, false)
		g.refresh(t)
		if g.preparation.State != "recovery_required" {
			t.Fatal("restore revived authority")
		}
	})
}

// Ordinary isolated copies retain exact approved content and single-link files.
// This is a separately selected large-file profile; the parent admits its peak
// base/chunk/publication/export/import coexistence before scheduling it.
func runtimeBaseFiles(t *testing.T, g runtimeStory) {
	t.Helper()
	base := os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	if !filepath.IsAbs(base) {
		t.Fatal("EXPLICIT_APPROVED_MODEL_DIRECTORY_REQUIRED")
	}
	directory := filepath.Join(g.g.s.Models.RootDirectory, "objects", g.preparation.ModelID)
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	for _, asset := range wire.ApprovedBaseFiles() {
		input, e := os.Open(filepath.Join(base, asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		target, e := os.OpenFile(filepath.Join(directory, asset.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0444)
		if e != nil {
			input.Close()
			t.Fatal(e)
		}
		digest := sha256.New()
		n, e := io.Copy(io.MultiWriter(target, digest), input)
		input.Close()
		syncError := target.Sync()
		closeError := target.Close()
		if e != nil || syncError != nil || closeError != nil || n != asset.SizeBytes || hex.EncodeToString(digest.Sum(nil)) != asset.SHA256 {
			t.Fatal("actual approved base bytes", asset.Path, e, syncError, closeError)
		}
	}
}
func runtimeReport(t *testing.T, g runtimeStory) []byte {
	t.Helper()
	c := g.lease.Preparation
	path := filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "result.json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var report wire.PreparedResult
	if e = json.Unmarshal(raw, &report); e != nil || report.Status != "prepared" || report.RowCount != 3 || report.Device != trainwire.VerificationProfile {
		t.Fatal("retained actual CPU prepared report required", e)
	}
	request, e := c.RequestBytes()
	if e != nil {
		t.Fatal(e)
	}
	// Retain the actual tensor inventory/deltas/predictions. Scope, production
	// resource/physical envelopes and random heldout IDs are explicit test doubles.
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
	report.RequestSHA256 = hash(request)
	raw, e = os.ReadFile(g.heldoutFile)
	if e != nil {
		t.Fatal(e)
	}
	rows := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(rows) != 3 || len(report.Cases) != 3 {
		t.Fatal("exact heldout cases required")
	}
	for i, raw := range rows {
		row, e := datawire.DecodeRecord(raw, c.Recipe.DataLimits)
		if e != nil {
			t.Fatal(e)
		}
		report.Cases[i].SourceID = row.SourceID
		report.Cases[i].PromptSHA256 = hash([]byte(row.Prompt))
		report.Cases[i].Expected = row.Completion
	}
	blank := func() wire.Score {
		return wire.Score{Rows: 3, Confusion: [][]int64{{0, 0, 0, 0}, {0, 0, 0, 0}, {0, 0, 0, 0}}}
	}
	report.Base, report.Candidate = blank(), blank()
	labels := wire.Labels()
	for _, row := range report.Cases {
		expected := -1
		for i, label := range labels {
			if row.Expected == label {
				expected = i
			}
		}
		if expected < 0 {
			t.Fatal("unrecognized heldout label")
		}
		for i, prediction := range []wire.Prediction{row.Base, row.Candidate} {
			actual := len(labels)
			if prediction.Label != nil {
				for j, label := range labels {
					if *prediction.Label == label {
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
	raw, e = json.Marshal(report)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func runtimeUploadAll(t *testing.T, g runtimeStory, u wire.Upload) {
	t.Helper()
	var total int64
	for _, asset := range u.Files {
		file, e := os.Open(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "model", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			length := min(wire.TransferChunkBytes, asset.SizeBytes-offset)
			body, e := io.ReadAll(io.NewSectionReader(file, offset, length))
			if e != nil || int64(len(body)) != length {
				file.Close()
				t.Fatal(e)
			}
			a, e := g.g.s.UploadChunk(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, u.ID, ChunkRequest{Asset: asset.Path, Offset: offset, Length: length, SHA256: hash(body)}, bytes.NewReader(body))
			if e != nil {
				file.Close()
				t.Fatal("actual complete-model chunk", asset.Path, offset, e)
			}
			total += length
			if a.ReceivedBytes != total {
				file.Close()
				t.Fatal("exact progress bytes")
			}
		}
		file.Close()
	}
	if total != 1208042441 {
		t.Fatal("retained declared complete-file workload changed", total)
	}
}

func TestActualPairedPreparationRuntimeFullFiles(t *testing.T) {
	t.Run("real_upload_independent_acceptance_stop_integrity_and_snapshot", func(t *testing.T) {
		g := runtimeSetup(t)
		g.dispatch(t)
		runtimeBaseFiles(t, g)
		g.claim(t)
		// Keep the ordinary lease alive during real large-file I/O. Cancellation is
		// joined before t.TempDir removes any owned file; no scheduler is installed.
		renewCtx, cancel := context.WithCancel(g.g.ctx)
		done := make(chan struct{})
		renewErrors := make(chan error, 1)
		go func() {
			defer close(done)
			timer := time.NewTicker(5 * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-renewCtx.Done():
					return
				case <-timer.C:
					if _, _, e := g.g.s.RenewPreparation(renewCtx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken); e != nil && renewCtx.Err() == nil {
						renewErrors <- e
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-done }()
		events := runtimeEvents(g.preparation.ID, *g.preparation.CurrentAttemptID, 3)
		if _, e := g.g.s.RecordEvents(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, events); e != nil {
			t.Fatal(e)
		}
		metadata := runtimeReport(t, g)
		u, e := g.g.s.BeginUpload(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, metadata)
		if e != nil {
			t.Fatal("actual upload begin", e)
		}
		if _, e = g.g.s.FinishUpload(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, u.ID); e == nil {
			t.Fatal("incomplete output accepted")
		}
		runtimeUploadAll(t, g, u)

		uncertain := creatorUnknown(g.creatorStory, 3)
		unknown, e := uncertain.g.s.FinishUpload(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, u.ID)
		runtimeError(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(unknown, OutputAcceptance{}) {
			t.Fatal("uncertain finalization returned acceptance")
		}
		accepted, e := g.g.s.FinishUpload(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, u.ID)
		if e != nil || !accepted.Duplicate || accepted.ManifestSHA256 != hash(metadata) {
			t.Fatal("independent complete actual tensor/report acceptance", e)
		}
		replay, e := g.g.s.FinishUpload(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, u.ID)
		if e != nil || !replay.Duplicate {
			t.Fatal("verified complete output replay", e)
		}
		g.released(t, false)
		receipt := g.stopReceipt(t, true)
		receipt.AgentErrorCode = ""
		digest := hash(metadata)
		receipt.OutcomeSHA256 = &digest
		stopped, e := g.g.s.RecordStop(g.g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !stopped.GPUReleased || stopped.State != "quality_failed" && stopped.State != "awaiting_review" {
			t.Fatal("separate positive physical stop", e)
		}
		cancel()
		<-done
		done = make(chan struct{})
		close(done)
		select {
		case e := <-renewErrors:
			t.Fatal("actual concurrent renewal", e)
		default:
		}
		if e = g.g.s.VerifyStored(g.g.ctx); e != nil {
			t.Fatal("actual accepted integrity", e)
		}
		report, e := g.g.s.Report(g.g.ctx, g.g.a, g.g.f.Workspace, g.preparation.ID)
		if e != nil || report.ArtifactID == nil || *report.ArtifactID != u.ID || !bytes.Equal(report.Result, metadata) {
			t.Fatal("actual retained report", e)
		}
		destination := filepath.Join(t.TempDir(), "export")
		if e = g.g.s.ExportFiles(g.g.ctx, destination); e != nil {
			t.Fatal("actual accepted file export", e)
		}
		imported := filepath.Join(t.TempDir(), "import")
		if e = os.Mkdir(imported, 0700); e != nil {
			t.Fatal(e)
		}
		originalStore := g.g.s.Store
		g.g.s.Store = nil
		if e = g.g.s.ImportFiles(g.g.ctx, destination, imported); e != nil {
			t.Fatal("actual accepted file import", e)
		}
		g.g.s.Store = originalStore
		restored, e := OpenStore(imported, g.g.f.Tenant, g.g.s.policy().MinimumFreeBytes, wire.MaximumModelBytes, wire.MaximumModelFiles)
		if e != nil {
			t.Fatal(e)
		}
		copyService := *g.g.s
		copyService.Store = restored
		if e = copyService.VerifyStored(g.g.ctx); e != nil {
			restored.Close()
			t.Fatal("restored selected repository readback", e)
		}
		restored.Close()
	})
}

// Isolated schema-owned fault injection changes no authentication checks or
// business tables. The action literal is selected only by this test source.
func runtimeRejectAudit(t *testing.T, g runtimeStory, action string) func() {
	t.Helper()
	if action != "model.preparation_dispatched" && action != "model.preparation_physical_stop_accepted" {
		t.Fatal("fixed test audit action required")
	}
	if g.g.f.Store.Dialect() == transaction.MySQL {
		g.g.exec(t, "", `CREATE TRIGGER reject_runtime_audit BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='`+action+`' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_RUNTIME_AUDIT_FAILURE'; END IF; END`)
		return func() { g.g.exec(t, "", `DROP TRIGGER reject_runtime_audit`) }
	}
	g.g.exec(t, `CREATE FUNCTION reject_runtime_audit_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='`+action+`' THEN RAISE EXCEPTION 'SYNTHETIC_RUNTIME_AUDIT_FAILURE'; END IF; RETURN NEW; END;$$`, "")
	g.g.exec(t, `CREATE TRIGGER reject_runtime_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_runtime_audit_fn()`, "")
	return func() {
		g.g.exec(t, `DROP TRIGGER reject_runtime_audit ON audit_events`, "")
		g.g.exec(t, `DROP FUNCTION reject_runtime_audit_fn()`, "")
	}
}
