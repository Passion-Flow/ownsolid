//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The published graph uses the production scope and integrity constraints. Its
// evaluation metadata is synthetic and no model files are needed for this read.
func publishLineageFixture(t *testing.T, g reviewStory, change func(*wire.DerivedCatalog)) *models.Service {
	t.Helper()
	c := wire.DerivedCatalog{
		SchemaVersion: 1, Kind: "merged_lora", Scope: g.c.Scope, ModelID: g.artifact,
		BaseModelID: g.model, BaseCatalogSHA256: wire.ApprovedModelCatalogSHA256,
		AttemptID: g.attempt, ArtifactID: g.artifact, ReportSHA256: g.input.ReportSHA256,
		RecipeSHA256: wire.RecipeDigest(), BaseRepository: wire.ApprovedBaseRepository,
		BaseRevision: wire.ApprovedBaseRevision, License: wire.ApprovedBaseLicense,
		Format: "safetensors", Architecture: wire.ApprovedBaseArchitecture,
		ModelType: wire.ApprovedBaseModelType, Precision: "BF16",
		MaximumContextTokens: wire.MaximumContextTokens, Files: wire.ApprovedBaseFiles(),
	}
	for i := range c.Files {
		if c.Files[i].Path == "model.safetensors" {
			c.Files[i].SHA256 = strings.Repeat("a", 64)
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(&c)
	}
	raw, err := c.CanonicalBytes()
	if err != nil {
		// The invalid artifact ID must still reach the production decoder.
		raw = reviewRaw(t, c)
	}
	digest, reviewID := hash(raw), uuid.NewString()
	name := "Synthetic published lineage model"
	if err := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		state := "ready"
		if q.Dialect() == transaction.MySQL {
			state = "staged"
		}
		g.seedWithin(t, q, "model_versions", map[string]any{
			"id": g.artifact, "name": name, "model_key": "qwen3-0.6b", "catalog_digest": digest,
			"manifest": reviewJSON(raw), "state": state, "kind": "merged_lora", "created_by": g.f.User,
		})
		g.seedWithin(t, q, "model_preparation_reviews", map[string]any{
			"id": reviewID, "preparation_id": g.id, "attempt_id": g.attempt, "artifact_id": g.artifact,
			"report_sha256": g.input.ReportSHA256, "expected_revision": 1, "decision": "approve",
			"model_id": g.artifact, "model_name": name, "reviewed_by": g.f.User, "audience": "admin",
			"reason": "Synthetic lineage approval", "idempotency_key": uuid.NewString(),
			"fingerprint": bytes.Repeat([]byte{9}, 32),
		})
		g.seedWithin(t, q, "model_derivations", map[string]any{
			"model_id": g.artifact, "preparation_id": g.id, "attempt_id": g.attempt, "artifact_id": g.artifact,
			"base_model_id": g.model, "review_id": reviewID, "catalog_digest": digest, "catalog": reviewJSON(raw),
			"report_sha256": g.input.ReportSHA256,
		})
		if _, err := q.Exec(ctx, g.f.Statement(
			"UPDATE model_preparations SET state='approved',review_id=$1,derived_model_id=$2,revision=revision+1 WHERE id=$3",
			"UPDATE model_preparations SET state='approved',review_id=?,derived_model_id=?,revision=revision+1 WHERE id=?",
			reviewID, g.artifact, g.id)); err != nil {
			return err
		}
		if q.Dialect() == transaction.MySQL {
			g.seedWithin(t, q, "model_publications", map[string]any{"model_id": g.artifact})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return &models.Service{DB: g.f.PostgreSQL, Identity: g.s.Identity, Workspaces: g.s.Workspaces}
}

func requireDerivedReadRefusal(t *testing.T, g reviewStory, s *models.Service, workspace, code string) {
	t.Helper()
	got, err := s.Get(g.ctx, g.a, workspace, g.artifact)
	if err == nil || err.Error() != code || !reflect.DeepEqual(got, models.Version{}) {
		t.Fatalf("derived detail refusal: id=%q, lineage=%+v, error=%v, want %s", got.ID, got.DatasetLineage, err, code)
	}
}

func TestPublishedModelDetailRetainsPreparationLineage(t *testing.T) {
	g := reviewGraph(t, reviewActorSetup(t), "lineage")
	s := publishLineageFixture(t, g, nil)
	p, err := g.s.Get(g.ctx, g.a, g.f.Workspace, g.id)
	if err != nil {
		t.Fatal(err)
	}
	// Use the pre-extraction field order and training descriptor type to verify
	// that a previously published snapshot keeps its original digest.
	legacy := struct {
		SchemaVersion          int                       `json:"schema_version"`
		TrainingCommand        trainingwire.Command      `json:"training_command"`
		Adapter                training.UploadDescriptor `json:"adapter"`
		TrainingIdentitySHA256 string                    `json:"training_identity_sha256"`
		Heldout                Holdout                   `json:"heldout"`
		DatasetAttemptID       string                    `json:"dataset_attempt_id"`
	}{
		SchemaVersion: 1, TrainingCommand: p.inputs.TrainingCommand,
		Adapter: training.UploadDescriptor(p.inputs.Adapter), TrainingIdentitySHA256: p.inputs.TrainingIdentitySHA256,
		Heldout: p.inputs.Heldout, DatasetAttemptID: p.inputs.DatasetAttemptID,
	}
	if hash(reviewRaw(t, legacy)) != p.InputSHA256 {
		t.Fatal("retained version 1 encoding changed")
	}
	want := p.DatasetLineage
	if want.Training.DatasetID == want.Holdout.DatasetID || want.Training.ManifestSHA256 == want.Holdout.ManifestSHA256 {
		t.Fatal("training and holdout must use separate fixture sources")
	}
	read := func(workspace string) models.Version {
		t.Helper()
		got, err := s.Get(g.ctx, g.a, workspace, g.artifact)
		if err != nil || got.ID != g.artifact || got.DatasetLineage == nil || *got.DatasetLineage != want {
			t.Fatalf("model detail: id=%q, lineage=%+v, error=%v, want %+v", got.ID, got.DatasetLineage, err, want)
		}
		return got
	}
	first := read(g.f.Workspace)
	withoutLineage := first
	withoutLineage.DatasetLineage = nil
	if !bytes.Equal(reviewRaw(t, first), reviewRaw(t, withoutLineage)) {
		t.Fatal("model detail added fields to the existing HTTP response")
	}

	g.seedDatasetVersion(t, want.Training.DatasetID, strings.Repeat("f", 64), 2, &want.Training.DatasetVersionID)
	read(g.f.Workspace)
	g.exec(t,
		"UPDATE dataset_versions SET retired_at=$1,retire_reason=$2 WHERE id=$3",
		"UPDATE dataset_versions SET retired_at=?,retire_reason=? WHERE id=?",
		time.Now().UTC(), "Superseded synthetic training source", want.Training.DatasetVersionID)
	after := read("")
	if first.CatalogDigest != after.CatalogDigest || !bytes.Equal(first.Manifest, after.Manifest) {
		t.Fatal("dataset retirement changed published model metadata")
	}

	other := uuid.NewString()
	g.exec(t,
		"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')",
		"INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')",
		other, g.f.Tenant, "Other lineage workspace")
	requireDerivedReadRefusal(t, g, s, other, "RESOURCE_NOT_FOUND")
}

func TestPublishedModelDetailRefusesConflictingPreparationReferences(t *testing.T) {
	g := reviewSetup(t)
	changes := []struct {
		name   string
		change func(*wire.DerivedCatalog)
	}{
		{"workspace", func(c *wire.DerivedCatalog) { c.Scope.WorkspaceID = uuid.NewString() }},
		{"preparation", func(c *wire.DerivedCatalog) { c.Scope.PreparationID = uuid.NewString() }},
		{"attempt", func(c *wire.DerivedCatalog) { c.AttemptID = uuid.NewString() }},
		{"artifact", func(c *wire.DerivedCatalog) { c.ArtifactID = uuid.NewString() }},
		{"base", func(c *wire.DerivedCatalog) { c.BaseModelID = uuid.NewString() }},
		{"report", func(c *wire.DerivedCatalog) { c.ReportSHA256 = strings.Repeat("f", 64) }},
	}
	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			fixture := reviewGraph(t, g, "lineage")
			s := publishLineageFixture(t, fixture, tt.change)
			requireDerivedReadRefusal(t, fixture, s, fixture.f.Workspace, "MODEL_DERIVED_CATALOG_INVALID")
		})
	}
	t.Run("snapshot_attempt", func(t *testing.T) {
		fixture := reviewGraph(t, g, "lineage_conflict")
		s := publishLineageFixture(t, fixture, nil)
		requireDerivedReadRefusal(t, fixture, s, fixture.f.Workspace, "MODEL_DERIVED_CATALOG_INVALID")
	})
	if g.f.Store.Dialect() != transaction.PostgreSQL {
		return
	}
	for _, tt := range []struct {
		name, table, trigger, column string
	}{
		{"artifact_report", "model_preparation_artifacts", "model_preparation_artifact_immutable", "manifest_sha256"},
		{"approval_report", "model_preparation_reviews", "model_preparation_review_immutable", "report_sha256"},
		{"snapshot_digest", "model_preparations", "model_preparation_inputs_immutable", "input_sha256"},
		{"training_dataset_manifest", "training_jobs", "training_job_inputs_immutable", "dataset_manifest_sha256"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := reviewGraph(t, g, "lineage")
			s := publishLineageFixture(t, fixture, nil)
			var id string
			switch tt.table {
			case "model_preparation_artifacts":
				id = fixture.artifact
			case "model_preparation_reviews":
				if err := fixture.f.Store.QueryRow(fixture.ctx, fixture.f.Statement(
					"SELECT review_id::text FROM model_preparations WHERE id=$1", "", fixture.id)).Scan(&id); err != nil {
					t.Fatal(err)
				}
			case "model_preparations":
				id = fixture.id
			case "training_jobs":
				id = fixture.job
			}
			// Inject a retained-row fault in the disposable schema only.
			fixture.exec(t, "ALTER TABLE "+tt.table+" DISABLE TRIGGER "+tt.trigger, "")
			fixture.exec(t, "UPDATE "+tt.table+" SET "+tt.column+"=$1 WHERE id=$2", "", strings.Repeat("f", 64), id)
			fixture.exec(t, "ALTER TABLE "+tt.table+" ENABLE TRIGGER "+tt.trigger, "")
			requireDerivedReadRefusal(t, fixture, s, fixture.f.Workspace, "MODEL_DERIVED_CATALOG_INVALID")
		})
	}
}
