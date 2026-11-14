// Package training owns scoped durable fine-tuning intent, attempt fences and accepted artifacts.
package training

import (
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type ExecutionRecipe = wire.ExecutionRecipe

type CreateRequest struct {
	WorkspaceID      string             `json:"workspace_id"`
	NodeID           string             `json:"node_id"`
	ModelID          string             `json:"model_id"`
	DatasetID        string             `json:"dataset_id"`
	DatasetVersionID string             `json:"dataset_version_id"`
	GPUUUID          string             `json:"gpu_uuid"`
	Configuration    wire.Configuration `json:"configuration"`
	Reason           string             `json:"reason"`
}

type Job struct {
	ID                    string             `json:"id"`
	WorkspaceID           string             `json:"workspace_id"`
	NodeID                string             `json:"node_id"`
	CurrentNodeID         string             `json:"current_node_id"`
	ModelID               string             `json:"model_id"`
	DatasetID             string             `json:"dataset_id"`
	DatasetVersionID      string             `json:"dataset_version_id"`
	GPUUUID               string             `json:"gpu_uuid"`
	Configuration         wire.Configuration `json:"configuration"`
	ConfigurationSHA256   string             `json:"configuration_sha256"`
	Recipe                ExecutionRecipe    `json:"recipe"`
	DatasetManifestSHA256 string             `json:"dataset_manifest_sha256"`
	ModelCatalogSHA256    string             `json:"model_catalog_sha256"`
	CreatedBy             string             `json:"created_by"`
	Reason                string             `json:"reason"`
	State                 string             `json:"state"`
	WaitCode              *string            `json:"wait_code"`
	Revision              int64              `json:"revision"`
	AttemptNumber         int                `json:"attempt_number"`
	CurrentAttemptID      *string            `json:"current_attempt_id"`
	AcceptedArtifactID    *string            `json:"accepted_artifact_id"`
	PendingRecoveryID     *string            `json:"pending_recovery_id"`
	CancelRequestedAt     *time.Time         `json:"cancel_requested_at"`
	ErrorCode             *string            `json:"error_code"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
	FinishedAt            *time.Time         `json:"finished_at"`
}

type ActionRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
