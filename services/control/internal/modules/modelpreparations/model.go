package modelpreparations

import (
	"encoding/json"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type CreateRequest struct {
	WorkspaceID   string             `json:"workspace_id"`
	TrainingJobID string             `json:"training_job_id"`
	HeldoutID     string             `json:"heldout_id"`
	NodeID        string             `json:"node_id"`
	GPUUUID       string             `json:"gpu_uuid"`
	Configuration wire.Configuration `json:"configuration"`
	Reason        string             `json:"reason"`
}

type RecoveryRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	NodeID           string `json:"node_id"`
	Reason           string `json:"reason"`
}

type ActionRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type preparationInputs struct {
	SchemaVersion          int                       `json:"schema_version"`
	TrainingCommand        trainingwire.Command      `json:"training_command"`
	Adapter                training.UploadDescriptor `json:"adapter"`
	TrainingIdentitySHA256 string                    `json:"training_identity_sha256"`
	Heldout                Holdout                   `json:"heldout"`
	DatasetAttemptID       string                    `json:"dataset_attempt_id"`
}

func (i preparationInputs) digest() string { raw, _ := json.Marshal(i); return hash(raw) }
func (i preparationInputs) validate() error {
	c, d, h := i.TrainingCommand, i.Adapter, i.Heldout
	if i.SchemaVersion != 1 || c.Validate() != nil || c.ModelCatalogSHA256 != wire.ApprovedModelCatalogSHA256 || d.Validate(c.Recipe.Resources.MaximumOutputBytes, c.Recipe.Resources.MaximumOutputFiles) != nil || d.Kind != "artifact" || len(d.Files) != 3 || d.Scope != c.Scope || d.AttemptID != c.AttemptID || (training.AcceptedAdapter{Command: c, Descriptor: d}).IdentitySHA256() != i.TrainingIdentitySHA256 || !datawire.ValidID(i.DatasetAttemptID) || !datawire.ValidID(h.ID) || h.WorkspaceID != c.Scope.WorkspaceID || !datawire.ValidID(h.DatasetID) || !datawire.ValidID(h.DatasetVersionID) || !datawire.ValidDigest(h.ManifestSHA256) || !datawire.ValidDigest(h.EffectiveSourceSHA256) || !datawire.ValidDigest(h.RowMappingSHA256) || h.SourceBytes < 1 || h.SourceBytes > datawire.MaximumBytesCeiling || h.RowCount < wire.MinimumRows || h.RowCount > wire.MaximumRows || !h.Labels.valid(h.RowCount) || h.RecipeID != wire.RecipeID || h.RecipeSHA256 != wire.RecipeDigest() || h.InstructionSHA256 != wire.InstructionDigest() || h.FingerprintAlgorithm != wire.PromptFingerprintAlgorithm {
		return fail("MODEL_PREPARATION_DATA_INVALID")
	}
	return nil
}
func (i preparationInputs) inventory() wire.InputInventory {
	return wire.InputInventory{AdapterFiles: append([]datawire.Asset{}, i.Adapter.Files...), Heldout: datawire.Asset{Path: "heldout.jsonl", SizeBytes: i.Heldout.SourceBytes, SHA256: i.Heldout.EffectiveSourceSHA256}}
}

type Preparation struct {
	ReviewID              *string              `json:"review_id"`
	DerivedModelID        *string              `json:"derived_model_id"`
	PendingRecoveryID     *string              `json:"pending_recovery_id"`
	ID                    string               `json:"id"`
	WorkspaceID           string               `json:"workspace_id"`
	NodeID                string               `json:"node_id"`
	CurrentNodeID         string               `json:"current_node_id"`
	ModelID               string               `json:"model_id"`
	TrainingJobID         string               `json:"training_job_id"`
	TrainingAttemptID     string               `json:"training_attempt_id"`
	TrainingArtifactID    string               `json:"training_artifact_id"`
	HeldoutID             string               `json:"heldout_id"`
	GPUUUID               string               `json:"gpu_uuid"`
	InputSHA256           string               `json:"input_sha256"`
	TrainingResultSHA256  string               `json:"training_result_sha256"`
	HeldoutManifestSHA256 string               `json:"heldout_manifest_sha256"`
	Configuration         wire.Configuration   `json:"configuration"`
	ConfigurationSHA256   string               `json:"configuration_sha256"`
	Recipe                wire.ExecutionRecipe `json:"recipe"`
	CreatedBy             string               `json:"created_by"`
	Reason                string               `json:"reason"`
	State                 string               `json:"state"`
	Revision              int64                `json:"revision"`
	AttemptNumber         int                  `json:"attempt_number"`
	CurrentAttemptID      *string              `json:"current_attempt_id"`
	WaitCode              *string              `json:"wait_code"`
	ErrorCode             *string              `json:"error_code"`
	CancelRequestedAt     *time.Time           `json:"cancel_requested_at"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
	FinishedAt            *time.Time           `json:"finished_at"`
	tenant                string
	inputs                preparationInputs
}

func validPreparationState(state string) bool {
	switch state {
	case "queued", "preparing", "running", "cancelling", "recovery_required", "quality_failed", "awaiting_review", "approved", "rejected", "cancelled", "failed":
		return true
	}
	return false
}
