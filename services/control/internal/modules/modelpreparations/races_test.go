package modelpreparations_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// This test-only wrapper calls the real private hash-verified source reader, then pauses outside every DB transaction.
type blockingSources struct {
	*datasets.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingSources) OpenPublishedSource(ctx context.Context, version datasets.Version, maximum int64) (*os.File, int64, error) {
	file, size, e := b.Service.OpenPublishedSource(ctx, version, maximum)
	if e != nil {
		return nil, 0, e
	}
	cancelled := false
	b.once.Do(func() {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			cancelled = true
		}
	})
	if cancelled {
		file.Close()
		return nil, 0, ctx.Err()
	}
	return file, size, nil
}

type holdoutOutcome struct {
	value modelpreparations.Holdout
	err   error
}

func blockRegistry(t *testing.T, f *scenario) (*blockingSources, func()) {
	t.Helper()
	policy := config.DefaultRuntimePolicy()
	policy.ModelLifecycle.ScanSeconds = 20
	f.identity.Config.Runtime = &policy
	service, e := modelpreparations.New(f.identity.DB, f.identity, f.workspaces, f.datasets)
	if e != nil {
		t.Fatal(e)
	}
	f.s = service
	blocked := &blockingSources{Service: f.datasets, entered: make(chan struct{}), release: make(chan struct{})}
	f.s.Datasets = blocked
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(blocked.release) }) }
	t.Cleanup(release)
	return blocked, release
}
func waitScan(t *testing.T, blocked *blockingSources) {
	t.Helper()
	select {
	case <-blocked.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("actual source reader never entered")
	}
}
func waitHoldout(t *testing.T, result <-chan holdoutOutcome) holdoutOutcome {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(15 * time.Second):
		t.Fatal("held-out transaction did not finish")
		return holdoutOutcome{}
	}
}
func TestActualHoldoutOutsideLockScanRejectsNewTrainingReferenceAtCommit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("concurrent"))
	blocked, release := blockRegistry(t, &f)
	training := trainingService(t, f)
	outcomes := make(chan holdoutOutcome, 1)
	go func() {
		value, e := f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
		outcomes <- holdoutOutcome{value, e}
	}()
	waitScan(t, blocked)
	// This real job creation must commit while the held-out file remains open/paused.
	job, e := training.Create(ctx, f.portal, trainingInput(t, f, training, version), uuid.NewString(), uuid.NewString())
	if e != nil || job.State != "queued" {
		t.Fatal("source scan held authority locks or blocked safe first admission", e)
	}
	release()
	result := waitHoldout(t, outcomes)
	expected(t, result.err, "MODEL_HOLDOUT_CONCURRENT_TRAINING_RETRY")
	if count(t, f, "SELECT count(*) FROM model_holdouts") != 0 || count(t, f, "SELECT count(*) FROM training_jobs") != 1 {
		t.Fatal("concurrent overlapping intents both committed")
	}
	f.s.Datasets = f.datasets
	_, e = f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_OVERLAP")
}
func TestActualHoldoutRechecksOwnerAfterOutsideLockSourceRead(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("authority"))
	blocked, release := blockRegistry(t, &f)
	outcomes := make(chan holdoutOutcome, 1)
	go func() {
		value, e := f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
		outcomes <- holdoutOutcome{value, e}
	}()
	waitScan(t, blocked)
	if _, e := f.identity.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", version.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	release()
	result := waitHoldout(t, outcomes)
	expected(t, result.err, "ACCESS_DENIED")
	if count(t, f, "SELECT count(*) FROM model_holdouts") != 0 || count(t, f, "SELECT count(*) FROM model_holdout_sources") != 0 {
		t.Fatal("stale owner mutated reservations")
	}
}
func TestActualHoldoutAnalysisConcurrencyAndNondefaultBudgetsLeaveNoPartialReservation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("slots"))
	blocked, release := blockRegistry(t, &f)
	outcomes := make(chan holdoutOutcome, 1)
	go func() {
		value, e := f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
		outcomes <- holdoutOutcome{value, e}
	}()
	waitScan(t, blocked)
	_, e := f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_SCAN_BUSY")
	release()
	if result := waitHoldout(t, outcomes); result.err != nil {
		t.Fatal(result.err)
	}
	f.s.Datasets = f.datasets
	large := rows("larger")
	// Valid long text exceeds the explicitly smaller scan byte budget, without invalid JSON or source repair.
	for index := range large {
		for len(large[index].Prompt) < 600 {
			large[index].Prompt += " synthetic"
		}
	}
	selected := publish(t, f, version.WorkspaceID, large)
	policy := config.DefaultRuntimePolicy()
	policy.ModelLifecycle.ScanMaximumBytes = 1024
	f.identity.Config.Runtime = &policy
	limited, e := modelpreparations.New(f.identity.DB, f.identity, f.workspaces, f.datasets)
	if e != nil {
		t.Fatal(e)
	}
	_, e = limited.ReserveHoldout(ctx, f.portal, input(selected), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_ANALYSIS_LIMIT")
	policy.ModelLifecycle.ScanMaximumBytes = 67108864
	policy.ModelLifecycle.ScanMaximumRows = 3
	f.identity.Config.Runtime = &policy
	more := append(rows("row-limit-a"), rows("row-limit-b")...)
	selected = publish(t, f, version.WorkspaceID, more)
	_, e = limited.ReserveHoldout(ctx, f.portal, input(selected), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_ANALYSIS_LIMIT")
	policy.ModelLifecycle.ScanMaximumRows = 100000
	policy.ModelLifecycle.MaxHoldoutsPerWorkspace = 1
	f.identity.Config.Runtime = &policy
	selected = publish(t, f, version.WorkspaceID, rows("holdout-limit"))
	_, e = limited.ReserveHoldout(ctx, f.portal, input(selected), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_LIMIT_REACHED")
	if count(t, f, "SELECT count(*) FROM model_holdouts") != 1 {
		t.Fatal("failed budget admitted a partial second holdout")
	}
}
