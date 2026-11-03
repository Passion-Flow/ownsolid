//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
)

// This explicit test wrapper first opens/hash-verifies the actual immutable private source, then pauses outside locks.
type pausedPreparationSource struct {
	*datasets.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *pausedPreparationSource) OpenPublishedSource(ctx context.Context, version datasets.Version, maximum int64) (*os.File, int64, error) {
	file, size, e := p.Service.OpenPublishedSource(ctx, version, maximum)
	if e != nil {
		return nil, 0, e
	}
	var cancelled bool
	first := false
	p.once.Do(func() { first = true; close(p.entered) })
	if first {
		select {
		case <-p.release:
		case <-ctx.Done():
			cancelled = true
		}
	}
	if cancelled {
		file.Close()
		return nil, 0, ctx.Err()
	}
	return file, size, nil
}

type preparationOutcome struct {
	value modelpreparations.Preparation
	err   error
}

func pausePreparationCreate(t *testing.T, g preparationScenario, key string) (func(), <-chan preparationOutcome) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	paused := &pausedPreparationSource{Service: g.f.s.Datasets, entered: make(chan struct{}), release: make(chan struct{})}
	g.service.Datasets = paused
	var once sync.Once
	release := func() { once.Do(func() { close(paused.release) }) }
	out := make(chan preparationOutcome, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		value, e := g.service.Create(ctx, g.f.portal, g.input, key, uuid.NewString())
		out <- preparationOutcome{value, e}
	}()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("paused admission did not stop before fixture cleanup")
		}
	})
	select {
	case <-paused.entered:
	case <-done:
		result := <-out
		t.Fatal("actual source pause not reached", result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("actual source pause deadline")
	}
	return release, out
}
func preparationResult(t *testing.T, out <-chan preparationOutcome) preparationOutcome {
	t.Helper()
	select {
	case value := <-out:
		return value
	case <-time.After(15 * time.Second):
		t.Fatal("preparation admission completion timeout")
		return preparationOutcome{}
	}
}

func TestActualCpuModelPreparationSameKeyConcurrentAdmissionCommitsExactlyOneIntent(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	policy := g.f.s.Identity.Config.Policy()
	policy.ModelLifecycle.ScanConcurrency = 2
	policy.ModelLifecycle.ScanSeconds = 20
	g.f.s.Identity.Config.Runtime = &policy
	service, e := modelpreparations.New(g.f.s.DB, g.f.s.Identity, g.f.s.Workspaces, g.f.s.Datasets)
	if e != nil {
		t.Fatal(e)
	}
	service.Store = g.store
	service.Training = g.f.s
	service.Nodes = g.f.s.Nodes
	service.Models = g.f.s.Models
	g.service = service
	g.f.s.Holdouts = service
	key := uuid.NewString()
	release, out := pausePreparationCreate(t, g, key)
	second, e := g.service.Create(ctx, g.f.portal, g.input, key, uuid.NewString())
	if e != nil || second.State != "queued" {
		t.Fatal("second source scan could not commit outside first scan's authority locks", e)
	}
	release()
	first := preparationResult(t, out)
	if first.err != nil || first.value.ID != second.ID {
		t.Fatal("same-key concurrent replay differed", first.err)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparations") != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_created'") != 1 || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 0 {
		t.Fatal("concurrent admission had duplicate/physical effects")
	}
}

