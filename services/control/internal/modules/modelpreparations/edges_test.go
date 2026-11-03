package modelpreparations_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func clonePublished(t *testing.T, f scenario, source datasets.Version) datasets.Version {
	t.Helper()
	ctx := context.Background()
	draft, e := f.datasets.CloneVersion(ctx, f.portal, source.WorkspaceID, source.DatasetID, source.ID, "Actual clone lineage verification", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := f.datasets.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("clone inspection", worked, e)
	}
	draft, e = f.datasets.GetDraft(ctx, f.portal, source.WorkspaceID, source.DatasetID, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	job, e := f.datasets.Publish(ctx, f.portal, source.WorkspaceID, source.DatasetID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Actual cloned immutable split"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := f.datasets.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("clone publication", worked, e)
	}
	job, e = f.datasets.GetJob(ctx, f.portal, source.WorkspaceID, source.DatasetID, job.ID)
	if e != nil || job.State != "succeeded" || job.VersionID == nil {
		t.Fatal("clone completion", e, job.State)
	}
	value, e := f.datasets.GetVersion(ctx, f.portal, source.WorkspaceID, source.DatasetID, *job.VersionID)
	if e != nil || value.ParentVersionID == nil || *value.ParentVersionID != source.ID {
		t.Fatal("real parent lineage", e)
	}
	return value
}

func TestActualHoldoutRejectsHistoricalValidationAndPublishedCloneAfterRetirement(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	service := trainingService(t, f)
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("historical-training"))
	if job, e := service.Create(ctx, f.portal, trainingInput(t, f, service, version), uuid.NewString(), uuid.NewString()); e != nil || job.State != "queued" {
		t.Fatal("historical admission", e)
	}
	file, _, e := f.datasets.VersionFile(ctx, f.portal, version.WorkspaceID, version.DatasetID, version.ID, "validation.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		file.Close()
		t.Fatal("real validation partition is empty", scanner.Err())
	}
	var validation datawire.Record
	e = json.Unmarshal(scanner.Bytes(), &validation)
	file.Close()
	if e != nil {
		t.Fatal(e)
	}
	selected := rows("independent-source-groups")
	selected[0].Prompt = validation.Prompt
	selected[0].Completion = "account"
	validationOnly := publish(t, f, version.WorkspaceID, selected)
	_, e = f.s.ReserveHoldout(ctx, f.portal, input(validationOnly), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_OVERLAP")
	cloned := clonePublished(t, f, version)
	if _, e = f.datasets.RetireVersion(ctx, f.portal, version.WorkspaceID, version.DatasetID, version.ID, "Retain historical training lineage", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.ReserveHoldout(ctx, f.portal, input(cloned), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_OVERLAP")
	if count(t, f, "SELECT count(*) FROM model_holdouts") != 0 {
		t.Fatal("historical leakage reserved")
	}
}

func TestActualHoldoutWorkspaceIsolationRetirementAndExactReplay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("reserved-retirement"))
	key := uuid.NewString()
	holdout, e := f.s.ReserveHoldout(ctx, f.portal, input(version), key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	other, e := f.workspaces.Create(ctx, f.admin, "Other isolated synthetic Workspace", f.portal.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.GetHoldout(ctx, f.portal, other.ID, holdout.ID)
	expected(t, e, "MODEL_HOLDOUT_NOT_FOUND")
	page, e := f.s.ListHoldouts(ctx, f.portal, other.ID, url.Values{})
	if e != nil || len(page.Items) != 0 {
		t.Fatal("cross Workspace list", e)
	}
	wrong := input(version)
	wrong.WorkspaceID = other.ID
	_, e = f.s.ReserveHoldout(ctx, f.portal, wrong, uuid.NewString(), uuid.NewString())
	expected(t, e, "DATASET_NOT_FOUND")
	if _, e = f.datasets.RetireVersion(ctx, f.portal, version.WorkspaceID, version.DatasetID, version.ID, "Retain immutable reservation after retirement", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	replayed, e := f.s.ReserveHoldout(ctx, f.portal, input(version), key, uuid.NewString())
	if e != nil || replayed.ID != holdout.ID {
		t.Fatal("retired exact replay lost immutable result", e)
	}
	_, e = f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
	expected(t, e, "DATASET_RETIRED")
	if _, e = f.s.GetHoldout(ctx, f.portal, version.WorkspaceID, holdout.ID); e != nil {
		t.Fatal("retirement erased historical metadata", e)
	}
	if count(t, f, "SELECT count(*) FROM model_holdout_prompts") != 6 {
		t.Fatal("retirement released protected prompts")
	}
}

func TestActualHoldoutMissingAndCorruptPrivateSourceFailAndRecover(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("source-integrity"))
	path := filepath.Join(f.identity.Config.Datasets.ArtifactRoot, "versions", version.ID, version.AttemptID, "effective.jsonl")
	saved := path + ".test-retained"
	if e := os.Rename(path, saved); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("missing source accepted")
	}
	if e := os.Rename(saved, path); e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	// Only this isolated test's immutable files are damaged; restore original bytes before retry.
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	tampered := append([]byte(nil), original...)
	tampered[0] ^= 1
	if e = os.WriteFile(path, tampered, 0600); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_HOLDOUT_SOURCE_CHANGED")
	if count(t, f, "SELECT count(*) FROM model_holdouts") != 0 {
		t.Fatal("damaged source left reservation")
	}
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(path, 0400); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal("repaired exact immutable source refused", e)
	}
}

func TestActualHoldoutScanDeadlineReleasesSlotAndAllowsExplicitRetry(t *testing.T) {
	f := setup(t)
	version := publish(t, f, f.portal.Me.Workspace.ID, rows("deadline"))
	blocked, release := blockRegistry(t, &f)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	outcomes := make(chan holdoutOutcome, 1)
	go func() {
		value, e := f.s.ReserveHoldout(ctx, f.portal, input(version), uuid.NewString(), uuid.NewString())
		outcomes <- holdoutOutcome{value, e}
	}()
	waitScan(t, blocked)
	result := waitHoldout(t, outcomes)
	expected(t, result.err, "MODEL_HOLDOUT_ANALYSIS_TIMEOUT")
	release()
	if count(t, f, "SELECT count(*) FROM model_holdouts") != 0 {
		t.Fatal("timeout left partial result")
	}
	f.s.Datasets = f.datasets
	if _, e := f.s.ReserveHoldout(context.Background(), f.portal, input(version), uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal("timeout retained scan slot", e)
	}
}
