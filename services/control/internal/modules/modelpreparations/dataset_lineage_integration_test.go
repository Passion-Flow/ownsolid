//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPreparationDetailRetainsDatasetLineage(t *testing.T) {
	g := reviewGraph(t, reviewActorSetup(t), "lineage")
	before, err := g.s.Get(g.ctx, g.a, g.f.Workspace, g.id)
	if err != nil {
		t.Fatal(err)
	}
	command, holdout := before.inputs.TrainingCommand, before.inputs.Heldout
	want := PreparationDatasetLineage{
		Training: DatasetVersionLineage{command.DatasetID, command.DatasetVersionID, strings.Repeat("a", 64)},
		Holdout:  DatasetVersionLineage{holdout.DatasetID, holdout.DatasetVersionID, strings.Repeat("e", 64)},
	}
	if before.inputs.SchemaVersion != 1 || before.DatasetLineage != want ||
		want.Training.DatasetID == want.Holdout.DatasetID ||
		want.Training.DatasetVersionID == want.Holdout.DatasetVersionID {
		t.Fatalf("version 1 preparation lineage = %+v, want %+v", before.DatasetLineage, want)
	}
	withoutLineage := before
	withoutLineage.DatasetLineage = PreparationDatasetLineage{}
	if !bytes.Equal(reviewRaw(t, before), reviewRaw(t, withoutLineage)) {
		t.Fatal("dataset lineage changed the serialized preparation detail")
	}

	g.seedDatasetVersion(t, command.DatasetID, strings.Repeat("f", 64), 2, &command.DatasetVersionID)
	afterPublish, err := g.s.Get(g.ctx, g.a, g.f.Workspace, g.id)
	if err != nil || afterPublish.DatasetLineage != want {
		t.Fatalf("after new version: %+v, %v, want %+v", afterPublish.DatasetLineage, err, want)
	}
	g.exec(t,
		"UPDATE dataset_versions SET retired_at=$1,retire_reason=$2 WHERE id=$3",
		"UPDATE dataset_versions SET retired_at=?,retire_reason=? WHERE id=?",
		time.Now().UTC(), "Superseded synthetic source", command.DatasetVersionID)
	afterRetire, err := g.s.Get(g.ctx, g.a, g.f.Workspace, g.id)
	if err != nil || afterRetire.DatasetLineage != want {
		t.Fatalf("after retirement: %+v, %v, want %+v", afterRetire.DatasetLineage, err, want)
	}

	otherWorkspace := uuid.NewString()
	g.exec(t,
		"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')",
		"INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')",
		otherWorkspace, g.f.Tenant, "Other repository workspace")
	other, err := g.s.Get(g.ctx, g.a, otherWorkspace, g.id)
	if err == nil || err.Error() != "MODEL_PREPARATION_NOT_FOUND" || other.DatasetLineage != (PreparationDatasetLineage{}) {
		t.Fatalf("cross-workspace detail: id=%q, lineage=%+v, error=%v", other.ID, other.DatasetLineage, err)
	}

	conflict := reviewGraph(t, g, "lineage_conflict")
	invalid, err := g.s.Get(g.ctx, g.a, g.f.Workspace, conflict.id)
	if err == nil || err.Error() != "MODEL_PREPARATION_DATA_INVALID" || invalid.DatasetLineage != (PreparationDatasetLineage{}) {
		t.Fatalf("conflicting snapshot detail: id=%q, lineage=%+v, error=%v", invalid.ID, invalid.DatasetLineage, err)
	}
}
