// Package deployments owns scoped inference intent and durable accelerator ownership.
package deployments

import (
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"time"
)

type Service struct {
	Nodes           *nodes.Service
	Models          *models.Service
	RegistryActions RegistryActionOwner
}
type Deployment struct {
	ModelKind           string                      `json:"model_kind"`
	CatalogDigest       string                      `json:"catalog_digest"`
	ServingRecipeSHA256 *string                     `json:"serving_recipe_sha256"`
	ID                  string                      `json:"id"`
	WorkspaceID         string                      `json:"workspace_id"`
	NodeID              string                      `json:"node_id"`
	ModelID             string                      `json:"model_id"`
	GPUUUID             string                      `json:"gpu_uuid"`
	DesiredState        string                      `json:"desired_state"`
	ObservedState       string                      `json:"observed_state"`
	Revision            int64                       `json:"revision"`
	Observation         *wire.DeploymentObservation `json:"observation"`
	ObservedAt          *time.Time                  `json:"observed_at"`
	LastTaskID          *string                     `json:"last_task_id"`
	TaskState           *string                     `json:"task_state"`
	TaskErrorCode       *string                     `json:"task_error_code"`
	TaskDeadlineAt      *time.Time                  `json:"task_deadline_at"`
	ResourceState       string                      `json:"resource_state"`
	Stale               bool                        `json:"stale"`
	CreatedAt           time.Time                   `json:"created_at"`
	RemovedAt           *time.Time                  `json:"removed_at"`
}
type CreateRequest struct {
	NodeID  string `json:"node_id"`
	ModelID string `json:"model_id"`
	GPUUUID string `json:"gpu_uuid"`
	Reason  string `json:"reason"`
}
type ActionRequest struct {
	Action   string `json:"action"`
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
}
type NodeState struct {
	NodeID      string                     `json:"node_id"`
	Observation *wire.InferenceObservation `json:"observation"`
	ReceivedAt  *time.Time                 `json:"received_at"`
	Stale       bool                       `json:"stale"`
}
