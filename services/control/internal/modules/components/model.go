package components

import (
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"time"
)

type RecipeAvailability struct {
	Version        string `json:"version"`
	Available      bool   `json:"available"`
	ForwardUpgrade bool   `json:"forward_upgrade"`
}
type State struct {
	NodeID               string                     `json:"node_id"`
	Component            string                     `json:"component"`
	Version              string                     `json:"version"`
	DesiredState         string                     `json:"desired_state"`
	DesiredVersion       string                     `json:"desired_version"`
	DesiredConfig        *wire.ComponentConfig      `json:"desired_config"`
	AppliedConfig        *wire.ComponentConfig      `json:"applied_config"`
	PreviousVersion      *string                    `json:"previous_version"`
	LastVerifiedAt       *time.Time                 `json:"last_verified_at"`
	Observation          *wire.ComponentObservation `json:"observation"`
	ReceivedAt           *time.Time                 `json:"received_at"`
	Stale                bool                       `json:"stale"`
	ManagementAvailable  bool                       `json:"management_available"`
	MaintenanceAvailable bool                       `json:"maintenance_available"`
	ArtifactAvailable    bool                       `json:"artifact_available"`
	ApprovedVersions     []RecipeAvailability       `json:"approved_versions"`
	RemovalPolicy        string                     `json:"removal_policy"`
	LastTaskID           *string                    `json:"last_task_id"`
}
type Request struct {
	Action  string                `json:"action"`
	Reason  string                `json:"reason"`
	Version string                `json:"version,omitempty"`
	Config  *wire.ComponentConfig `json:"config,omitempty"`
}
type LogSnapshot struct {
	NodeID     string              `json:"node_id"`
	Snapshot   *wire.ComponentLogs `json:"snapshot"`
	TaskID     *string             `json:"task_id"`
	ReceivedAt *time.Time          `json:"received_at"`
	Stale      bool                `json:"stale"`
}