func TestActualCpuModelPreparationOutsideLockScanRechecksRoleEpochAndRecipeAtCommit(t *testing.T) {
	for _, kind := range []string{"role", "epoch", "recipe"} {
		t.Run(kind, func(t *testing.T) {
			g := newPreparationScenario(t)
			ctx := context.Background()
			policy := g.f.s.Identity.Config.Policy()
			policy.ModelLifecycle.ScanSeconds = 20
			policy.ModelLifecycle.MaxActivePerWorkspace = 1
			g.f.s.Identity.Config.Runtime = &policy
			initial, e := g.service.Create(ctx, g.f.portal, g.input, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			release, out := pausePreparationCreate(t, g, uuid.NewString())
			// A real authorized cancellation must finish while the second source descriptor remains paused/open.
			cancelled, e := g.service.Cancel(ctx, g.f.portal, g.input.WorkspaceID, initial.ID, modelpreparations.ActionRequest{ExpectedRevision: initial.Revision, Reason: "Prove source analysis released all authority locks"}, uuid.NewString())
			if e != nil || cancelled.State != "cancelled" {
				t.Fatal("source read retained Workspace authority locks", e)
			}
			switch kind {
			case "role":
				_, e = g.f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", g.input.WorkspaceID, g.f.portal.Me.User.ID)
			case "epoch":
				_, e = g.f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", g.f.portal.Me.User.ID)
			case "recipe":
				g.f.s.Identity.Config.ModelPreparation.WorkerSourceSHA256 = strings.Repeat("f", 64)
			}
			if e != nil {
				t.Fatal(e)
			}
			release()
			result := preparationResult(t, out)
			if result.err == nil {
				t.Fatal("stale authority or changed recipe admitted")
			}
			if kind == "role" {
				expected(t, result.err, "ACCESS_DENIED")
			}
			if kind == "recipe" {
				expected(t, result.err, "MODEL_PREPARATION_RECIPE_UNAVAILABLE")
			}
			if count(t, g.f, "SELECT count(*) FROM model_preparations") != 1 || count(t, g.f, "SELECT count(*) FROM model_preparations WHERE state='queued'") != 0 {
				t.Fatal("stale preflight committed a second intent")
			}
		})
	}
}

func TestActualCpuModelPreparationCreationAuditSourceFailureRetirementAndForeignScope(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	f := g.f
	if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_creation_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_created' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_CREATION_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_creation_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_creation_audit()`); e != nil {
		t.Fatal(e)
	}
	key := uuid.NewString()
	if _, e := g.service.Create(ctx, f.portal, g.input, key, uuid.NewString()); e == nil {
		t.Fatal("creation audit failure accepted")
	}
	if count(t, f, "SELECT count(*) FROM model_preparations") != 0 {
		t.Fatal("creation audit failure retained intent")
	}
	if _, e := f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_creation_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	first, e := g.service.Create(ctx, f.portal, g.input, key, uuid.NewString())
	if e != nil {
		t.Fatal("same-key retry after audit repair", e)
	}
	path := filepath.Join(f.s.Identity.Config.Datasets.ArtifactRoot, "versions", g.version.ID, g.version.AttemptID, "effective.jsonl")
	retained := path + ".test-retained"
	if e = os.Rename(path, retained); e != nil {
		t.Fatal(e)
	}
	_, e = g.service.Create(ctx, f.portal, g.input, uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_SOURCE_CHANGED")
	if count(t, f, "SELECT count(*) FROM model_preparations") != 1 {
		t.Fatal("missing source admitted")
	}
	if e = os.Rename(retained, path); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Datasets.RetireVersion(ctx, f.portal, g.version.WorkspaceID, g.version.DatasetID, g.version.ID, "Retain exact reserved source for preparation", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	second, e := g.service.Create(ctx, f.portal, g.input, uuid.NewString(), uuid.NewString())
	if e != nil || second.InputSHA256 != first.InputSHA256 {
		t.Fatal("retired retained holdout lost immutable binding", e)
	}
	other, e := f.s.Workspaces.Create(ctx, f.admin, "Isolated preparation scope", f.portal.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.service.Get(ctx, f.portal, other.ID, first.ID)
	expected(t, e, "MODEL_PREPARATION_NOT_FOUND")
	foreign := g.input
	foreign.WorkspaceID = other.ID
	_, e = g.service.Create(ctx, f.portal, foreign, uuid.NewString(), uuid.NewString())
	expected(t, e, "RESOURCE_NOT_FOUND")
	if count(t, f, "SELECT count(*) FROM model_preparations") != 2 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 0 {
		t.Fatal("failed scope/source/audit effect")
	}
}
