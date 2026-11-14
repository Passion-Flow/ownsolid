package modelpreparations

import (
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations/snapshot"
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

type preparationInputs snapshot.Inputs

func (i preparationInputs) digest() string                 { return snapshot.Inputs(i).Digest() }
func (i preparationInputs) validate() error                { return snapshot.Inputs(i).Validate() }
func (i preparationInputs) inventory() wire.InputInventory { return snapshot.Inputs(i).Inventory() }

// DatasetVersionLineage identifies a frozen dataset version and its manifest.
type DatasetVersionLineage = snapshot.DatasetVersionLineage

// PreparationDatasetLineage separates training from preparation evaluation.
type PreparationDatasetLineage = snapshot.DatasetLineage

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
	// DatasetLineage is populated by the scoped detail read for internal consumers.
	DatasetLineage PreparationDatasetLineage `json:"-"`
	tenant         string
	inputs         preparationInputs
}

func validPreparationState(state string) bool {
	switch state {
	case "queued", "preparing", "running", "cancelling", "recovery_required", "quality_failed", "awaiting_review", "approved", "rejected", "cancelled", "failed":
		return true
	}
	return false
}
